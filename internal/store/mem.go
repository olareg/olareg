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
}

type memRepo struct {
	mu    sync.Mutex
	index types.Index
	blobs map[digest.Digest][]byte
	log   slog.Logger
	path  string
	conf  config.Config
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
	}
	if m.log == nil {
		m.log = slog.Null{}
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
		blobs: map[digest.Digest][]byte{},
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
	mr.mu.Lock()
	mr.index.AddDesc(desc, opts...)
	mr.mu.Unlock()
	return nil
}

// IndexRemove removes an entry from the index and writes the change to index.json.
func (mr *memRepo) IndexRemove(desc types.Descriptor) error {
	mr.mu.Lock()
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
		return types.BytesReadCloser{Reader: bytes.NewReader(b)}, nil
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

// BlobCreate is used to create a new blob.
func (mr *memRepo) BlobCreate(opts ...BlobOpt) (BlobCreator, error) {
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
	mru.mr.blobs[mru.d.Digest()] = mru.buffer.Bytes()
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
