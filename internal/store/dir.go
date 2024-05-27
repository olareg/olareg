package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	// imports required for go-digest
	_ "crypto/sha256"
	_ "crypto/sha512"

	"github.com/opencontainers/go-digest"

	"github.com/olareg/olareg/config"
	"github.com/olareg/olareg/internal/cache"
	"github.com/olareg/olareg/internal/slog"
	"github.com/olareg/olareg/types"
)

type dir struct {
	mu    sync.Mutex
	wg    sync.WaitGroup
	root  string
	repos *cache.Cache[string, *dirRepo]
	log   slog.Logger
	conf  config.Config
	stop  chan struct{}
}

type dirRepo struct {
	mu        sync.Mutex
	wg        sync.WaitGroup
	wgBlock   chan struct{}
	timeCheck time.Time
	timeMod   time.Time
	name      string
	path      string
	exists    bool
	index     types.Index
	uploads   *cache.Cache[string, *dirRepoUpload]
	log       slog.Logger
	conf      config.Config
}

type dirRepoUpload struct {
	fh        *os.File
	w         io.Writer
	size      int64
	d         digest.Digester
	expect    digest.Digest
	path      string
	filename  string
	dr        *dirRepo
	sessionID string
}

// NewDir returns a directory store.
func NewDir(conf config.Config, opts ...Opts) Store {
	sc := storeConf{}
	for _, opt := range opts {
		opt(&sc)
	}
	cacheOpts := cache.Opts[string, *dirRepo]{
		PruneFn: func(_ string, dr *dirRepo) error {
			if !dr.uploads.IsEmpty() {
				return fmt.Errorf("uploads in progress")
			}
			// warning, this will block, ensure repos are always held open for a minimal time (this mostly affects the design of tests)
			if !*dr.conf.Storage.ReadOnly {
				if err := dr.gc(); err != nil {
					return err
				}
			}
			return nil
		},
	}
	// TODO: consider a config for an upper limit on the number of repos
	if conf.Storage.GC.GracePeriod > 0 {
		cacheOpts.Age = conf.Storage.GC.GracePeriod
	}
	d := &dir{
		root:  conf.Storage.RootDir,
		repos: cache.New[string, *dirRepo](cacheOpts),
		log:   sc.log,
		conf:  conf,
		stop:  make(chan struct{}),
	}
	if d.log == nil {
		d.log = slog.Null{}
	}
	if !*d.conf.Storage.ReadOnly && d.conf.Storage.GC.Frequency > 0 {
		d.wg.Add(1)
		go d.gcTicker()
	}
	return d
}

