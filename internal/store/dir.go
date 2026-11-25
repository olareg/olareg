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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"iter"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	digest "github.com/sudo-bmitch/oci-digest"

	"github.com/olareg/olareg/config"
	"github.com/olareg/olareg/types"
)

// dir is the on disk representation of an OCI Layout.
// When holding multiple mutex locks simultaneously, always start from fine grain lock first (dirRepoUpload, then dirRepo, then dir) to avoid deadlocks.

type dir struct {
	root string
	log  *slog.Logger
	conf config.ConfigStorage
}

type dirRepo struct {
	mu         sync.Mutex
	d          *dir
	name       string
	path       string
	indexEmpty bool
	validOL    bool // valid oci-layout file
	log        *slog.Logger
}

type dirRepoUpload struct {
	mu       sync.Mutex
	fh       *os.File
	repoPath string
	tempName string
	dr       *dirRepo
}

// newDir creates a new directory backed store.
func newDir(conf config.ConfigStorage, opts ...Opts) (Store, error) {
	op := OptParams{
		log: slog.New(slog.DiscardHandler),
	}
	for _, opt := range opts {
		opt(&op)
	}
	d := &dir{
		root: conf.RootDir,
		log:  op.log,
		conf: conf,
	}
	return d, nil
}

// RepoGet returns a repo.
// The returned interface should remain valid until [Repo.Close] is called.
func (d *dir) RepoGet(repo string) (Repo, error) {
	// minimal validation since this is done in backend
	if !filepath.IsLocal(repo) {
		return nil, fmt.Errorf("repo %s must be a relative path%.0w", repo, types.ErrRepoNotAllowed)
	}
	dr := dirRepo{
		path: filepath.Join(d.root, repo),
		name: repo,
		d:    d,
		log:  d.log,
	}
	// fast fail on an invalid oci-layout file
	if err := dr.layoutVerify(); err != nil && !errors.Is(err, types.ErrNotFound) {
		return nil, err
	}
	return &dr, nil
}

// IsEphemeral indicates whether or not the repos continue to exist after closing.
func (d *dir) IsEphemeral() bool {
	return false
}

// Close indicates the store is no longer needed and may free up any resources.
// Future calls to the store or any contained repos may fail.
func (d *dir) Close() error {
	return nil
}

// IndexGet returns the top level index.json file contents.
func (dr *dirRepo) IndexGet() (types.LayoutIndex, error) {
	// verify the layout file
	dr.mu.Lock()
	err := dr.layoutVerify()
	dr.mu.Unlock()
	if err != nil && !errors.Is(err, types.ErrNotFound) {
		return types.LayoutIndex{}, err
	}
	// load index.json
	li := types.LayoutIndex{
		Index: types.Index{
			SchemaVersion: 2,
			MediaType:     types.MediaTypeOCI1ManifestList,
			Manifests:     []types.Descriptor{},
		},
	}
	fh, err := os.Open(filepath.Join(dr.path, indexFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return li, nil
		}
		return li, err
	}
	defer fh.Close()
	err = json.NewDecoder(fh).Decode(&li)
	if li.SchemaVersion != 2 || li.MediaType != types.MediaTypeOCI1ManifestList {
		return li, fmt.Errorf("unsupported schema version or media type in index.json")
	}
	dr.indexEmpty = len(li.Manifests) == 0
	return li, err
}

