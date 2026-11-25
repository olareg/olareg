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

// Package store is a minimal interface on top of an OCI Layout.
package store

import (
	"io"
	"iter"
	"log/slog"
	"time"

	digest "github.com/sudo-bmitch/oci-digest"

	"github.com/olareg/olareg/config"
	"github.com/olareg/olareg/types"
)

const (
	indexFile  = "index.json"
	layoutFile = "oci-layout"
	blobsDir   = "blobs"
	uploadDir  = "_uploads"
)

var backends = map[string]func(conf config.ConfigStorage, opts ...Opts) (Store, error){}

func New(conf config.ConfigStorage, opts ...Opts) (Store, error) {
	name := conf.StoreType
	fn, ok := backends[name]
	if !ok {
		return nil, types.ErrNotFound
	}
	return fn(conf, opts...)
}

func Register(name string, newFn func(conf config.ConfigStorage, opts ...Opts) (Store, error)) {
	backends[name] = newFn
}

func RegisterDefaults() {
	backends[config.StoreMem] = newMem
	backends[config.StoreDir] = newDir
}

// Opts includes options for initializing the included stores.
type Opts func(*OptParams)

type OptParams struct {
	log *slog.Logger
}

// WithLog sets the logger for the included stores.
func WithLog(log *slog.Logger) Opts {
	return func(op *OptParams) {
		op.log = log
	}
}

// Store interface is used to abstract access to a backend storage system for repositories.
type Store interface {
	// RepoGet returns a repo.
	// The returned interface should remain valid until [Repo.Close] is called.
	RepoGet(repo string) (Repo, error)

	// IsEphemeral indicates whether or not the repos continue to exist after closing.
	IsEphemeral() bool

	// Close indicates the store is no longer needed and may free up any resources.
	// Future calls to the store or any contained repos may fail.
	Close() error
}

// Repo interface is used to abstract access to each OCI Layout directory.
type Repo interface {
	// IndexGet returns the top level index.json file contents.
	IndexGet() (types.LayoutIndex, error)

	// IndexSet returns the top level index.json file contents.
	IndexSet(types.LayoutIndex) error

	// BlobCreate is used to create a new blob.
	BlobCreate() (BlobCreator, error)

	// BlobDelete removes an entry from the CAS.
	BlobDelete(d digest.Digest) error

	// BlobGet returns a reader to an entry from the CAS.
	BlobGet(d digest.Digest) (io.ReadSeekCloser, error)

	// BlobMeta returns metadata on a blob.
	BlobMeta(d digest.Digest) (BlobMeta, error)

	// BlobPut directly adds a blob without an intermediate `BlobCreator`
	BlobPut(d digest.Digest, b []byte) error

	// Walk is used to traverse the contents of a repo.
	// Descriptors that are not found will be silently skipped.
	Walk(depth types.ManifestParseDepth, retReader bool, descList ...types.Descriptor) iter.Seq[WalkStep]

	// GC (garbage collect) cleans unmarked blobs that have been created before the cutoff time.
	GC(cutoff time.Time, keepDig map[digest.Digest]bool) error

	// Close indicates the repo is no longer being accessed and resources may be freed.
	Close() error
}

// BlobMeta contains metadata on a blob.
type BlobMeta struct {
	Mod  time.Time
	Size int64
}

// BlobCreator is used to upload new blobs.
type BlobCreator interface {
	// Writer is used to push the blob content.
	io.Writer
	// Reader returns a reader from the start of the blob.
	// The reader should always be fully consumed before calling Write again.
	Reader() io.Reader
	// Save is used to store the blob to a given digest value.
	// The store is not required to verify this value.
	Save(digest.Digest) error
	// Cancel is used to stop an upload.
	Cancel() error
}

// WalkStep is returned from each iteration of a [Backend.Walk]
type WalkStep struct {
	Desc types.Descriptor
	Rdr  io.ReadSeekCloser
	Meta BlobMeta
}
