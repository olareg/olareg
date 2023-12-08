package store

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	// imports required for go-digest
	_ "crypto/sha256"
	_ "crypto/sha512"

	"github.com/opencontainers/go-digest"

	"github.com/olareg/olareg/internal/slog"
	"github.com/olareg/olareg/types"
)

const (
	freqCheck  = time.Second
	indexFile  = "index.json"
	layoutFile = "oci-layout"
)

type dir struct {
	mu    sync.Mutex
	root  string
	repos map[string]*dirRepo // TODO: switch to storing these in a cache that expires from memory
	log   slog.Logger
}

type dirRepo struct {
	timeCheck time.Time
	timeMod   time.Time
	mu        sync.Mutex
	name      string
	path      string
	exists    bool
	index     types.Index
	log       slog.Logger
}

// OptDir includes options for the directory store.
type OptDir func(*dir)

// NewDir returns a directory store.
func NewDir(root string, opts ...OptDir) Store {
	d := &dir{
		root:  root,
		repos: map[string]*dirRepo{},
	}
	for _, opt := range opts {
		opt(d)
	}
	if d.log == nil {
		d.log = slog.Null{}
	}
	return d
}

// WithDirLog includes a logger on the directory store.
func WithDirLog(log slog.Logger) OptDir {
	return func(d *dir) {
		d.log = log
	}
}

// TODO: include options for memory caching, allowed methods.

func (d *dir) RepoGet(repoStr string) Repo {
	d.mu.Lock()
	defer d.mu.Unlock()
	if dr, ok := d.repos[repoStr]; ok {
		return dr
	}
	dr := dirRepo{
		path: filepath.Join(d.root, repoStr),
		name: repoStr,
		log:  d.log,
	}
	d.repos[repoStr] = &dr
	statDir, err := os.Stat(dr.path)
	if err == nil && statDir.IsDir() {
		statIndex, errIndex := os.Stat(filepath.Join(dr.path, indexFile))
		statLayout, errLayout := os.Stat(filepath.Join(dr.path, layoutFile))
		// TODO: validate content of layout
		if errIndex == nil && errLayout == nil && !statIndex.IsDir() && !statLayout.IsDir() {
			dr.exists = true
		}
	}
	return &dr
}

// IndexGet returns the current top level index for a repo.
func (dr *dirRepo) IndexGet() (types.Index, error) {
	err := dr.repoLoad(false)
	return dr.index, err
}

// BlobGet returns a reader to an entry from the CAS.
func (dr *dirRepo) BlobGet(d digest.Digest) (io.ReadSeekCloser, error) {
	if !dr.exists {
		return nil, fmt.Errorf("repo does not exist %s: %w", dr.name, types.ErrNotFound)
	}
	fh, err := os.Open(filepath.Join(dr.path, "blobs", d.Algorithm().String(), d.Encoded()))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to load digest %s: %w", d.String(), types.ErrNotFound)
		}
		return nil, fmt.Errorf("failed to load digest %s: %w", d.String(), err)
	}
	return fh, nil
}

func (dr *dirRepo) repoLoad(force bool) error {
	dr.mu.Lock()
	defer dr.mu.Unlock()
	if !force && time.Since(dr.timeCheck) < freqCheck {
		return nil
	}
	fh, err := os.Open(filepath.Join(dr.path, indexFile))
	if err != nil {
		return err
	}
	defer fh.Close()
	stat, err := fh.Stat()
	if err != nil {
		return err
	}
	dr.timeCheck = time.Now()
	if dr.timeMod == stat.ModTime() {
		return nil
	}
	err = json.NewDecoder(fh).Decode(&dr.index)
	if err != nil {
		return err
	}
	dr.exists = true
	for _, d := range dr.index.Manifests {
		if types.MediaTypeIndex(d.MediaType) {
			err = dr.repoLoadIndex(d)
			if err != nil {
				return err // TODO: after dropping 1.19 support, join multiple errors into one return
			}
		}
	}
	dr.timeMod = stat.ModTime()
	return nil
}

func (dr *dirRepo) repoLoadIndex(d types.Descriptor) error {
	rdr, err := dr.BlobGet(d.Digest)
	if err != nil {
		return err
	}
	i := types.Index{}
	err = json.NewDecoder(rdr).Decode(&i)
	_ = rdr.Close() // close here rather than defer, to avoid open fh during recursion
	if err != nil {
		return err
	}
	dr.index.AddChildren(i.Manifests)
	for _, di := range i.Manifests {
		if types.MediaTypeIndex(di.MediaType) {
			err = dr.repoLoadIndex(di)
			if err != nil {
				return err // TODO: after dropping 1.19 support, join multiple errors into one return
			}
		}
	}
	return nil
}