// IndexSet returns the top level index.json file contents.
func (dr *dirRepo) IndexSet(li types.LayoutIndex) error {
	if *dr.d.conf.ReadOnly {
		return types.ErrReadOnly
	}
	// write the oci-layout file if missing
	dr.mu.Lock()
	err := dr.layoutWrite()
	dr.mu.Unlock()
	if err != nil {
		return err
	}
	// write the index first to a temp file to avoid collisions
	fh, err := os.CreateTemp(dr.path, "index.json.*")
	if err != nil {
		dr.log.Debug("dirRepo index temp file create filed", "repo", dr.name, "err", err)
		return err
	}
	defer fh.Close()
	err = json.NewEncoder(fh).Encode(li)
	if err != nil {
		//#nosec G703 repo path has been cleaned
		_ = os.Remove(fh.Name())
		return err
	}
	//#nosec G703 repo path has been cleaned
	err = os.Rename(fh.Name(), filepath.Join(dr.path, indexFile))
	if err != nil {
		dr.log.Debug("dirRepo index rename failed", "repo", dr.name, "err", err)
		//#nosec G703 repo path has been cleaned
		_ = os.Remove(fh.Name())
		return err
	}
	dr.indexEmpty = len(li.Manifests) == 0
	dr.log.Debug("dirRepo index updated", "repo", dr.name, "entries", len(li.Manifests))
	return nil
}

// BlobCreate is used to create a new blob.
func (dr *dirRepo) BlobCreate() (BlobCreator, error) {
	if *dr.d.conf.ReadOnly {
		return nil, types.ErrReadOnly
	}
	// create a temp file in the repo blob store, under an upload folder
	tmpDir := filepath.Join(dr.path, uploadDir)
	uploadFH, err := os.Stat(tmpDir)
	if err == nil && !uploadFH.IsDir() {
		return nil, fmt.Errorf("upload location %s is not a directory", tmpDir)
	}
	if err != nil {
		//#nosec G301 directory permissions are intentionally world readable.
		err = os.MkdirAll(tmpDir, 0o755)
		if err != nil {
			return nil, fmt.Errorf("failed to create upload directory %s: %w", tmpDir, err)
		}
	}
	tf, err := os.CreateTemp(tmpDir, "upload.*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file in %s: %w", tmpDir, err)
	}
	bc := &dirRepoUpload{
		fh:       tf,
		repoPath: dr.path,
		tempName: tf.Name(),
		dr:       dr,
	}
	return bc, nil
}

// BlobDelete removes an entry from the CAS.
func (dr *dirRepo) BlobDelete(d digest.Digest) error {
	if *dr.d.conf.ReadOnly {
		return types.ErrReadOnly
	}
	if d.IsZero() {
		return fmt.Errorf("invalid digest: %s", d.String())
	}
	filename := filepath.Join(dr.path, blobsDir, d.Algorithm().String(), d.Encoded())
	fi, err := os.Stat(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("failed to stat %s: %w", d.String(), types.ErrNotFound)
		}
		return fmt.Errorf("failed to stat %s: %w", d.String(), err)
	}
	if fi.IsDir() {
		return fmt.Errorf("invalid blob %s: %s is a directory", d.String(), filename)
	}
	err = os.Remove(filename)
	if err != nil {
		dr.log.Debug("blob delete failed", "repo", dr.name, "digest", d.String(), "err", err)
	} else {
		dr.log.Debug("blob deleted", "repo", dr.name, "digest", d.String())
	}
	return err
}

// BlobGet returns a reader to an entry from the CAS.
func (dr *dirRepo) BlobGet(d digest.Digest) (io.ReadSeekCloser, error) {
	if d.IsZero() {
		return nil, fmt.Errorf("invalid digest: %s", d.String())
	}
	fh, err := os.Open(filepath.Join(dr.path, blobsDir, d.Algorithm().String(), d.Encoded()))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to load digest %s: %w", d.String(), types.ErrNotFound)
		}
		return nil, fmt.Errorf("failed to load digest %s: %w", d.String(), err)
	}
	return fh, nil
}

