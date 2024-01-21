package store

import (
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
	"github.com/olareg/olareg/internal/slog"
	"github.com/olareg/olareg/types"
)

type dir struct {
	mu    sync.Mutex
	root  string
	repos map[string]*dirRepo // TODO: switch to storing these in a cache that expires from memory
	log   slog.Logger
	conf  config.Config
	wg    sync.WaitGroup
	stop  chan struct{}
}

type dirRepo struct {
	mu        sync.Mutex
	timeCheck time.Time
	timeMod   time.Time
	name      string
	path      string
	exists    bool
	index     types.Index
	log       slog.Logger
	conf      config.Config
}

type dirRepoUpload struct {
	fh       *os.File
	w        io.Writer
	size     int64
	d        digest.Digester
	expect   digest.Digest
	path     string
	filename string
}

// NewDir returns a directory store.
func NewDir(conf config.Config, opts ...Opts) Store {
	sc := storeConf{}
	for _, opt := range opts {
		opt(&sc)
	}
	d := &dir{
		root:  conf.Storage.RootDir,
		repos: map[string]*dirRepo{},
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

// TODO: include options for memory caching.

func (d *dir) RepoGet(repoStr string) (Repo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if dr, ok := d.repos[repoStr]; ok {
		return dr, nil
	}
	if stringsHasAny(strings.Split(repoStr, "/"), indexFile, layoutFile, blobsDir) {
		return nil, fmt.Errorf("repo %s cannot contain %s, %s, or %s%.0w", repoStr, indexFile, layoutFile, blobsDir, types.ErrRepoNotAllowed)
	}
	dr := dirRepo{
		path: filepath.Join(d.root, repoStr),
		name: repoStr,
		log:  d.log,
		conf: d.conf,
	}
	d.repos[repoStr] = &dr
	statDir, err := os.Stat(dr.path)
	if err == nil && statDir.IsDir() {
		statIndex, errIndex := os.Stat(filepath.Join(dr.path, indexFile))
		//#nosec G304 internal method is only called with filenames within admin provided path.
		layoutBytes, errLayout := os.ReadFile(filepath.Join(dr.path, layoutFile))
		if errIndex == nil && errLayout == nil && !statIndex.IsDir() && layoutVerify(layoutBytes) {
			dr.exists = true
		}
	}
	return &dr, nil
}

func (d *dir) Close() error {
	// signal to background jobs to exit
	close(d.stop)
	d.mu.Lock()
	if !*d.conf.Storage.ReadOnly {
		// perform a final GC of each repo
		for _, dr := range d.repos {
			_ = dr.gc()
		}
	}
	// the store is unusable after close, reset the repos to quickly stop a running GC
	d.repos = map[string]*dirRepo{}
	// wait for background jobs to finish
	d.mu.Unlock()
	d.wg.Wait()
	return nil
}

// gcTicker is a goroutine to continuously run the GC on a schedule
func (d *dir) gcTicker() {
	defer d.wg.Done()
	ticker := time.NewTicker(d.conf.Storage.GC.Frequency)
	for {
		select {
		case <-ticker.C:
			_ = d.gc()
		case <-d.stop:
			ticker.Stop()
			return
		}
	}
}

// run a GC on every repo
func (d *dir) gc() error {
	now := time.Now()
	stop := now
	if d.conf.Storage.GC.GracePeriod > 0 {
		stop = stop.Add(d.conf.Storage.GC.GracePeriod * -1)
	}
	start := stop
	if d.conf.Storage.GC.Frequency > 0 {
		start = start.Add(d.conf.Storage.GC.Frequency * -1)
	}
	repoNames := make([]string, 0, len(d.repos))
	d.mu.Lock()
	defer d.mu.Unlock()
	// since the lock isn't held for the entire GC, build a list of repos to check
	for r := range d.repos {
		repoNames = append(repoNames, r)
	}
	for _, r := range repoNames {
		repo, ok := d.repos[r]
		if !ok {
			continue
		}
		if repo.timeMod.Before(start) || repo.timeMod.After(stop) {
			continue
		}
		// drop top level lock while GCing a single repo
		d.mu.Unlock()
		err := repo.gc()
		d.mu.Lock()
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
func (dr *dirRepo) BlobCreate(opts ...BlobOpt) (BlobCreator, error) {
	if *dr.conf.Storage.ReadOnly {
		return nil, types.ErrReadOnly
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
			return nil, err
		}
	}
	// if blob exists, return the appropriate error
	if conf.expect != "" {
		_, err := os.Stat(filepath.Join(dr.path, blobsDir, conf.expect.Algorithm().String(), conf.expect.Encoded()))
		if err == nil {
			return nil, types.ErrBlobExists
		}
	}
	// create a temp file in the repo blob store, under an upload folder
	tmpDir := filepath.Join(dr.path, uploadDir)
	uploadFH, err := os.Stat(tmpDir)
	if err == nil && !uploadFH.IsDir() {
		return nil, fmt.Errorf("upload location %s is not a directory", tmpDir)
	}
	if err != nil {
		//#nosec G301 directory permissions are intentionally world readable.
		err = os.MkdirAll(tmpDir, 0755)
		if err != nil {
			return nil, fmt.Errorf("failed to create upload directory %s: %w", tmpDir, err)
		}
	}
	tf, err := os.CreateTemp(tmpDir, "upload.*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file in %s: %w", tmpDir, err)
	}
	filename := tf.Name()
	// start a new digester with the appropriate algo
	d := conf.algo.Digester()
	w := io.MultiWriter(tf, d.Hash())
	return &dirRepoUpload{
		fh:       tf,
		w:        w,
		d:        d,
		expect:   conf.expect,
		path:     dr.path,
		filename: filename,
	}, nil
}

// BlobDelete deletes an entry from the CAS.
func (dr *dirRepo) BlobDelete(d digest.Digest) error {
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
	err = os.Remove(filename)
	return err
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
	dr.mu.Lock()
	defer dr.mu.Unlock()
	locked := true
	var cutoff time.Time
	if dr.conf.Storage.GC.GracePeriod >= 0 {
		cutoff = time.Now().Add(dr.conf.Storage.GC.GracePeriod * -1)
	}
	manifests := make([]types.Descriptor, 0, len(dr.index.Manifests))
	subjects := map[digest.Digest]types.Descriptor{}
	inIndex := map[digest.Digest]bool{}
	// build a list of manifests and subjects to scan
	for _, d := range dr.index.Manifests {
		inIndex[d.Digest] = true
		keep := false
		// keep tagged entries or every entry if untagged entries are not GCed
		if !*dr.conf.Storage.GC.Untagged || (d.Annotations != nil && d.Annotations[types.AnnotRefName] != "") {
			keep = true
		}
		// keep new blobs
		if !keep && dr.conf.Storage.GC.GracePeriod >= 0 {
			if meta, err := dr.blobMeta(d.Digest, locked); err == nil && meta.mod.After(cutoff) {
				keep = true
			}
		}
		// referrers responses
		if d.Annotations != nil && d.Annotations[types.AnnotReferrerSubject] != "" {
			dig, _ := digest.Parse(d.Annotations[types.AnnotReferrerSubject])
			subjExists := (dig != "")
			if _, err := dr.blobMeta(dig, locked); subjExists && err != nil {
				subjExists = false
			}
			if *dr.conf.Storage.GC.ReferrersWithSubj && subjExists {
				// track a map of responses only preserved when their subject remains
				subjects[dig] = d.Copy()
				keep = false
			} else if !*dr.conf.Storage.GC.ReferrersDangling {
				// keep if dangling aren't GCed
				keep = true
			} else if subjExists {
				// subject exists but need to delete dangling
				if meta, err := dr.blobMeta(d.Digest, locked); err == nil && dr.conf.Storage.GC.GracePeriod >= 0 && meta.mod.After(cutoff) {
					// always keep new entries
					keep = true
				} else {
					// else preserve only if subject remains
					subjects[dig] = d.Copy()
					keep = false
				}
			}
		}
		if keep {
			manifests = append(manifests, d.Copy())
		}
	}
	seen := map[digest.Digest]bool{}
	// walk all manifests to note seen digests
	for len(manifests) > 0 {
		// work from tail to make deletes easier
		d := manifests[len(manifests)-1]
		manifests = manifests[:len(manifests)-1]
		if seen[d.Digest] {
			continue
		}
		br, err := dr.blobGet(d.Digest, locked)
		if err != nil {
			continue
		}
		seen[d.Digest] = true
		// parse manifests for descriptors (manifests, config, layers)
		if types.MediaTypeIndex(d.MediaType) {
			man := types.Index{}
			err = json.NewDecoder(br).Decode(&man)
			errClose := br.Close()
			if err != nil || errClose != nil {
				continue
			}
			for _, child := range man.Manifests {
				manifests = append(manifests, child.Copy())
			}
		} else if types.MediaTypeImage(d.MediaType) {
			man := types.Manifest{}
			err = json.NewDecoder(br).Decode(&man)
			errClose := br.Close()
			if err != nil || errClose != nil {
				continue
			}
			seen[man.Config.Digest] = true
			for _, layer := range man.Layers {
				seen[layer.Digest] = true
			}
		}
		// if there are referrers to this manifest
		if referrer, ok := subjects[d.Digest]; ok {
			manifests = append(manifests, referrer)
		}
	}
	// clean old blobs that were not seen
	algoS, err := os.ReadDir(filepath.Join(dr.path, blobsDir))
	if err != nil {
		return fmt.Errorf("failed to read dir %s: %v", filepath.Join(dr.path, blobsDir), err)
	}
	for _, algo := range algoS {
		if !algo.IsDir() {
			continue
		}
		encodeS, err := os.ReadDir(filepath.Join(dr.path, blobsDir, algo.Name()))
		if err != nil {
			return fmt.Errorf("failed to read dir %s: %v", filepath.Join(dr.path, blobsDir, algo.Name()), err)
		}
		for _, encode := range encodeS {
			d, err := digest.Parse(algo.Name() + ":" + encode.Name())
			if err != nil {
				// skip unparsable entries
				continue
			}
			if seen[d] {
				continue
			}
			filename := filepath.Join(dr.path, blobsDir, algo.Name(), encode.Name())
			fi, err := os.Stat(filename)
			if err != nil || fi.IsDir() {
				continue
			}
			if dr.conf.Storage.GC.GracePeriod >= 0 && fi.ModTime().After(cutoff) && !inIndex[d] {
				// keep recently uploaded blobs (manifests handled above)
				continue
			}
			// prune from index
			if inIndex[d] {
				dr.index.RmDesc(types.Descriptor{Digest: d})
			}
			// attempt to prune from blob store, ignoring errors
			_ = os.Remove(filename)
		}
	}
	return nil
}

// Write is used to push content into the blob.
func (dru *dirRepoUpload) Write(p []byte) (int, error) {
	if dru.w == nil {
		return 0, fmt.Errorf("writer is closed")
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
	if err != nil {
		_ = os.Remove(dru.filename)
		return err
	}
	return nil
}

// Cancel is used to stop an upload.
func (dru *dirRepoUpload) Cancel() {
	if dru.fh != nil {
		_ = dru.fh.Close()
	}
	_ = os.Remove(dru.filename)
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