func (d *dir) RepoGet(ctx context.Context, repoStr string) (Repo, error) {
	d.mu.Lock()
	locked := true
	defer func() {
		if locked {
			d.mu.Unlock()
		}
	}()
	// if stop ch was closed, fail
	select {
	case <-d.stop:
		return nil, fmt.Errorf("cannot get repo after Close")
	default:
	}
	if dr, err := d.repos.Get(repoStr); err == nil {
		d.mu.Unlock()
		locked = false
		// wgBlock prevents adding to the WG while a wg.Wait is running, GC blocks new requests
		select {
		case <-dr.wgBlock:
			dr.wg.Add(1)
			dr.wgBlock <- struct{}{}
			return dr, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if stringsHasAny(strings.Split(repoStr, "/"), indexFile, layoutFile, blobsDir) {
		return nil, fmt.Errorf("repo %s cannot contain %s, %s, or %s%.0w", repoStr, indexFile, layoutFile, blobsDir, types.ErrRepoNotAllowed)
	}
	uploadCacheOpts := cache.Opts[string, *dirRepoUpload]{
		PruneFn: func(_ string, dru *dirRepoUpload) error {
			dru.delete()
			return nil
		},
	}
	if d.conf.Storage.GC.RepoUploadMax > 0 {
		uploadCacheOpts.Count = d.conf.Storage.GC.RepoUploadMax
	}
	if d.conf.Storage.GC.GracePeriod > 0 {
		uploadCacheOpts.Age = d.conf.Storage.GC.GracePeriod
	}
	dr := dirRepo{
		wgBlock: make(chan struct{}, 1),
		path:    filepath.Join(d.root, repoStr),
		name:    repoStr,
		conf:    d.conf,
		uploads: cache.New[string, *dirRepoUpload](uploadCacheOpts),
		log:     d.log,
	}
	dr.wgBlock <- struct{}{}
	d.repos.Set(repoStr, &dr)
	statDir, err := os.Stat(dr.path)
	if err == nil && statDir.IsDir() {
		statIndex, errIndex := os.Stat(filepath.Join(dr.path, indexFile))
		//#nosec G304 internal method is only called with filenames within admin provided path.
		layoutBytes, errLayout := os.ReadFile(filepath.Join(dr.path, layoutFile))
		if errIndex == nil && errLayout == nil && !statIndex.IsDir() && layoutVerify(layoutBytes) {
			dr.exists = true
		}
	}
	dr.wg.Add(1)
	return &dr, nil
}

func (d *dir) Close() error {
	// signal to background jobs to exit and block new repos from being created
	close(d.stop)
	d.mu.Lock()
	repoNames, err := d.repos.List()
	if err != nil {
		return fmt.Errorf("failed to list repos during close: %w", err)
	}
	errs := []error{}
	for _, r := range repoNames {
		repo, err := d.repos.Get(r)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		repo.wg.Wait()
		if !*d.conf.Storage.ReadOnly {
			err = repo.uploads.DeleteAll()
			if err != nil {
				errs = append(errs, err)
				continue
			}
		}
		err = d.repos.Delete(r)
		if err != nil {
			errs = append(errs, err)
		}
	}
	// wait for background jobs to finish
	d.mu.Unlock()
	d.wg.Wait()
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// gcTicker is a goroutine to continuously run the GC on a schedule
func (d *dir) gcTicker() {
	defer d.wg.Done()
	ticker := time.NewTicker(d.conf.Storage.GC.Frequency)
	prev := time.Time{}
	for {
		select {
		case cur := <-ticker.C:
			_ = d.gc(cur, prev)
			prev = cur
		case <-d.stop:
			ticker.Stop()
			return
		}
	}
}

// run a GC on every repo
func (d *dir) gc(cur, prev time.Time) error {
	start := prev
	if d.conf.Storage.GC.GracePeriod > 0 {
		start = start.Add(d.conf.Storage.GC.GracePeriod * -1)
	}
	repoNames, err := d.repos.List()
	if err != nil {
		return fmt.Errorf("failed to list repos in gc: %w", err)
	}
	for _, r := range repoNames {
		// if stop ch was closed, exit immediately
		select {
		case <-d.stop:
			return fmt.Errorf("stop signal received")
		default:
		}

		repo, err := d.repos.Get(r)
		if err != nil {
			continue
		}
		// skip repos that were have not been recently updated
		repo.mu.Lock()
		outsideRange := repo.timeMod.Before(start)
		repo.mu.Unlock()
		if outsideRange {
			continue
		}
		err = repo.gc()
		if err != nil {
			return err
		}
	}
	return nil
}

// IndexGet returns the current top level index for a repo.
func (dr *dirRepo) IndexGet() (types.Index, error) {
	dr.mu.Lock()
	defer dr.mu.Unlock()
	err := dr.indexLoad(false, true)
	if err != nil && errors.Is(err, types.ErrNotFound) {
		err = nil // ignore not found errors
	}
	ic := dr.index.Copy()
	return ic, err
}

// IndexInsert adds a new entry to the index and writes the change to index.json.
func (dr *dirRepo) IndexInsert(desc types.Descriptor, opts ...types.IndexOpt) error {
	if *dr.conf.Storage.ReadOnly {
		return types.ErrReadOnly
	}
	dr.mu.Lock()
	defer dr.mu.Unlock()
	_ = dr.indexLoad(false, true)
	dr.index.AddDesc(desc, opts...)
	dr.log.Debug("index entry added", "repo", dr.name, "desc", desc)
	return dr.indexSave(true)
}

// IndexRemove removes an entry from the index and writes the change to index.json.
func (dr *dirRepo) IndexRemove(desc types.Descriptor) error {
	if *dr.conf.Storage.ReadOnly {
		return types.ErrReadOnly
	}
	dr.mu.Lock()
	defer dr.mu.Unlock()
	_ = dr.indexLoad(false, true)
	dr.index.RmDesc(desc)
	dr.log.Debug("index entry removed", "repo", dr.name, "desc", desc)
	return dr.indexSave(true)
}

// BlobGet returns a reader to an entry from the CAS.
func (dr *dirRepo) BlobGet(d digest.Digest) (io.ReadSeekCloser, error) {
	return dr.blobGet(d, false)
}

func (dr *dirRepo) blobGet(d digest.Digest, locked bool) (io.ReadSeekCloser, error) {
	if !dr.exists {
		return nil, fmt.Errorf("repo does not exist %s: %w", dr.name, types.ErrNotFound)
	}
	fh, err := os.Open(filepath.Join(dr.path, blobsDir, d.Algorithm().String(), d.Encoded()))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to load digest %s: %w", d.String(), types.ErrNotFound)
		}
		return nil, fmt.Errorf("failed to load digest %s: %w", d.String(), err)
	}
	return fh, nil
}

// blobMeta returns metadata on a blob.
func (dr *dirRepo) blobMeta(d digest.Digest, locked bool) (blobMeta, error) {
	m := blobMeta{}
	if !dr.exists {
		return m, fmt.Errorf("repo does not exist %s: %w", dr.name, types.ErrNotFound)
	}
	fi, err := os.Stat(filepath.Join(dr.path, blobsDir, d.Algorithm().String(), d.Encoded()))
	if err != nil {
		if os.IsNotExist(err) {
			return m, fmt.Errorf("failed to load digest %s: %w", d.String(), types.ErrNotFound)
		}
		return m, fmt.Errorf("failed to load digest %s: %w", d.String(), err)
	}
	m.mod = fi.ModTime()
	return m, nil
}

// BlobCreate is used to create a new blob.
func (dr *dirRepo) BlobCreate(opts ...BlobOpt) (BlobCreator, string, error) {
	if *dr.conf.Storage.ReadOnly {
		return nil, "", types.ErrReadOnly
	}
	conf := blobConfig{
		algo: digest.Canonical,
	}
	for _, opt := range opts {
		opt(&conf)
	}
	if !dr.exists {
		err := dr.repoInit(false)
		if err != nil {
			return nil, "", err
		}
	}
	// if blob exists, return the appropriate error
	if conf.expect != "" {
		_, err := os.Stat(filepath.Join(dr.path, blobsDir, conf.expect.Algorithm().String(), conf.expect.Encoded()))
		if err == nil {
			return nil, "", types.ErrBlobExists
		}
	}
	dr.mu.Lock()
	defer dr.mu.Unlock()
	sessionID, err := genSessionID()
	if err != nil {
		return nil, "", fmt.Errorf("failed generating sessionID: %w", err)
	}
	if _, err := dr.uploads.Get(sessionID); err == nil {
		return nil, "", fmt.Errorf("session ID collision")
	}
	// create a temp file in the repo blob store, under an upload folder
	tmpDir := filepath.Join(dr.path, uploadDir)
	uploadFH, err := os.Stat(tmpDir)
	if err == nil && !uploadFH.IsDir() {
		return nil, "", fmt.Errorf("upload location %s is not a directory", tmpDir)
	}
	if err != nil {
		//#nosec G301 directory permissions are intentionally world readable.
		err = os.MkdirAll(tmpDir, 0755)
		if err != nil {
			return nil, "", fmt.Errorf("failed to create upload directory %s: %w", tmpDir, err)
		}
	}
	tf, err := os.CreateTemp(tmpDir, "upload.*")
	if err != nil {
		return nil, "", fmt.Errorf("failed to create temp file in %s: %w", tmpDir, err)
	}
	filename := tf.Name()
	// start a new digester with the appropriate algo
	d := conf.algo.Digester()
	w := io.MultiWriter(tf, d.Hash())
	bc := &dirRepoUpload{
		fh:        tf,
		w:         w,
		d:         d,
		expect:    conf.expect,
		path:      dr.path,
		filename:  filename,
		dr:        dr,
		sessionID: sessionID,
	}
	dr.timeMod = time.Now()
	dr.uploads.Set(sessionID, bc)
	return bc, sessionID, nil
}

// BlobDelete deletes an entry from the CAS.
func (dr *dirRepo) BlobDelete(d digest.Digest) error {
	return dr.blobDelete(d, false)
}

func (dr *dirRepo) blobDelete(d digest.Digest, locked bool) error {
	if *dr.conf.Storage.ReadOnly {
		return types.ErrReadOnly
	}
	if !dr.exists {
		return fmt.Errorf("repo does not exist %s: %w", dr.name, types.ErrNotFound)
	}
	filename := filepath.Join(dr.path, blobsDir, d.Algorithm().String(), d.Encoded())
	fi, err := os.Stat(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("failed to stat %s: %w", d.String(), types.ErrNotFound)
		}
		return fmt.Errorf("failed to stat %s: %w", d.String(), err)
	}
	if fi.IsDir() {
		return fmt.Errorf("invalid blob %s: %s is a directory", d.String(), filename)
	}
	dr.log.Debug("blob deleted", "repo", dr.name, "digest", d.String())
	err = os.Remove(filename)
	return err
}

