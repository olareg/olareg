package store

import (
	"bytes"
	"fmt"
	"io"
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
			Manifests: []types.Descriptor{},
		},
		blobs: map[digest.Digest][]byte{},
		log:   m.log,
		conf:  m.conf,
	}
	m.repos[repoStr] = mr
	return mr, nil
}

// IndexGet returns the current top level index for a repo.
func (mr *memRepo) IndexGet() (types.Index, error) {
	mr.mu.Lock()
	defer mr.mu.Unlock()
	ic := mr.index.Copy()
	return ic, nil
}

// IndexAnnotate sets an annotation on the index.
// Setting the value to an empty string deletes the key from the annotations.
func (mr *memRepo) IndexAnnotate(key, value string) error {
	mr.mu.Lock()
	defer mr.mu.Unlock()
	if value == "" {
		if mr.index.Annotations == nil {
			return nil
		} else {
			delete(mr.index.Annotations, key)
		}
	} else {
		if mr.index.Annotations == nil {
			mr.index.Annotations = map[string]string{key: value}
		} else {
			mr.index.Annotations[key] = value
		}
	}
	return nil
}

// IndexAdd adds a new entry to the index and writes the change to index.json.
func (mr *memRepo) IndexAdd(desc types.Descriptor, opts ...types.IndexOpt) error {
	mr.mu.Lock()
	mr.index.AddDesc(desc, opts...)
	mr.mu.Unlock()
	return nil
}

// IndexRm removes an entry from the index and writes the change to index.json.
func (mr *memRepo) IndexRm(desc types.Descriptor) error {
	mr.mu.Lock()
	mr.index.RmDesc(desc)
	mr.mu.Unlock()
	return nil
}

// BlobGet returns a reader to an entry from the CAS.
func (mr *memRepo) BlobGet(d digest.Digest) (io.ReadSeekCloser, error) {
	mr.mu.Lock()
	b, ok := mr.blobs[d]
	mr.mu.Unlock()
	if ok {
		return types.BytesReadCloser{Reader: bytes.NewReader(b)}, nil
	}
	return nil, types.ErrNotFound
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
		_, ok := mr.blobs[conf.expect]
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
	_, ok := mr.blobs[d]
	delete(mr.blobs, d)
	mr.mu.Unlock()
	if !ok {
		return types.ErrNotFound
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
