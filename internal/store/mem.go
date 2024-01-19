package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/opencontainers/go-digest"

	"github.com/olareg/olareg/config"
	"github.com/olareg/olareg/internal/slog"
	"github.com/olareg/olareg/types"
)

type mem struct {
	mu    sync.Mutex
	repos map[string]*memRepo
	log   slog.Logger
	conf  config.Config
	wg    sync.WaitGroup
	stop  chan struct{}
}

type memRepo struct {
	mu    sync.Mutex
	index types.Index
	blobs map[digest.Digest]*memRepoBlob
	log   slog.Logger
	path  string
	conf  config.Config
	mod   time.Time
}

type memRepoBlob struct {
	b []byte
	m blobMeta
}

type memRepoUpload struct {
	buffer *bytes.Buffer
	w      io.Writer
	d      digest.Digester
	expect digest.Digest
	mr     *memRepo
}

func NewMem(conf config.Config, opts ...Opts) Store {
	sc := storeConf{}
	for _, opt := range opts {
		opt(&sc)
	}
	m := &mem{
		repos: map[string]*memRepo{},
		log:   sc.log,
		conf:  conf,
		stop:  make(chan struct{}),
	}
	if m.log == nil {
		m.log = slog.Null{}
	}
	if m.conf.Storage.GC.Frequency > 0 {
		m.wg.Add(1)
		go m.gcTicker()
	}
	return m
}

func (m *mem) RepoGet(repoStr string) (Repo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mr, ok := m.repos[repoStr]; ok {
		return mr, nil
	}
	mr := &memRepo{
		index: types.Index{
			SchemaVersion: 2,
			MediaType:     types.MediaTypeOCI1ManifestList,
			Manifests:     []types.Descriptor{},
			Annotations:   map[string]string{},
		},
		blobs: map[digest.Digest]*memRepoBlob{},
		log:   m.log,
		conf:  m.conf,
	}
	if *m.conf.API.Referrer.Enabled {
		mr.index.Annotations[types.AnnotReferrerConvert] = "true"
	}
	if m.conf.Storage.RootDir != "" {
		mr.path = filepath.Join(m.conf.Storage.RootDir, repoStr)
		err := mr.repoGetIndex()
		if err != nil {
			return nil, err
		}
	}
	m.repos[repoStr] = mr
	return mr, nil
}

func (m *mem) Close() error {
	// signal to background jobs to exit
	close(m.stop)
	// mem is unusable after close, reset the repos to free memory and stop a running GC
	m.mu.Lock()
	m.repos = map[string]*memRepo{}
	m.mu.Unlock()
	// wait for background jobs to finish
	m.wg.Wait()
	return nil
}

// gcTicker is a goroutine to continuously run the GC on a schedule
func (m *mem) gcTicker() {
	defer m.wg.Done()
	ticker := time.NewTicker(m.conf.Storage.GC.Frequency)
	for {
		select {
		case <-ticker.C:
			_ = m.gc()
		case <-m.stop:
			ticker.Stop()
			return
		}
	}
}

// run a GC on every repo
func (m *mem) gc() error {
	now := time.Now()
	stop := now
	if m.conf.Storage.GC.GracePeriod > 0 {
		stop = stop.Add(m.conf.Storage.GC.GracePeriod * -1)
	}
	start := stop
	if m.conf.Storage.GC.Frequency > 0 {
		start = start.Add(m.conf.Storage.GC.Frequency * -1)
	}
	repoNames := make([]string, 0, len(m.repos))
	m.mu.Lock()
	defer m.mu.Unlock()
	// since the lock isn't held for the entire GC, build a list of repos to check
	for r := range m.repos {
		repoNames = append(repoNames, r)
	}
	for _, r := range repoNames {
		repo, ok := m.repos[r]
		if !ok {
			continue
		}
		if repo.mod.Before(start) || repo.mod.After(stop) {
			continue
		}
		// drop top level lock while GCing a single repo
		m.mu.Unlock()
		err := repo.gc()
		m.mu.Lock()
		if err != nil {
			return err
		}
	}
	return nil
}