// BlobMeta returns metadata on a blob.
func (dr *dirRepo) BlobMeta(d digest.Digest) (BlobMeta, error) {
	if d.IsZero() {
		return BlobMeta{}, fmt.Errorf("invalid digest: %s", d.String())
	}
	fi, err := os.Stat(filepath.Join(dr.path, blobsDir, d.Algorithm().String(), d.Encoded()))
	if err != nil {
		if os.IsNotExist(err) {
			return BlobMeta{}, fmt.Errorf("failed to load digest %s: %w", d.String(), types.ErrNotFound)
		}
		return BlobMeta{}, fmt.Errorf("failed to load digest %s: %w", d.String(), err)
	}
	m := BlobMeta{}
	m.Mod = fi.ModTime()
	m.Size = fi.Size()
	return m, nil
}

// BlobPut directly adds a blob without an intermediate `BlobCreator`
func (dr *dirRepo) BlobPut(d digest.Digest, b []byte) error {
	if *dr.d.conf.ReadOnly {
		return types.ErrReadOnly
	}
	if d.IsZero() {
		return types.ErrDigestInvalid
	}
	if b == nil {
		return fmt.Errorf("bytes cannot be nil%.0w", types.ErrNotFound)
	}
	// create the blob algorithm directory, if missing
	tgtDir := filepath.Join(dr.path, blobsDir, d.Algorithm().String())
	fi, err := os.Stat(tgtDir)
	if err == nil && !fi.IsDir() {
		return fmt.Errorf("failed to move file to blob storage, %s is not a directory", tgtDir)
	}
	if err != nil {
		//#nosec G301 directory permissions are intentionally world readable.
		err = os.MkdirAll(tgtDir, 0o755)
		if err != nil {
			return fmt.Errorf("unable to create blob storage directory %s: %w", tgtDir, err)
		}
	}
	// write the blob into the store
	blobName := filepath.Join(tgtDir, d.Encoded())
	//#nosec G306 blob permissions are intentionally world readable.
	err = os.WriteFile(blobName, b, 0o644)
	if err != nil {
		dr.log.Debug("blob create failed", "repo", dr.name, "digest", d.String(), "err", err)
	} else {
		dr.log.Debug("blob created", "repo", dr.name, "digest", d.String())
	}
	return err
}

// Walk is used to traverse the contents of a repo.
// Descriptors that are not found will be silently skipped.
func (dr *dirRepo) Walk(depth types.ManifestParseDepth, retReader bool, descList ...types.Descriptor) iter.Seq[WalkStep] {
	return func(yield func(WalkStep) bool) {
		for i := 0; i < len(descList); i++ { // descList may be appended in this loop
			d := descList[i]
			bm, err := dr.BlobMeta(d.Digest)
			if err != nil {
				dr.log.Debug("walk failed to get blob metadata", "repo", dr.name, "digest", d.Digest, "err", err)
				continue
			}
			step := WalkStep{Desc: d, Meta: BlobMeta{Mod: bm.Mod, Size: bm.Size}}
			// always read manifests to parse later
			var raw []byte
			if types.MediaTypeManifest(d.MediaType) {
				rdr, err := dr.BlobGet(d.Digest)
				if err != nil {
					dr.log.Debug("walk failed to get blob", "repo", dr.name, "digest", d.Digest, "err", err)
					continue
				}
				raw, err = io.ReadAll(rdr)
				_ = rdr.Close()
				if err != nil {
					dr.log.Debug("walk failed to read blob", "repo", dr.name, "digest", d.Digest, "err", err)
					continue
				}
				if retReader {
					step.Rdr = types.BytesReadCloser{Reader: bytes.NewReader(raw)}
				}
			} else if retReader {
				// reader requested for blobs
				step.Rdr, err = dr.BlobGet(d.Digest)
				if err != nil {
					dr.log.Debug("walk failed to get blob", "repo", dr.name, "digest", d.Digest, "err", err)
					continue
				}
			}
			if !yield(step) {
				return
			}
			if len(raw) > 0 {
				addDesc, _ := types.ManifestParseDescriptors(raw, d, depth)
				if len(addDesc) > 0 {
					descList = append(descList, addDesc...)
				}
			}
		}
	}
}

