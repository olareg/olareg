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
	RepoGet(repoStr string) Repo
}

type Repo interface {
	// IndexGet returns the current top level index for a repo.
	IndexGet() (types.Index, error)

	// TODO:
	// IndexAdd(desc types.Descriptor) error // call index.Add, this wraps in a lock to ensure atomic change
	// IndexRm(desc types.Descriptor) error // reverse of add

	// BlobGet returns a reader to an entry from the CAS.
	BlobGet(d digest.Digest) (io.ReadSeekCloser, error)

	// TODO: some kind of BlobPut or BlobCreate interface
	// the return should be an interface that includes a WriteCloser, along with digest generate/verify methods.
}
