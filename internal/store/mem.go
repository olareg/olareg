// Copyright the olareg contributors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package store

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"sync"
	"time"

	digest "github.com/sudo-bmitch/oci-digest"

	"github.com/olareg/olareg/config"
	"github.com/olareg/olareg/types"
)

// mem is the in memory representation of an OCI Layout.
// When holding multiple mutex locks simultaneously, always start from fine grain lock first (memRepoUpload, then memRepo, then mem) to avoid deadlocks.

type mem struct {
	mu    sync.Mutex
	repos map[string]*memRepo
	blobs map[digest.Digest]*memBlob
	log   *slog.Logger
	conf  config.ConfigStorage
	next  Store
}

type memRepo struct {
	mu    sync.Mutex
	m     *mem
	name  string
	index types.LayoutIndex
	blobs map[digest.Digest]*memRepoBlob
	next  Repo
}

type memBlob struct {
	b        []byte
	refCount int
}

type memRepoBlob struct {
	b   []byte
	mod time.Time
}

type memRepoUpload struct {
	mu     sync.Mutex
	buffer *bytes.Buffer
	mr     *memRepo
}

// newMem creates a new memory backed store.
func newMem(conf config.ConfigStorage, opts ...Opts) (Store, error) {
	op := OptParams{
		log: slog.New(slog.DiscardHandler),
	}
	for _, opt := range opts {
		opt(&op)
	}
	m := &mem{
		repos: map[string]*memRepo{},
		blobs: map[digest.Digest]*memBlob{},
		log:   op.log,
		conf:  conf,
	}
	if conf.RootDir != "" {
		// configure directory backend as a read-only fallthrough storage
		dirConf := conf
		bTrue := true
		dirConf.ReadOnly = &bTrue
		dir, err := newDir(dirConf, opts...)
		if err != nil {
			return nil, err
		}
		m.next = dir
	}
	return m, nil
}

// repoGet returns an existing repo or initializes a new one.
func (m *mem) RepoGet(repo string) (Repo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mr, ok := m.repos[repo]; ok {
		return mr, nil
	}
	// create an empty repo
	mr := &memRepo{
		m:    m,
		name: repo,
		index: types.LayoutIndex{
			Index: types.Index{
				SchemaVersion: 2,
				MediaType:     types.MediaTypeOCI1ManifestList,
				Manifests:     []types.Descriptor{},
				Annotations:   map[string]string{},
			},
		},
		blobs: map[digest.Digest]*memRepoBlob{},
	}
	if m.next != nil {
		if nextRepo, err := m.next.RepoGet(repo); err == nil {
			mr.next = nextRepo
			// initialize the index
			idx, err := nextRepo.IndexGet()
			if err != nil {
				return nil, fmt.Errorf("failed to initialize index: %v", err)
			}
			mr.index = idx
		}
	}
	m.repos[repo] = mr
	return mr, nil
}

// IsEphemeral indicates whether or not the repos continue to exist after closing.
func (m *mem) IsEphemeral() bool {
	return true
}

// Close is used to free up backend resources.
// Further calls to Backend methods may fail after this is run.
func (m *mem) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blobs = map[digest.Digest]*memBlob{}
	m.repos = map[string]*memRepo{}
	return nil
}

// IndexGet returns the top level index.json file contents.
func (mr *memRepo) IndexGet() (types.LayoutIndex, error) {
	mr.mu.Lock()
	defer mr.mu.Unlock()
	return mr.index.Copy(), nil
}

// IndexSet returns the top level index.json file contents.
func (mr *memRepo) IndexSet(i types.LayoutIndex) error {
	if *mr.m.conf.ReadOnly {
		return types.ErrReadOnly
	}
	mr.mu.Lock()
	defer mr.mu.Unlock()
	mr.index = i.Copy()
	mr.m.log.Debug("memRepo index updated", "repo", mr.name, "entries", len(i.Manifests))
	return nil
}

// BlobCreate is used to create a new blob.
func (mr *memRepo) BlobCreate() (BlobCreator, error) {
	if *mr.m.conf.ReadOnly {
		return nil, types.ErrReadOnly
	}
	mr.mu.Lock()
	defer mr.mu.Unlock()
	buffer := &bytes.Buffer{}
	bc := &memRepoUpload{
		buffer: buffer,
		mr:     mr,
	}
	return bc, nil
}