// GC (garbage collect) cleans unmarked blobs that have been created before the cutoff time.
func (dr *dirRepo) GC(cutoff time.Time, keepDig map[digest.Digest]bool) error {
	if *dr.d.conf.ReadOnly {
		return types.ErrReadOnly
	}
	algoDirs, err := os.ReadDir(filepath.Join(dr.path, blobsDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("failed to read dir %s: %v", filepath.Join(dr.path, blobsDir), err)
	}
	for _, algoDir := range algoDirs {
		if !algoDir.IsDir() {
			continue
		}
		encodeFiles, err := os.ReadDir(filepath.Join(dr.path, blobsDir, algoDir.Name()))
		if err != nil {
			return fmt.Errorf("failed to read dir %s: %v", filepath.Join(dr.path, blobsDir, algoDir.Name()), err)
		}
		for _, encodeFile := range encodeFiles {
			d, err := digest.Parse(algoDir.Name() + ":" + encodeFile.Name())
			if err != nil || keepDig[d] {
				// skip unparsable digests and those on the keep list
				continue
			}
			filename := filepath.Join(dr.path, blobsDir, algoDir.Name(), encodeFile.Name())
			fi, err := os.Stat(filename)
			if err != nil || fi.ModTime().After(cutoff) {
				// skip nodes that cannot be stat or were written after the cutoff
				continue
			}
			err = os.Remove(filename)
			if err != nil {
				dr.log.Debug("gc failed removing blob", "repo", dr.name, "filename", filename, "err", err)
			} else {
				dr.log.Debug("gc pruned blob", "repo", dr.name, "digest", d.String())
			}
		}
	}
	return nil
}

// Close indicates the repo is no longer being accessed and resources may be freed.
func (dr *dirRepo) Close() error {
	if *dr.d.conf.ReadOnly {
		return nil
	}
	errs := []error{}
	// remove the upload directory
	uploadFiles, err := os.ReadDir(filepath.Join(dr.path, uploadDir))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, err)
	} else if err == nil {
		empty := true
		for _, de := range uploadFiles {
			if !de.Type().IsRegular() || !strings.HasPrefix(de.Name(), "upload.") {
				empty = false
				continue
			}
			err := os.Remove(filepath.Join(dr.path, uploadDir, de.Name()))
			if err != nil {
				empty = false
				dr.log.Debug("dirRepo close: failed to delete upload file", "repo", dr.name, "file", de.Name(), "err", err)
				errs = append(errs, err)
			}
		}
		if empty {
			err := os.Remove(filepath.Join(dr.path, uploadDir))
			if err != nil {
				dr.log.Debug("dirRepo close: failed to delete upload dir", "repo", dr.name, "err", err)
				errs = append(errs, err)
			} else {
				dr.log.Debug("dirRepo close: pruned upload dir", "repo", dr.name)
			}
		}
	}
	if len(errs) == 0 && *dr.d.conf.GC.EmptyRepo {
		blobsEmpty := true
		// prune empty algorithm folders
		algoDirs, err := os.ReadDir(filepath.Join(dr.path, blobsDir))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		} else if err == nil {
			for _, algoDE := range algoDirs {
				if !algoDE.IsDir() {
					blobsEmpty = false
					continue
				}
				hashFiles, err := os.ReadDir(filepath.Join(dr.path, blobsDir, algoDE.Name()))
				if err != nil {
					errs = append(errs, err)
					blobsEmpty = false
					continue
				}
				if len(hashFiles) > 0 {
					blobsEmpty = false
					continue
				}
				err = os.Remove(filepath.Join(dr.path, blobsDir, algoDE.Name()))
				if err != nil {
					dr.log.Debug("dirRepo close: failed to delete algorithm dir", "repo", dr.name, "algo", algoDE.Name(), "err", err)
					errs = append(errs, err)
				} else {
					dr.log.Debug("dirRepo close: pruned algorithm dir", "repo", dr.name, "algo", algoDE.Name())
				}
			}
			if blobsEmpty {
				err = os.Remove(filepath.Join(dr.path, blobsDir))
				if err != nil {
					dr.log.Debug("dirRepo close: failed to delete blobs dir", "repo", dr.name, "err", err)
					errs = append(errs, err)
				} else {
					dr.log.Debug("dirRepo close: pruned blobs dir", "repo", dr.name)
				}
			}
		}
		// if blob store is empty, verify the only contents remaining are the index.json and oci-layout files
		if blobsEmpty {
			files, err := os.ReadDir(dr.path)
			if err != nil {
				blobsEmpty = false
			} else {
				if len(files) > 2 {
					blobsEmpty = false
				} else {
					for _, file := range files {
						if !slices.Contains([]string{indexFile, layoutFile}, file.Name()) {
							blobsEmpty = false
						}
					}
				}
			}
		}
		// if the repo is empty, remove the index.json, oci-layout, and any empty directories up to the dir root
		if blobsEmpty {
			if dr.validOL {
				err = os.Remove(filepath.Join(dr.path, layoutFile))
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					dr.log.Debug("dirRepo close: failed to delete oci-layout", "repo", dr.name, "err", err)
					errs = append(errs, err)
				}
				dr.validOL = false
			}
			if dr.indexEmpty {
				err = os.Remove(filepath.Join(dr.path, indexFile))
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					dr.log.Debug("dirRepo close: failed to delete index.json", "repo", dr.name, "err", err)
					errs = append(errs, err)
				}
			}
			// clean empty directories under the root dir
			repoPath, err := filepath.Rel(dr.d.root, dr.path)
			if err != nil {
				errs = append(errs, err)
			} else if !types.PathRE.MatchString(repoPath) {
				errs = append(errs, fmt.Errorf("invalid repo path, cannot clean directories: %s", repoPath))
			} else {
				splitPath := strings.Split(repoPath, string(filepath.Separator))
				for len(splitPath) > 0 {
					curDir := filepath.Join(dr.d.root, filepath.Join(splitPath...))
					de, err := os.ReadDir(curDir)
					if err != nil {
						errs = append(errs, err)
						break
					} else if len(de) > 0 {
						// directory not empty, stop cleaning
						break
					}
					err = os.Remove(curDir)
					if err != nil {
						dr.log.Debug("dirRepo close: failed to delete repo path", "repo", dr.name, "curDir", curDir, "err", err)
						errs = append(errs, err)
						break
					} else {
						dr.log.Debug("dirRepo close: pruned repo path", "repo", dr.name, "curDir", curDir)
					}
					splitPath = splitPath[:len(splitPath)-1]
				}
			}
		}
	}
	return errors.Join(errs...)
}