func (dr *dirRepo) blobList(locked bool) ([]digest.Digest, error) {
	dl := []digest.Digest{}
	algoS, err := os.ReadDir(filepath.Join(dr.path, blobsDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return dl, nil
		}
		return dl, fmt.Errorf("failed to read dir %s: %v", filepath.Join(dr.path, blobsDir), err)
	}
	for _, algo := range algoS {
		if !algo.IsDir() {
			continue
		}
		encodeS, err := os.ReadDir(filepath.Join(dr.path, blobsDir, algo.Name()))
		if err != nil {
			return dl, fmt.Errorf("failed to read dir %s: %v", filepath.Join(dr.path, blobsDir, algo.Name()), err)
		}
		for _, encode := range encodeS {
			d, err := digest.Parse(algo.Name() + ":" + encode.Name())
			if err != nil {
				// skip unparsable entries
				continue
			}
			dl = append(dl, d)
		}
	}
	return dl, nil
}

// BlobSession is used to retrieve an upload session
func (dr *dirRepo) BlobSession(sessionID string) (BlobCreator, error) {
	dr.mu.Lock()
	defer dr.mu.Unlock()
	if bc, err := dr.uploads.Get(sessionID); err == nil {
		return bc, nil
	}
	return nil, types.ErrNotFound
}