func (mr *memRepo) repoGetIndex() error {
	// initialize index from backend dir if available
	// validate directory is an OCI Layout
	statIndex, errIndex := os.Stat(filepath.Join(mr.path, indexFile))
	//#nosec G304 internal method is only called with filenames within admin provided path.
	layoutBytes, errLayout := os.ReadFile(filepath.Join(mr.path, layoutFile))
	if errIndex != nil || errLayout != nil || statIndex.IsDir() || !layoutVerify(layoutBytes) {
		return nil
	}
	// read the index.json
	fh, err := os.Open(filepath.Join(mr.path, indexFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer fh.Close()
	parseIndex := types.Index{}
	err = json.NewDecoder(fh).Decode(&parseIndex)
	if err != nil {
		return err
	}
	mr.index = parseIndex
	// ingest to load child descriptors and configure referrers
	_, err = indexIngest(mr, &mr.index, mr.conf, true)
	if err != nil {
		return err
	}
	return nil
}

// IndexGet returns the current top level index for a repo.
func (mr *memRepo) IndexGet() (types.Index, error) {
	mr.mu.Lock()
	defer mr.mu.Unlock()
	ic := mr.index.Copy()
	return ic, nil
}

// IndexInsert adds a new entry to the index and writes the change to index.json.
func (mr *memRepo) IndexInsert(desc types.Descriptor, opts ...types.IndexOpt) error {
	if *mr.conf.Storage.ReadOnly {
		return types.ErrReadOnly
	}
	mr.mu.Lock()
	mr.mod = time.Now()
	mr.index.AddDesc(desc, opts...)
	mr.mu.Unlock()
	return nil
}

// IndexRemove removes an entry from the index and writes the change to index.json.
func (mr *memRepo) IndexRemove(desc types.Descriptor) error {
	if *mr.conf.Storage.ReadOnly {
		return types.ErrReadOnly
	}
	mr.mu.Lock()
	mr.mod = time.Now()
	mr.index.RmDesc(desc)
	mr.mu.Unlock()
	return nil
}

// BlobGet returns a reader to an entry from the CAS.
func (mr *memRepo) BlobGet(d digest.Digest) (io.ReadSeekCloser, error) {
	return mr.blobGet(d, false)
}

func (mr *memRepo) blobGet(d digest.Digest, locked bool) (io.ReadSeekCloser, error) {
	if !locked {
		mr.mu.Lock()
		defer mr.mu.Unlock()
	}
	b, ok := mr.blobs[d]
	if ok {
		// when there is a directory backing, nil indicates an explicit delete or blob doesn't exist
		if b == nil {
			return nil, fmt.Errorf("failed to load digest %s: %w", d.String(), types.ErrNotFound)
		}
		return types.BytesReadCloser{Reader: bytes.NewReader(b.b)}, nil
	}
	// else try to load from backing dir
	if mr.path != "" {
		fh, err := os.Open(filepath.Join(mr.path, blobsDir, d.Algorithm().String(), d.Encoded()))
		if err != nil {
			if os.IsNotExist(err) {
				mr.blobs[d] = nil // explicitly mark as not found to skip future attempts
				return nil, fmt.Errorf("failed to load digest %s: %w", d.String(), types.ErrNotFound)
			}
			return nil, fmt.Errorf("failed to load digest %s: %w", d.String(), err)
		}
		return fh, nil
	}
	return nil, fmt.Errorf("failed to load digest %s: %w", d.String(), types.ErrNotFound)
}

// blobMeta returns metadata on a blob.
func (mr *memRepo) blobMeta(d digest.Digest, locked bool) (blobMeta, error) {
	if !locked {
		mr.mu.Lock()
		defer mr.mu.Unlock()
	}
	m := blobMeta{}
	b, ok := mr.blobs[d]
	if ok {
		// when there is a directory backing, nil indicates an explicit delete or blob doesn't exist
		if b == nil {
			return m, fmt.Errorf("failed to load digest %s: %w", d.String(), types.ErrNotFound)
		}
		return b.m, nil
	}
	// else try to load from backing dir
	if mr.path != "" {
		fi, err := os.Stat(filepath.Join(mr.path, blobsDir, d.Algorithm().String(), d.Encoded()))
		if err != nil {
			if os.IsNotExist(err) {
				mr.blobs[d] = nil // explicitly mark as not found to skip future attempts
				return m, fmt.Errorf("failed to load digest %s: %w", d.String(), types.ErrNotFound)
			}
			return m, fmt.Errorf("failed to load digest %s: %w", d.String(), err)
		}
		m.mod = fi.ModTime()
		return m, nil
	}
	return m, fmt.Errorf("failed to load digest %s: %w", d.String(), types.ErrNotFound)
}

// BlobCreate is used to create a new blob.
func (mr *memRepo) BlobCreate(opts ...BlobOpt) (BlobCreator, error) {
	if *mr.conf.Storage.ReadOnly {
		return nil, types.ErrReadOnly
	}
	conf := blobConfig{
		algo: digest.Canonical,
	}
	for _, opt := range opts {
		opt(&conf)
	}
	// if blob exists, return the appropriate error
	if conf.expect != "" {
		mr.mu.Lock()
		b, ok := mr.blobs[conf.expect]
		if b == nil {
			ok = false
		}
		mr.mu.Unlock()
		if ok {
			return nil, types.ErrBlobExists
		}
	}
	buffer := &bytes.Buffer{}
	d := conf.algo.Digester()
	w := io.MultiWriter(buffer, d.Hash())
	return &memRepoUpload{
		buffer: buffer,
		w:      w,
		d:      d,
		expect: conf.expect,
		mr:     mr,
	}, nil
}

// BlobDelete deletes an entry from the CAS.
func (mr *memRepo) BlobDelete(d digest.Digest) error {
	if *mr.conf.Storage.ReadOnly {
		return types.ErrReadOnly
	}
	mr.mu.Lock()
	defer mr.mu.Unlock()
	_, ok := mr.blobs[d]
	if ok {
		if mr.path != "" {
			mr.blobs[d] = nil
		} else {
			delete(mr.blobs, d)
		}
	}
	if !ok {
		if mr.path == "" {
			return types.ErrNotFound
		}
		if mr.path != "" {
			_, err := os.Stat(filepath.Join(mr.path, blobsDir, d.Algorithm().String(), d.Encoded()))
			if err != nil && errors.Is(err, fs.ErrNotExist) {
				return types.ErrNotFound
			}
			mr.blobs[d] = nil
		}
	}
	mr.mod = time.Now()
	return nil
}

// gc runs the garbage collect
func (mr *memRepo) gc() error {
	mr.mu.Lock()
	defer mr.mu.Unlock()
	var cutoff time.Time
	if mr.conf.Storage.GC.GracePeriod >= 0 {
		cutoff = time.Now().Add(mr.conf.Storage.GC.GracePeriod * -1)
	}
	manifests := make([]types.Descriptor, 0, len(mr.index.Manifests))
	subjects := map[digest.Digest]types.Descriptor{}
	inIndex := map[digest.Digest]bool{}
	// build a list of manifests and subjects to scan
	for _, d := range mr.index.Manifests {
		inIndex[d.Digest] = true
		keep := false
		// keep tagged entries or every entry if untagged entries are not GCed
		if !*mr.conf.Storage.GC.Untagged || (d.Annotations != nil && d.Annotations[types.AnnotRefName] != "") {
			keep = true
		}
		// keep new blobs
		if !keep && mr.conf.Storage.GC.GracePeriod >= 0 && mr.blobs[d.Digest].m.mod.After(cutoff) {
			keep = true
		}
		// referrers responses
		if d.Annotations != nil && d.Annotations[types.AnnotReferrerSubject] != "" {
			dig, _ := digest.Parse(d.Annotations[types.AnnotReferrerSubject])
			subjExists := false
			if _, ok := mr.blobs[dig]; dig != "" && ok {
				subjExists = true
			}
			if *mr.conf.Storage.GC.ReferrersWithSubj && subjExists {
				// track a map of responses only preserved when their subject remains
				subjects[dig] = d.Copy()
				keep = false
			} else if !*mr.conf.Storage.GC.ReferrersDangling {
				// keep if dangling aren't GCed
				keep = true
			} else if subjExists {
				// subject exists but need to delete dangling
				if mr.conf.Storage.GC.GracePeriod >= 0 && mr.blobs[d.Digest].m.mod.After(cutoff) {
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
		if _, ok := mr.blobs[d.Digest]; !ok || seen[d.Digest] {
			// skip missing or already seen blobs
			continue
		}
		seen[d.Digest] = true
		// parse manifests for descriptors (manifests, config, layers)
		if types.MediaTypeIndex(d.MediaType) {
			man := types.Index{}
			err := json.Unmarshal(mr.blobs[d.Digest].b, &man)
			if err != nil {
				continue
			}
			for _, child := range man.Manifests {
				manifests = append(manifests, child.Copy())
			}
		} else if types.MediaTypeImage(d.MediaType) {
			man := types.Manifest{}
			err := json.Unmarshal(mr.blobs[d.Digest].b, &man)
			if err != nil {
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
	for d := range mr.blobs {
		if seen[d] {
			continue
		}
		if mr.conf.Storage.GC.GracePeriod >= 0 && mr.blobs[d].m.mod.After(cutoff) && !inIndex[d] {
			// keep recently uploaded blobs (manifests handled above)
			continue
		}
		// prune from index
		if inIndex[d] {
			mr.index.RmDesc(types.Descriptor{Digest: d})
		}
		// prune from blob store
		delete(mr.blobs, d)
	}
	return nil
}

// Write sends data to the buffer.
func (mru *memRepoUpload) Write(p []byte) (int, error) {
	return mru.w.Write(p)
}

func (mru *memRepoUpload) Close() error {
	if mru.expect != "" && mru.d.Digest() != mru.expect {
		return fmt.Errorf("digest mismatch, expected %s, received %s", mru.expect, mru.d.Digest())
	}
	// relocate []byte to in memory blob store
	mru.mr.mu.Lock()
	mru.mr.mod = time.Now()
	mru.mr.blobs[mru.d.Digest()] = &memRepoBlob{
		b: mru.buffer.Bytes(),
		m: blobMeta{
			mod: time.Now(),
		},
	}

	mru.mr.mu.Unlock()
	return nil
}

// Cancel is used to stop an upload.
func (mru *memRepoUpload) Cancel() {
	mru.buffer.Truncate(0)
}

// Size reports the number of bytes pushed.
func (mru *memRepoUpload) Size() int64 {
	return int64(mru.buffer.Len())
}

// Digest is used to get the current digest of the content.
func (mru *memRepoUpload) Digest() digest.Digest {
	return mru.d.Digest()
}

// Verify ensures a digest matches the content.
func (mru *memRepoUpload) Verify(expect digest.Digest) error {
	if mru.d.Digest() != expect {
		return fmt.Errorf("digest mismatch, expected %s, received %s", expect, mru.d.Digest())
	}
	return nil
}