// layoutVerify ingests the oci-layout file, verifying the contents.
// This may return a [types.ErrNotFound] when either is missing.
// All other errors should block working with the repo directory.
func (dr *dirRepo) layoutVerify() error {
	if dr.validOL {
		return nil
	}
	//#nosec G304 internal method is only called with filenames within admin provided path.
	fh, err := os.Open(filepath.Join(dr.path, layoutFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%v%.0w", err, types.ErrNotFound)
		}
		return err
	}
	defer fh.Close()
	if err := types.LayoutVerify(fh); err != nil {
		return fmt.Errorf("failed to verify layout file %s: %w", filepath.Join(dr.path, layoutFile), err)
	}
	dr.validOL = true
	return nil
}

// layoutWrite writes the oci-layout if needed.
func (dr *dirRepo) layoutWrite() error {
	if *dr.d.conf.ReadOnly {
		return types.ErrReadOnly
	}
	err := dr.layoutVerify()
	if err == nil {
		return err // noop
	} else if !errors.Is(err, types.ErrNotFound) {
		return err
	}
	// ensure repo directory exists (blob creation should have already done this)
	stat, err := os.Stat(dr.path)
	if err == nil && !stat.IsDir() {
		return fmt.Errorf("repo %s is not a directory", dr.path)
	}
	if err != nil {
		//#nosec G301 directory permissions are intentionally world readable.
		err = os.MkdirAll(dr.path, 0o755)
		if err != nil {
			dr.log.Debug("dirRepo layout mkdir failed", "repo", dr.name, "err", err)
			return fmt.Errorf("failed to create repo directory %s: %w", dr.path, err)
		}
	}
	//#nosec G306 only called with filenames within admin provided path.
	fh, err := os.Create(filepath.Join(dr.path, layoutFile))
	if err != nil {
		dr.log.Debug("dirRepo layout create failed", "repo", dr.name, "err", err)
		return err
	}
	defer fh.Close()
	l := types.Layout{Version: types.LayoutVersion}
	err = json.NewEncoder(fh).Encode(l)
	if err != nil {
		return err
	}
	return nil
}