// Done indicates the routine using this repo is finished.
// This must be called exactly once for every instance of [Store.RepoGet].
func (dr *dirRepo) Done() {
	dr.wg.Done()
}

func (dr *dirRepo) repoInit(locked bool) error {
	if !locked {
		dr.mu.Lock()
		defer dr.mu.Unlock()
		locked = true
	}
	if dr.exists {
		return nil
	}
	// create the directory
	fi, err := os.Stat(dr.path)
	if err == nil && !fi.IsDir() {
		return fmt.Errorf("repo %s is not a directory", dr.path)
	}
	if err != nil {
		//#nosec G301 directory permissions are intentionally world readable.
		err = os.MkdirAll(dr.path, 0755)
		if err != nil {
			return fmt.Errorf("failed to create repo directory %s: %w", dr.path, err)
		}
	}
	layoutName := filepath.Join(dr.path, layoutFile)
	//#nosec G304 internal method is only called with filenames within admin provided path.
	layoutBytes, err := os.ReadFile(layoutName)
	if err != nil || !layoutVerify(layoutBytes) {
		l := types.Layout{Version: types.LayoutVersion}
		lJSON, err := json.Marshal(l)
		if err != nil {
			return err
		}
		//#nosec G306 file permissions are intentionally world readable.
		err = os.WriteFile(layoutName, lJSON, 0644)
		if err != nil {
			return err
		}
	}
	// create index if it doesn't exist
	indexName := filepath.Join(dr.path, indexFile)
	fi, err = os.Stat(indexName)
	if err == nil && fi.IsDir() {
		return fmt.Errorf("index.json is a directory: %s", indexName)
	}
	if err != nil && errors.Is(err, fs.ErrNotExist) {
		err = dr.indexSave(locked)
	}
	if err != nil {
		return err
	}
	dr.exists = true
	return nil
}