// BlobDelete removes an entry from the CAS.
func (mr *memRepo) BlobDelete(d digest.Digest) error {
	if *mr.m.conf.ReadOnly {
		return types.ErrReadOnly
	}
	if d.IsZero() {
		return fmt.Errorf("invalid digest: %s", d.String())
	}
	mr.mu.Lock()
	defer mr.mu.Unlock()
	if b, ok := mr.blobs[d]; ok {
		if b != nil {
			if mr.next != nil {
				mr.blobs[d] = nil
			} else {
				delete(mr.blobs, d)
			}
			mr.m.mu.Lock()
			if mr.m.blobs[d] != nil && mr.m.blobs[d].refCount > 1 {
				mr.m.blobs[d].refCount--
			} else {
				delete(mr.m.blobs, d)
			}
			mr.m.mu.Unlock()
		} else {
			return types.ErrNotFound
		}
	} else if mr.next != nil {
		b, err := mr.next.BlobGet(d)
		if errors.Is(err, types.ErrNotFound) {
			return types.ErrNotFound
		}
		_ = b.Close()
		// blob exists in fallback, only delete it from the memory store with a flag on the repo
		mr.blobs[d] = nil
	} else {
		return types.ErrNotFound
	}
	mr.m.log.Debug("blob deleted", "repo", mr.name, "digest", d.String())
	return nil
}

// BlobGet returns a reader to an entry from the CAS.
func (mr *memRepo) BlobGet(d digest.Digest) (io.ReadSeekCloser, error) {
	if d.IsZero() {
		return nil, fmt.Errorf("invalid digest: %s", d.String())
	}
	mr.mu.Lock()
	defer mr.mu.Unlock()
	if b, ok := mr.blobs[d]; ok {
		if b != nil && ok {
			return types.BytesReadCloser{Reader: bytes.NewReader(b.b)}, nil
		} else {
			// blob explicitly deleted, do not fallback
			return nil, types.ErrNotFound
		}
	} else if mr.next != nil {
		// fallback to underlying storage
		return mr.next.BlobGet(d)
	} else {
		return nil, types.ErrNotFound
	}
}

// BlobMeta returns metadata on a blob.
func (mr *memRepo) BlobMeta(d digest.Digest) (BlobMeta, error) {
	if d.IsZero() {
		return BlobMeta{}, fmt.Errorf("invalid digest: %s", d.String())
	}
	mr.mu.Lock()
	defer mr.mu.Unlock()
	if b, ok := mr.blobs[d]; ok {
		if b != nil && ok {
			return BlobMeta{Mod: b.mod, Size: int64(len(b.b))}, nil
		} else {
			// blob explicitly deleted, do not fallback
			return BlobMeta{}, types.ErrNotFound
		}
	} else if mr.next != nil {
		// fallback to underlying storage
		return mr.next.BlobMeta(d)
	} else {
		return BlobMeta{}, types.ErrNotFound
	}
}

// BlobPut directly adds a blob without an intermediate `BlobCreator`
func (mr *memRepo) BlobPut(d digest.Digest, b []byte) error {
	if *mr.m.conf.ReadOnly {
		return types.ErrReadOnly
	}
	if d.IsZero() {
		return types.ErrDigestInvalid
	}
	if b == nil {
		return fmt.Errorf("bytes cannot be nil%.0w", types.ErrNotFound)
	}
	mrb := &memRepoBlob{
		b:   b,
		mod: time.Now(),
	}
	mr.mu.Lock()
	defer mr.mu.Unlock()
	m := mr.m
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.blobs[d]; ok {
		if !bytes.Equal(existing.b, mrb.b) {
			return fmt.Errorf("digest collision encountered pushing to %s: %s", mr.name, d.String())
		}
		mrb.b = existing.b
		m.blobs[d].refCount++
	} else {
		m.blobs[d] = &memBlob{
			b:        mrb.b,
			refCount: 1,
		}
	}
	mr.blobs[d] = mrb
	m.log.Debug("blob created", "repo", mr.name, "digest", d.String())
	return nil
}

