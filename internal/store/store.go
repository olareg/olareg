// Package store is used to interface with different types of storage (memory, disk)
package store

import (
	"io"

	// imports required for go-digest
	_ "crypto/sha256"
	_ "crypto/sha512"

	"github.com/opencontainers/go-digest"

	"github.com/olareg/olareg/types"
)

type Store interface {
	RepoGet(repoStr string) (Repo, error)
}

type Repo interface {
	// IndexGet returns the current top level index for a repo.
	IndexGet() (types.Index, error)
	// IndexAdd adds a new entry to the index and writes the change to index.json.
	IndexAdd(desc types.Descriptor, opts ...types.IndexOpt) error
	// IndexRm removes an entry from the index and writes the change to index.json.
	IndexRm(desc types.Descriptor) error

	// BlobGet returns a reader to an entry from the CAS.
	BlobGet(d digest.Digest) (io.ReadSeekCloser, error)
	// BlobCreate is used to create a new blob.
	BlobCreate(opts ...BlobOpt) (BlobCreator, error)
}

type BlobOpt func(*blobConfig)

type blobConfig struct {
	algo   digest.Algorithm
	expect digest.Digest
}

func BlobWithAlgorithm(a digest.Algorithm) BlobOpt {
	return func(bc *blobConfig) {
		bc.algo = a
	}
}

func BlobWithDigest(d digest.Digest) BlobOpt {
	return func(bc *blobConfig) {
		bc.expect = d
		bc.algo = d.Algorithm()
	}
}

// BlobCreator is used to upload new blobs.
type BlobCreator interface {
	// WriteCloser is used to push the blob content.
	io.WriteCloser
	// Cancel is used to stop an upload.
	Cancel()
	// Size reports the number of bytes pushed.
	Size() int64
	// Digest is used to get the current digest of the content.
	Digest() digest.Digest
	// Verify ensures a digest matches the content.
	Verify(digest.Digest) error
}