func (dr *dirRepo) indexLoad(force, locked bool) error {
	if !locked {
		dr.mu.Lock()
		defer dr.mu.Unlock()
		locked = true
	}
	if !force && time.Since(dr.timeCheck) < freqCheck {
		return nil
	}
	if dr.index.MediaType == "" && len(dr.index.Manifests) == 0 {
		// default values for the index if the load fails (does not exist or unparsable)
		dr.index = types.Index{
			SchemaVersion: 2,
			MediaType:     types.MediaTypeOCI1ManifestList,
			Manifests:     []types.Descriptor{},
			Annotations:   map[string]string{},
		}
		if *dr.conf.API.Referrer.Enabled {
			dr.index.Annotations[types.AnnotReferrerConvert] = "true"
		}
	}
	fh, err := os.Open(filepath.Join(dr.path, indexFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%v%.0w", err, types.ErrNotFound)
		}
		return err
	}
	defer fh.Close()
	stat, err := fh.Stat()
	if err != nil {
		return err
	}
	dr.timeCheck = time.Now()
	if dr.timeMod == stat.ModTime() {
		// file is unchanged from previous loaded version
		return nil
	}
	parseIndex := types.Index{}
	err = json.NewDecoder(fh).Decode(&parseIndex)
	if err != nil {
		return err
	}
	dr.index = parseIndex
	dr.timeMod = stat.ModTime()
	dr.exists = true

	mod, err := indexIngest(dr, &dr.index, dr.conf, locked)
	if err != nil {
		return err
	}
	if mod && !*dr.conf.Storage.ReadOnly {
		err = dr.indexSave(locked)
		if err != nil {
			return err
		}
	}
	return nil
}

func (dr *dirRepo) indexSave(locked bool) error {
	if *dr.conf.Storage.ReadOnly {
		return types.ErrReadOnly
	}
	if !locked {
		dr.mu.Lock()
		defer dr.mu.Unlock()
	}
	// force minimal settings on the index
	dr.index.SchemaVersion = 2
	dr.index.MediaType = types.MediaTypeOCI1ManifestList
	fh, err := os.CreateTemp(dr.path, "index.json.*")
	if err != nil {
		return err
	}
	defer fh.Close()
	err = json.NewEncoder(fh).Encode(dr.index)
	if err != nil {
		_ = os.Remove(fh.Name())
		return err
	}
	err = os.Rename(fh.Name(), filepath.Join(dr.path, indexFile))
	if err != nil {
		_ = os.Remove(fh.Name())
		return err
	}
	fi, err := fh.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat index.json for tracking mod time: %w", err)
	}
	dr.timeMod = fi.ModTime()
	return nil
}

// gc runs the garbage collect
func (dr *dirRepo) gc() error {
	<-dr.wgBlock
	defer func() { dr.wgBlock <- struct{}{} }()
	dr.wg.Wait()
	dr.mu.Lock()
	defer dr.mu.Unlock()
	dr.log.Debug("starting GC", "repo", dr.name)
	// attempt to remove an empty upload folder, ignore errors (e.g. uploads managed by another tool)
	if dr.uploads.IsEmpty() {
		fi, err := os.Stat(filepath.Join(dr.path, uploadDir))
		if err == nil && fi.IsDir() {
			_ = os.Remove(filepath.Join(dr.path, uploadDir))
		}
	}
	// run the GC process, track any errors but wait until later to return it
	errGC := func() error {
		err := dr.indexLoad(true, true)
		if err != nil {
			return fmt.Errorf("failed to load index: %w", err)
		}
		i, mod, err := repoGarbageCollect(dr, dr.conf, dr.index, true)
		if err != nil {
			return err
		}
		if mod {
			dr.index = i
			if err := dr.indexSave(true); err != nil {
				return err
			}
		}
		return nil
	}()
	// prune an empty repo dir and mark the repo as empty if successful
	if *dr.conf.Storage.GC.EmptyRepo && len(dr.index.Manifests) == 0 && dr.uploads.IsEmpty() {
		// TODO: switch to a slice of errors instead of a function with a return
		errDir := func() error {
			for _, dir := range []string{
				filepath.Join(dr.path, uploadDir),
				filepath.Join(dr.path, blobsDir, "sha256"),
				filepath.Join(dr.path, blobsDir, "sha512"),
				filepath.Join(dr.path, blobsDir),
				filepath.Join(dr.path, indexFile),
				filepath.Join(dr.path, layoutFile),
				filepath.Join(dr.path),
			} {
				err := os.Remove(dir)
				if err != nil && !errors.Is(err, fs.ErrNotExist) {
					return err
				}
			}
			return nil
		}()
		if errDir == nil {
			dr.exists = false
		}
	}
	dr.log.Debug("finished GC", "repo", dr.name, "err", errGC)
	if errGC != nil {
		return errGC
	}
	return nil
}