// Writer is used to push the blob content.
func (dru *dirRepoUpload) Write(p []byte) (n int, err error) {
	dru.mu.Lock()
	defer dru.mu.Unlock()
	if dru.fh == nil {
		return 0, fmt.Errorf("writer is closed")
	}
	n, err = dru.fh.Write(p)
	return n, err
}

// Reader returns a reader from the start of the blob.
// The reader should always be fully consumed before calling Write again.
func (dru *dirRepoUpload) Reader() io.Reader {
	dru.mu.Lock()
	defer dru.mu.Unlock()
	if dru.tempName == "" {
		return nil
	}
	rdr, err := os.Open(dru.tempName)
	if err != nil {
		return nil
	}
	return rdr
}

// Save is used to store the blob to a given digest value.
// The store is not required to verify this value.
func (dru *dirRepoUpload) Save(d digest.Digest) error {
	if d.IsZero() {
		return types.ErrDigestInvalid
	}
	dru.mu.Lock()
	defer dru.mu.Unlock()
	err := dru.fh.Close()
	dru.fh = nil
	if err != nil {
		return errors.Join(err, os.Remove(dru.tempName))
	}
	// move temp file to blob store
	tgtDir := filepath.Join(dru.repoPath, blobsDir, d.Algorithm().String())
	fi, err := os.Stat(tgtDir)
	if err == nil && !fi.IsDir() {
		return errors.Join(fmt.Errorf("failed to move file to blob storage, %s is not a directory", tgtDir),
			os.Remove(dru.tempName))
	}
	if err != nil {
		//#nosec G301 directory permissions are intentionally world readable.
		err = os.MkdirAll(tgtDir, 0o755)
		if err != nil {
			return errors.Join(fmt.Errorf("unable to create blob storage directory %s: %w", tgtDir, err),
				os.Remove(dru.tempName))
		}
	}
	blobName := filepath.Join(tgtDir, d.Encoded())
	err = os.Rename(dru.tempName, blobName)
	if err == nil {
		//#nosec G302 blob permissions are intentionally world readable.
		err = os.Chmod(blobName, 0o644)
	}
	dru.tempName = ""
	if err != nil {
		dru.dr.log.Debug("blob create failed", "repo", dru.dr.name, "digest", d.String(), "err", err)
	} else {
		dru.dr.log.Debug("blob created", "repo", dru.dr.name, "digest", d.String())
	}
	return err
}

// Cancel is used to stop an upload.
func (dru *dirRepoUpload) Cancel() error {
	dru.mu.Lock()
	defer dru.mu.Unlock()
	errs := []error{}
	if dru.fh != nil {
		if err := dru.fh.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if dru.tempName != "" {
		if err := os.Remove(dru.tempName); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	dr := dru.dr
	dr.mu.Lock()
	defer dr.mu.Unlock()
	return errors.Join(errs...)
}
