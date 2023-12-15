package store

import (
	"bytes"
	"fmt"
	"io"
	"sync"

	"github.com/opencontainers/go-digest"

	"github.com/olareg/olareg/types"
)

type mem struct {
	mu    sync.Mutex
	repos map[string]*memRepo
}

type memRepo struct {
	mu    sync.Mutex
	index types.Index
	blobs map[digest.Digest][]byte
}

type memRepoUpload struct {
	buffer *bytes.Buffer
	w      io.Writer
	d      digest.Digester
	expect digest.Digest
	mr     *memRepo
}

func NewMem() Store {
	return &mem{
		repos: map[string]*memRepo{},
	}
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
	}
	m.repos[repoStr] = mr
	return mr, nil
}

// IndexGet returns the current top level index for a repo.
func (mr *memRepo) IndexGet() (types.Index, error) {
	return mr.index, nil
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