// Walk is used to traverse the contents of a repo.
// Descriptors that are not found will be silently skipped.
func (mr *memRepo) Walk(depth types.ManifestParseDepth, retReader bool, descList ...types.Descriptor) iter.Seq[WalkStep] {
	return func(yield func(WalkStep) bool) {
		for i := 0; i < len(descList); i++ { // descList may be appended in this loop
			d := descList[i]
			step := WalkStep{Desc: d}
			var raw []byte
			mr.mu.Lock()
			mrb, ok := mr.blobs[d.Digest]
			mr.mu.Unlock()
			if ok && mrb != nil {
				step.Meta = BlobMeta{Mod: mrb.mod, Size: int64(len(mrb.b))}
				if retReader {
					step.Rdr = types.BytesReadCloser{Reader: bytes.NewReader(mrb.b)}
				}
				if types.MediaTypeManifest(d.MediaType) {
					raw = mrb.b
				}
			} else if !ok && mr.next != nil {
				// fall through to next store
				meta, err := mr.next.BlobMeta(d.Digest)
				if err != nil {
					mr.m.log.Debug("walk failed to get blob metadata", "repo", mr.name, "digest", d.Digest, "err", err)
					continue
				}
				step.Meta = meta
				if types.MediaTypeManifest(d.MediaType) {
					rdr, err := mr.next.BlobGet(d.Digest)
					if err != nil {
						mr.m.log.Debug("walk failed to get blob", "repo", mr.name, "digest", d.Digest, "err", err)
						continue
					}
					raw, err = io.ReadAll(rdr)
					_ = rdr.Close()
					if err != nil {
						continue
					}
					if retReader {
						step.Rdr = types.BytesReadCloser{Reader: bytes.NewReader(raw)}
					}
				} else if retReader {
					rdr, err := mr.next.BlobGet(d.Digest)
					if err != nil {
						mr.m.log.Debug("walk failed to get blob", "repo", mr.name, "digest", d.Digest, "err", err)
						continue
					}
					step.Rdr = rdr
				}
			} else {
				// skip deleted or missing entries
				mr.m.log.Debug("walk failed to get blob, deleted or missing", "repo", mr.name, "digest", d.Digest, "deleted", ok)
				continue
			}
			if !yield(step) {
				return
			}
			if raw != nil {
				addDesc, _ := types.ManifestParseDescriptors(raw, d, depth)
				if len(addDesc) > 0 {
					descList = append(descList, addDesc...)
				}
			}
		}
	}
}

// GC (garbage collect) cleans unmarked blobs that have been created before the cutoff time.
func (mr *memRepo) GC(cutoff time.Time, keepDig map[digest.Digest]bool) error {
	if *mr.m.conf.ReadOnly {
		return types.ErrReadOnly
	}
	// clean old unmarked blobs from the repo
	rmDigList := map[digest.Digest]bool{}
	mr.mu.Lock()
	for cur, mrb := range mr.blobs {
		if !keepDig[cur] && mrb != nil && mrb.mod.Before(cutoff) {
			rmDigList[cur] = true
			mr.m.log.Debug("gc pruned blob", "repo", mr.name, "digest", cur.String())
			delete(mr.blobs, cur)
		}
	}
	mr.mu.Unlock()
	if len(rmDigList) == 0 {
		return nil
	}
	// clean from shared blob store
	m := mr.m
	m.mu.Lock()
	for cur := range rmDigList {
		if mb, ok := m.blobs[cur]; ok && mb.refCount > 1 {
			mb.refCount--
		} else {
			mr.m.log.Debug("gc pruned blob from shared store", "digest", cur.String())
			delete(m.blobs, cur)
		}
	}
	m.mu.Unlock()
	return nil
}

// Close indicates the repo is no longer being accessed and resources may be freed.
func (mr *memRepo) Close() error {
	if mr.next != nil {
		if err := mr.next.Close(); err != nil {
			return err
		}
	}
	return nil
}

// Cancel is used to stop an upload.
func (mru *memRepoUpload) Cancel() error {
	mru.mu.Lock()
	defer mru.mu.Unlock()
	mru.buffer = nil
	return nil
}

// Reader returns a new reader from the uploaded buffer.
func (mru *memRepoUpload) Reader() io.Reader {
	mru.mu.Lock()
	defer mru.mu.Unlock()
	if mru.buffer == nil {
		return nil
	}
	return bytes.NewBuffer(mru.buffer.Bytes())
}

// Save is used to save an upload to a CAS digest.
func (mru *memRepoUpload) Save(d digest.Digest) error {
	if d.IsZero() {
		return types.ErrDigestInvalid
	}
	mru.mu.Lock()
	defer mru.mu.Unlock()
	if mru.buffer == nil {
		return fmt.Errorf("upload was canceled or not initialized%.0w", types.ErrNotFound)
	}
	mrb := &memRepoBlob{
		b:   mru.buffer.Bytes(),
		mod: time.Now(),
	}
	mru.buffer = nil
	mr := mru.mr
	mr.mu.Lock()
	defer mr.mu.Unlock()
	m := mr.m
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.blobs[d]; ok {
		if !bytes.Equal(existing.b, mrb.b) {
			return fmt.Errorf("digest collision encountered pushing to %s: %s", mr.name, d.String())
		}
		mrb.b = existing.b
		m.blobs[d].refCount++
	} else {
		m.blobs[d] = &memBlob{
			b:        mrb.b,
			refCount: 1,
		}
	}
	mr.blobs[d] = mrb
	m.log.Debug("blob created", "repo", mr.name, "digest", d.String())
	return nil
}

// Write sends data to the buffer.
func (mru *memRepoUpload) Write(p []byte) (int, error) {
	mru.mu.Lock()
	defer mru.mu.Unlock()
	if mru.buffer == nil {
		return 0, fmt.Errorf("upload was canceled or not initialized%.0w", types.ErrNotFound)
	}
	return mru.buffer.Write(p)
}