// Write is used to push content into the blob.
func (dru *dirRepoUpload) Write(p []byte) (int, error) {
	if dru.w == nil {
		return 0, fmt.Errorf("writer is closed")
	}
	// verify session still exists and update last write time
	if _, err := dru.dr.uploads.Get(dru.sessionID); err != nil {
		return 0, fmt.Errorf("session expired %s: %w", dru.sessionID, err)
	}
	n, err := dru.w.Write(p)
	dru.size += int64(n)
	return n, err
}

// Close finishes an upload, verifying digest if requested, and moves it into the blob store.
func (dru *dirRepoUpload) Close() error {
	err := dru.fh.Close()
	if err != nil {
		_ = os.Remove(dru.filename)
		return err // TODO: join multiple errors after 1.19 support is removed
	}
	if dru.expect != "" && dru.d.Digest() != dru.expect {
		_ = os.Remove(dru.filename)
		return fmt.Errorf("digest mismatch, expected %s, received %s", dru.expect, dru.d.Digest())
	}
	// move temp file to blob store
	tgtDir := filepath.Join(dru.path, blobsDir, dru.d.Digest().Algorithm().String())
	fi, err := os.Stat(tgtDir)
	if err == nil && !fi.IsDir() {
		_ = os.Remove(dru.filename)
		return fmt.Errorf("failed to move file to blob storage, %s is not a directory", tgtDir)
	}
	if err != nil {
		//#nosec G301 directory permissions are intentionally world readable.
		err = os.MkdirAll(tgtDir, 0755)
		if err != nil {
			_ = os.Remove(dru.filename)
			return fmt.Errorf("unable to create blob storage directory %s: %w", tgtDir, err)
		}
	}
	blobName := filepath.Join(tgtDir, dru.d.Digest().Encoded())
	err = os.Rename(dru.filename, blobName)
	_ = dru.dr.uploads.Delete(dru.sessionID)
	dru.dr.log.Debug("blob created", "repo", dru.dr.name, "digest", dru.d.Digest().String(), "err", err)
	return err
}

// Cancel is used to stop an upload.
func (dru *dirRepoUpload) Cancel() {
	_ = dru.dr.uploads.Delete(dru.sessionID)
}

func (dru *dirRepoUpload) delete() {
	dru.w = nil
	if dru.fh != nil {
		_ = dru.fh.Close()
		dru.fh = nil
	}
	_ = os.Remove(dru.filename)
	now := time.Now()
	// goroutine to avoid deadlock, the dr.mu lock may already be held depending on how prune is called
	go func() {
		dru.dr.mu.Lock()
		if dru.dr.timeMod.Before(now) {
			dru.dr.timeMod = now
		}
		dru.dr.mu.Unlock()
	}()
}

// Size reports the number of bytes pushed.
func (dru *dirRepoUpload) Size() int64 {
	return dru.size
}

// Digest is used to get the current digest of the content.
func (dru *dirRepoUpload) Digest() digest.Digest {
	return dru.d.Digest()
}

// Verify ensures a digest matches the content.
func (dru *dirRepoUpload) Verify(expect digest.Digest) error {
	if dru.d.Digest() != expect {
		return fmt.Errorf("digest mismatch, expected %s, received %s", expect, dru.d.Digest())
	}
	return nil
}

func stringsHasAny(list []string, check ...string) bool {
	for _, l := range list {
		for _, c := range check {
			if l == c {
				return true
			}
		}
	}
	return false
}
