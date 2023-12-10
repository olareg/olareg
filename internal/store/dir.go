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

type dirRepoUpload struct {
	fh       *os.File
	w        io.Writer
	size     int64
	d        digest.Digester
	expect   digest.Digest
	path     string
	filename string
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
		if errIndex == nil && !statIndex.IsDir() && verifyLayout(filepath.Join(dr.path, layoutFile)) {
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

// BlobCreate is used to create a new blob.
func (dr *dirRepo) BlobCreate(opts ...BlobOpt) (BlobCreator, error) {
	conf := blobConfig{
		algo: digest.Canonical,
	}
	for _, opt := range opts {
		opt(&conf)
	}
	if !dr.exists {
		err := dr.repoInit()
		if err != nil {
			return nil, err
		}
	}
	// create a temp file in the repo blob store, under an upload folder
	uploadDir := filepath.Join(dr.path, "uploads")
	uploadFH, err := os.Stat(uploadDir)
	if err == nil && !uploadFH.IsDir() {
		return nil, fmt.Errorf("upload location %s is not a directory", uploadDir)
	}
	if err != nil {
		//#nosec G301 directory permissions are intentionally world readable.
		err = os.MkdirAll(uploadDir, 0755)
		if err != nil {
			return nil, fmt.Errorf("failed to create upload directory %s: %w", uploadDir, err)
		}
	}
	tf, err := os.CreateTemp(uploadDir, "upload.*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file in %s: %w", uploadDir, err)
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

func (dr *dirRepo) repoInit() error {
	dr.mu.Lock()
	defer dr.mu.Unlock()
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
	indexName := filepath.Join(dr.path, indexFile)
	fi, err = os.Stat(indexName)
	if err == nil && fi.IsDir() {
		return fmt.Errorf("index.json is a directory: %s", indexName)
	}
	// create an index if it doesn't exist, but don't overwrite data
	if err != nil {
		i := types.Index{
			SchemaVersion: 2,
			Manifests:     []types.Descriptor{},
		}
		iJSON, err := json.Marshal(i)
		if err != nil {
			return err
		}
		//#nosec G306 file permissions are intentionally world readable.
		err = os.WriteFile(indexName, iJSON, 0644)
		if err != nil {
			return err
		}
	}
	layoutName := filepath.Join(dr.path, layoutFile)
	if !verifyLayout(layoutName) {
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
	dr.exists = true
	return nil
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
		return err // TODO: join multiple errors after 1.19 support is removed
	}
	if dru.expect != "" && dru.d.Digest() != dru.expect {
		return fmt.Errorf("digest mismatch, expected %s, received %s", dru.expect, dru.d.Digest())
	}
	// move temp file to blob store
	blobDir := filepath.Join(dru.path, "blobs", dru.d.Digest().Algorithm().String())
	fi, err := os.Stat(blobDir)
	if err == nil && !fi.IsDir() {
		return fmt.Errorf("failed to move file to blob storage, %s is not a directory", blobDir)
	}
	if err != nil {
		//#nosec G301 directory permissions are intentionally world readable.
		err = os.MkdirAll(blobDir, 0755)
		if err != nil {
			return fmt.Errorf("unable to create blob storage directory %s: %w", blobDir, err)
		}
	}
	blobName := filepath.Join(blobDir, dru.d.Digest().Encoded())
	err = os.Rename(dru.filename, blobName)
	return err
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

// TempFilename returns the assigned temp filename.
func (dru *dirRepoUpload) TempFilename() string {
	return dru.filename
}

func verifyLayout(filename string) bool {
	//#nosec G304 internal method is only called with filenames within admin provided path.
	b, err := os.ReadFile(filename)
	if err != nil {
		return false
	}
	l := types.Layout{}
	err = json.Unmarshal(b, &l)
	if err != nil {
		return false
	}
	if l.Version != types.LayoutVersion {
		return false
	}
	return true
}
