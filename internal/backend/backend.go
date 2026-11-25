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

// Package backend creates an interface for OCI and registry abstractions on top of a [store.Store].
package backend

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	digest "github.com/sudo-bmitch/oci-digest"

	"github.com/olareg/olareg/config"
	"github.com/olareg/olareg/internal/cache"
	"github.com/olareg/olareg/internal/reproducible"
	"github.com/olareg/olareg/internal/store"
	"github.com/olareg/olareg/types"
)

const (
	indexFile   = "index.json"
	layoutFile  = "oci-layout"
	blobsDir    = "blobs"
	cacheAgeMin = time.Minute * 15
)

// Backend is used to manage a collection of OCI Layouts.
type Backend struct {
	mu    sync.Mutex
	conf  config.Config
	store store.Store
	repos *cache.Cache[string, *Repo]
	stop  chan struct{}
}

// Repo is used to manage a single OCI Layout.
type Repo struct {
	mu        sync.RWMutex
	conf      config.Config
	name      string
	store     store.Repo
	index     types.LayoutIndex
	tags      map[string]digest.Digest
	manifests map[digest.Digest]types.Descriptor
	referrers map[digest.Digest]indexCache
	history   map[string]indexCache
	uploads   *cache.Cache[string, *uploadSession]
	mod       time.Time
}

type UploadStatus struct {
	Size int64
}

type indexCache struct {
	descriptor types.Descriptor
	index      types.Index
	raw        []byte
}

type uploadSession struct {
	mu        sync.Mutex
	bc        store.BlobCreator
	alg       digest.Algorithm
	w         digest.Writer
	size      int64
	sessionID string
}

type BlobOpt func(*blobConfig) error

type blobConfig struct {
	algo   digest.Algorithm
	expect digest.Digest
	b      []byte
	rdr    io.Reader
}

type manifestSubject struct {
	MediaType    string            `json:"mediaType,omitempty"`
	ArtifactType string            `json:"artifactType,omitempty"`
	Config       types.Descriptor  `json:"config"`
	Annotations  map[string]string `json:"annotations,omitempty"`
	Subject      *types.Descriptor `json:"subject,omitempty"`
}

// BlobWithAlgorithm configures the digest algorithm used while uploading the blob.
func BlobWithAlgorithm(a digest.Algorithm) BlobOpt {
	return func(bc *blobConfig) error {
		bc.algo = a
		return nil
	}
}

// BlobWithBytes immediately uploads the blob without creating an upload session.
func BlobWithBytes(b []byte) BlobOpt {
	return func(bc *blobConfig) error {
		bc.b = b
		return nil
	}
}

// BlobWithDigest is used to set the expected digest when creating the blob from a reader or bytes.
func BlobWithDigest(d digest.Digest) BlobOpt {
	return func(bc *blobConfig) error {
		if d.IsZero() {
			return fmt.Errorf("invalid digest: %s", d.String())
		}
		bc.expect = d
		bc.algo = d.Algorithm()
		return nil
	}
}

// BlobWithReader immediately uploads the blob without creating an upload session.
func BlobWithReader(rdr io.Reader) BlobOpt {
	return func(bc *blobConfig) error {
		bc.rdr = rdr
		return nil
	}
}

// New creates a new backend with the associated storage.
func New(conf config.Config) (*Backend, error) {
	s, err := store.New(conf.Storage, store.WithLog(conf.Log))
	if err != nil {
		return nil, err
	}
	b := &Backend{
		conf:  conf,
		store: s,
		stop:  make(chan struct{}),
	}
	cacheOpts := cache.Opts[string, *Repo]{}
	if !s.IsEphemeral() {
		cacheOpts.Age = min(cacheAgeMin, conf.Storage.GC.GracePeriod*2)
		cacheOpts.PruneFn = func(s string, r *Repo) error {
			if !r.uploads.IsEmpty() {
				return fmt.Errorf("upload in progress")
			}
			return r.close()
		}
	}
	b.repos = cache.New(cacheOpts)
	if !*conf.Storage.ReadOnly && conf.Storage.GC.Frequency > 0 {
		go b.gcTick()
	}
	return b, nil
}

// RepoGet loads a [Repo] from the underlying storage, or initializes an empty repo if it is new.
// If the underlying storage was not written by olareg, the repository may be scanned for nested manifests and to generate referrer responses.
func (b *Backend) RepoGet(path string) (*Repo, error) {
	// validate the path
	if !types.PathRE.MatchString(path) {
		return nil, fmt.Errorf("repo path is invalid: %s%.0w", path, types.ErrRepoNotAllowed)
	}
	repoList := strings.Split(path, "/")
	for _, reserved := range []string{indexFile, layoutFile, blobsDir} {
		if slices.Contains(repoList, reserved) {
			return nil, fmt.Errorf("repo %s cannot contain reserved name %s%.0w", path, reserved, types.ErrRepoNotAllowed)
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.store == nil {
		return nil, fmt.Errorf("backend close or never initialized")
	}
	if r, err := b.repos.Get(path); err == nil && r != nil {
		return r, nil
	}
	sr, err := b.store.RepoGet(path)
	if err != nil {
		b.conf.Log.Debug("store failed to get repo", "repo", path, "err", err)
		return nil, err
	}
	ind, err := sr.IndexGet()
	if err != nil {
		return nil, err
	}
	r := &Repo{
		conf:      b.conf,
		name:      path,
		store:     sr,
		index:     ind,
		tags:      map[string]digest.Digest{},
		manifests: map[digest.Digest]types.Descriptor{},
		referrers: map[digest.Digest]indexCache{},
		history:   map[string]indexCache{},
	}
	uploadCacheOpt := cache.Opts[string, *uploadSession]{
		PruneFn: func(s string, us *uploadSession) error {
			_ = us.bc.Cancel()
			return nil
		},
	}
	if b.conf.Storage.GC.RepoUploadMax > 0 {
		uploadCacheOpt.Count = b.conf.Storage.GC.RepoUploadMax
	}
	if b.conf.Storage.GC.GracePeriod > 0 {
		uploadCacheOpt.Age = b.conf.Storage.GC.GracePeriod
	}
	r.uploads = cache.New(uploadCacheOpt)
	// load values from the index.json for easier access
	for _, desc := range r.index.Manifests {
		descMin := descriptorTrim(desc)
		r.manifests[desc.Digest] = descMin
		if tag, ok := desc.AnnotationVal(types.AnnotRefName); ok {
			r.tags[tag] = desc.Digest
		}
		if subj, ok := desc.AnnotationVal(types.AnnotReferrerSubject); ok && *r.conf.API.Referrer.Enabled {
			if subjDig, err := digest.Parse(subj); err == nil {
				r.referrers[subjDig] = indexCache{descriptor: descMin}
			} else {
				// force a walk on a parsing failure
				r.index.OlaregExt.Referrers = false
			}
		}
		if tag, ok := desc.AnnotationVal(types.AnnotTagHistory); ok && *r.conf.API.TagHistory.Enabled {
			r.history[tag] = indexCache{descriptor: descMin}
		}
	}
	indexChanged := false
	// if the index is not empty and missing either child manifests or referrers from olareg, walk it
	if len(r.index.Manifests) > 0 && (!r.index.OlaregExt.ChildManifests || (!r.index.OlaregExt.Referrers && *r.conf.API.Referrer.Enabled)) {
		b.conf.Log.Debug("walking repo to convert to olareg structures", "repo", path)
		manifestsNew := map[digest.Digest]types.Descriptor{}
		referrersNew := map[digest.Digest][]types.Descriptor{}
		for step := range sr.Walk(types.ParseManifests, true, r.index.Manifests...) {
			descMin := descriptorTrim(step.Desc)
			manifestsNew[step.Desc.Digest] = descMin
			raw, err := io.ReadAll(step.Rdr)
			_ = step.Rdr.Close()
			if err != nil {
				b.conf.Log.Info("repoGet failed to read manifest", "repo", path, "digest", step.Desc.Digest, "err", err)
				continue
			}
			if *r.conf.API.Referrer.Enabled {
				// parse the full referrer response manifest into the cache
				if subj, ok := step.Desc.AnnotationVal(types.AnnotReferrerSubject); ok {
					dig, err := digest.Parse(subj)
					respInd := types.Index{}
					if err == nil {
						err = json.Unmarshal(raw, &respInd)
					}
					if err == nil && (respInd.SchemaVersion != 2 || respInd.MediaType != types.MediaTypeOCI1ManifestList) {
						err = fmt.Errorf("unsupported schema version or media type on referrers response, subject %s", subj)
					}
					if err == nil {
						r.referrers[dig] = indexCache{
							descriptor: step.Desc,
							index:      respInd,
							raw:        raw,
						}
					} else {
						b.conf.Log.Info("repoGet failed to parse referrers response", "repo", path, "digest", step.Desc.Digest, "err", err)
					}
				}
				// parse manifests for a subject to rebuild/verify the referrers response
				if subj, resp, err := types.ManifestReferrerDescriptor(raw, step.Desc); err == nil {
					if !slices.ContainsFunc(referrersNew[subj.Digest], func(cur types.Descriptor) bool { return cur.Digest.Equal(resp.Digest) }) {
						referrersNew[subj.Digest] = append(referrersNew[subj.Digest], resp)
					}
				} else if !errors.Is(err, types.ErrNotFound) {
					b.conf.Log.Info("repoGet failed to parse manifest for subject", "repo", path, "digest", step.Desc.Digest, "err", err)
				}
			}
		}
		// save the full list of manifests, including child entries
		r.manifests = manifestsNew
		// delete missing manifests from index
		for i, v := range slices.Backward(r.index.Manifests) {
			if _, ok := manifestsNew[v.Digest]; !ok {
				r.index.Manifests = slices.Delete(r.index.Manifests, i, i+1)
				indexChanged = true
			}
		}
		// add missing manifests to index
		for dig, desc := range manifestsNew {
			if !slices.ContainsFunc(r.index.Manifests, func(cur types.Descriptor) bool { return cur.Digest.Equal(dig) }) {
				r.index.Manifests = append(r.index.Manifests, desc)
				indexChanged = true
			}
		}
		if *r.conf.API.Referrer.Enabled {
			// verify/update existing referrers responses
			for subj, respCache := range r.referrers {
				newList, ok := referrersNew[subj]
				if !ok {
					// prune stale referrers response
					mod, err := r.indexDelByAnnotation(types.AnnotReferrerSubject, subj.String())
					if err != nil {
						b.conf.Log.Info("failed to prune stale referrers response", "repo", path, "subject", subj.String(), "err", err)
					}
					if mod {
						indexChanged = true
					}
					delete(r.referrers, subj)
					continue
				}
				changed := false
				// prune referrer entries that no longer exist
				startLen := len(respCache.index.Manifests)
				respCache.index.Manifests = slices.DeleteFunc(respCache.index.Manifests, func(cur types.Descriptor) bool {
					return !slices.ContainsFunc(newList, func(newEntry types.Descriptor) bool { return cur.Digest.Equal(newEntry.Digest) })
				})
				if startLen != len(respCache.index.Manifests) {
					changed = true
				}
				// add referrers that weren't listed
				for _, desc := range newList {
					if !slices.ContainsFunc(respCache.index.Manifests, func(cur types.Descriptor) bool { return cur.Digest.Equal(desc.Digest) }) {
						respCache.index.Manifests = append(respCache.index.Manifests, desc)
						changed = true
					}
				}
				// refresh response cache, optionally writing back to the underlying store
				if changed {
					err := r.cachePut(&respCache)
					if err != nil {
						b.conf.Log.Info("repoGet failed to save referrers response", "repo", path, "subject", subj, "err", err)
						continue
					}
					mod, err := r.indexAddByAnnotation(respCache.descriptor, types.AnnotReferrerSubject, subj.String())
					if err != nil {
						b.conf.Log.Info("repoGet failed to add referrers entry to index", "repo", path, "subject", subj, "err", err)
						continue
					}
					if mod {
						indexChanged = true
					}
				} else {
					// trim cache, pull from store
					respCache.raw = nil
					respCache.index = types.Index{}
				}
				r.referrers[subj] = respCache
			}
			// add referrer responses that are missing from the index
			for subj, descList := range referrersNew {
				if _, ok := r.referrers[subj]; ok {
					continue
				}
				respCache := cacheNew()
				respCache.index.Manifests = descList
				err := r.cachePut(&respCache)
				if err != nil {
					b.conf.Log.Info("repoGet failed to save referrers response", "repo", path, "subject", subj, "err", err)
					continue
				}
				r.referrers[subj] = respCache
				mod, err := r.indexAddByAnnotation(respCache.descriptor, types.AnnotReferrerSubject, subj.String())
				if err != nil {
					b.conf.Log.Info("repoGet failed to add referrers entry to index", "repo", path, "subject", subj, "err", err)
					continue
				}
				if mod {
					indexChanged = true
				}
				// prune fallback tags
				mod, err = r.indexDelByAnnotation(types.AnnotRefName, fallbackTag(subj))
				if err != nil {
					b.conf.Log.Info("repoGet failed to prune referrers entry from index", "repo", path, "subject", subj, "err", err)
					continue
				}
				if mod {
					indexChanged = true
				}
			}
		}
		r.index.OlaregExt.ChildManifests = true
		r.index.OlaregExt.Referrers = *r.conf.API.Referrer.Enabled
	}
	// initialize history if missing, based on created annotation in index.json
	if !r.index.OlaregExt.TagHistory && *r.conf.API.TagHistory.Enabled {
		for _, desc := range r.index.Manifests {
			tag, ok := desc.AnnotationVal(types.AnnotRefName)
			if !ok {
				continue
			}
			t := &time.Time{}
			createdStr, ok := desc.AnnotationVal(types.AnnotCreated)
			if ok {
				created, err := time.Parse(time.RFC3339, createdStr)
				if err == nil {
					t = &created
				}
			}
			mod, err := r.historyAdd(tag, types.AnnotTagEventCreated, desc, t)
			if err != nil {
				b.conf.Log.Info("repoGet failed to update tag history", "repo", path, "tag", tag, "err", err)
			}
			if mod {
				indexChanged = true
			}
		}
	}
	r.index.OlaregExt.TagHistory = *r.conf.API.TagHistory.Enabled
	if indexChanged && !*b.conf.Storage.ReadOnly {
		err := r.store.IndexSet(r.index)
		if err != nil {
			b.conf.Log.Info("repoGet failed to save index", "repo", path, "err", err)
			return nil, err
		}
	}
	// set the repo at the end to avoid race conditions (e.g. with GC)
	b.repos.Set(path, r)
	b.conf.Log.Debug("get repo complete", "repo", path)
	return r, nil
}

// Close frees resources used by the backend.
// No further calls should be made to the backend after this.
func (b *Backend) Close() error {
	// stop the GC ticker
	close(b.stop)
	cutoff := time.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	errs := []error{}
	repos, err := b.repos.List()
	if err != nil {
		return err
	}
	for _, name := range repos {
		repo, err := b.repos.Get(name)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to get repo %s: %w", name, err))
			continue
		}
		if !*b.conf.Storage.ReadOnly {
			if err := repo.uploads.DeleteAll(); err != nil {
				b.conf.Log.Debug("failed to delete uploads", "repo", name, "err", err)
				errs = append(errs, fmt.Errorf("failed to delete uploads from repo %s: %w", name, err))
			}
			repo.gc(cutoff)
		}
		repo.mu.Lock()
		if err := repo.store.Close(); err != nil {
			b.conf.Log.Debug("failed to close store repo", "repo", name, "err", err)
			errs = append(errs, fmt.Errorf("failed to close repo %s: %w", name, err))
		}
		repo.store = nil
		repo.mu.Unlock()
	}
	b.repos = nil
	if err := b.store.Close(); err != nil {
		b.conf.Log.Debug("failed to close store", "err", err)
		errs = append(errs, fmt.Errorf("failed to close store: %w", err))
	}
	b.store = nil
	return errors.Join(errs...)
}

// gcTick is the goroutine to periodically prune all recently modified repos.
func (b *Backend) gcTick() {
	if b.conf.Storage.GC.Frequency <= 0 {
		return
	}
	ticker := time.NewTicker(b.conf.Storage.GC.Frequency)
	prev := time.Time{}
	for {
		select {
		case cur := <-ticker.C:
			b.gc(cur, prev)
			prev = cur
		case <-b.stop:
			ticker.Stop()
			b.conf.Log.Debug("GC timer stopped")
			return
		}
	}
}

func (b *Backend) gc(cur, prev time.Time) {
	if b.conf.Storage.GC.GracePeriod > 0 {
		cur = cur.Add(b.conf.Storage.GC.GracePeriod * -1)
		prev = prev.Add(b.conf.Storage.GC.GracePeriod * -1)
	}
	// only hold the lock for long enough to get all the repo pointers
	repos, err := b.repos.List()
	if err != nil {
		b.conf.Log.Debug("failed to list repos", "err", err)
		return
	}
	for _, name := range repos {
		repo, err := b.repos.Get(name)
		if err != nil {
			b.conf.Log.Debug("failed to get repo", "repo", name, "err", err)
			continue
		}
		// if stop ch was closed, exit immediately
		select {
		case <-b.stop:
			return
		default:
		}
		// skip repos that were not updated since the last check, offsetting for the grace period
		repo.mu.RLock()
		outsideRange := repo.mod.Before(prev)
		repo.mu.RUnlock()
		if outsideRange {
			continue
		}
		repo.gc(cur)
	}
}

// BlobDelete removes the requested blob.
func (r *Repo) BlobDelete(dig digest.Digest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.manifests[dig]; ok {
		return fmt.Errorf("cannot delete a manifest from the blob API%.0w", types.ErrForbidden)
	}
	r.mod = time.Now()
	return r.store.BlobDelete(dig)
}

// BlobGet returns the request blob.
func (r *Repo) BlobGet(dig digest.Digest) (io.ReadSeekCloser, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rdr, err := r.store.BlobGet(dig)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve the blob %s: %w", dig.String(), err)
	}
	return rdr, nil
}

// ManifestDelete removes the requested manifest by digest.
func (r *Repo) ManifestDelete(dig digest.Digest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	desc, ok := r.manifests[dig]
	if !ok {
		return fmt.Errorf("manifest not found: %s%.0w", dig.String(), types.ErrNotFound)
	}
	// refuse to delete manifests used by internal data structures (referrers and tag history)
	for _, ic := range r.referrers {
		if ic.descriptor.Digest.Equal(dig) {
			return fmt.Errorf("delete forbidden, manifest is used internally for the referrers response%.0w", types.ErrForbidden)
		}
	}
	for _, ic := range r.history {
		if ic.descriptor.Digest.Equal(dig) {
			return fmt.Errorf("delete forbidden, manifest is used internally for the tag history%.0w", types.ErrForbidden)
		}
	}
	r.mod = time.Now()
	// remove referrers responses to the manifest
	modRef, err := r.referrersDel(dig)
	if err != nil {
		return err
	}
	// remove from the blob store, ignoring "not found" errors
	err = r.store.BlobDelete(dig)
	if err != nil && !errors.Is(err, types.ErrNotFound) {
		return err
	}
	// remove from the index
	modIndex, err := r.indexDelByDigest(desc)
	if err != nil {
		return err
	}
	// remove from the descriptor map
	delete(r.manifests, dig)
	// write index
	if !modRef && !modIndex {
		return nil
	}
	err = r.store.IndexSet(r.index)
	if err != nil {
		return err
	}
	return nil
}

// ManifestGet returns the a manifest by digest.
func (r *Repo) ManifestGet(dig digest.Digest) (types.Descriptor, io.ReadSeekCloser, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	desc, ok := r.manifests[dig]
	if !ok {
		return types.Descriptor{}, nil, fmt.Errorf("manifest %s not found%.0w", dig.String(), types.ErrNotFound)
	}
	rdr, err := r.store.BlobGet(dig)
	if err != nil {
		return types.Descriptor{}, nil, fmt.Errorf("failed to retrieve the manifest %s: %.0w", dig.String(), err)
	}
	return desc, rdr, nil
}

// ManifestHead returns the descriptor for a manifest.
func (r *Repo) ManifestHead(dig digest.Digest) (types.Descriptor, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	desc, ok := r.manifests[dig]
	if !ok {
		return types.Descriptor{}, fmt.Errorf("manifest %s not found%.0w", dig.String(), types.ErrNotFound)
	}
	return desc, nil
}

// ManifestPut pushes a new manifest to the store after validation.
// Tag history and referrers are also managed.
// Upon success, the manifest descriptor, processed tags, and subject digest are returned.
func (r *Repo) ManifestPut(b []byte, mediaType string, dig digest.Digest, tags []string) (types.Descriptor, []string, digest.Digest, error) {
	// validate the media type
	if mediaType == "" {
		mediaType = types.MediaTypeDetect(b)
	}
	if !types.MediaTypeManifest(mediaType) {
		return types.Descriptor{}, nil, digest.Digest{}, fmt.Errorf("unsupported media type %s%.0w", mediaType, types.ErrManifestInvalid)
	}
	// validate (or compute) the digest
	if dig.IsZero() {
		var err error
		dig, err = digest.Canonical.FromBytes(b)
		if err != nil {
			return types.Descriptor{}, nil, digest.Digest{}, fmt.Errorf("failed to compute digest: %w", err)
		}
	} else {
		comp, err := dig.Algorithm().FromBytes(b)
		if err != nil {
			return types.Descriptor{}, nil, digest.Digest{}, fmt.Errorf("failed to compute digest: %w", err)
		}
		if !comp.Equal(dig) {
			return types.Descriptor{}, nil, digest.Digest{}, fmt.Errorf("digest mismatch, expected %s, computed %s%.0w", dig.String(), comp.String(), types.ErrDigestInvalid)
		}
	}
	// validate the tags
	for _, tag := range tags {
		if !types.RefTagRE.MatchString(tag) {
			return types.Descriptor{}, nil, digest.Digest{}, fmt.Errorf("invalid tag value: %s%.0w", tag, types.ErrUnsupported)
		}
	}
	// validate the manifest body (media type and descriptors)
	if err := r.manifestVerify(b, mediaType); err != nil {
		return types.Descriptor{}, nil, digest.Digest{}, err
	}
	// push to blob store
	err := r.store.BlobPut(dig, b)
	if err != nil {
		return types.Descriptor{}, nil, digest.Digest{}, fmt.Errorf("failed to put manifest in blob store: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mod = time.Now()
	// add entry to manifests
	desc := types.Descriptor{
		MediaType: mediaType,
		Digest:    dig,
		Size:      int64(len(b)),
	}
	r.manifests[dig] = desc
	// update index and manage tags
	modIndex := false
	if len(tags) == 0 {
		modIndex, err = r.indexAddByDigest(desc)
		if err != nil {
			return types.Descriptor{}, nil, digest.Digest{}, fmt.Errorf("failed to update the index: %w", err)
		}
	}
	procTags := []string{}
	for _, tag := range tags {
		mod, err := r.indexAddByAnnotation(desc, types.AnnotRefName, tag)
		if err != nil {
			continue
		}
		procTags = append(procTags, tag)
		modIndex = modIndex || mod
	}
	subjDig, modIndexRef, err := r.referrersAdd(b, mediaType, dig)
	if err != nil {
		return types.Descriptor{}, nil, digest.Digest{}, err
	}
	if modIndex || modIndexRef {
		err = r.store.IndexSet(r.index)
		if err != nil {
			return types.Descriptor{}, nil, digest.Digest{}, err
		}
	}
	return desc, procTags, subjDig, nil
}

func (r *Repo) manifestVerify(b []byte, mt string) error {
	// TODO: limit URL values according to the config
	switch mt {
	case types.MediaTypeDocker2Manifest, types.MediaTypeOCI1Manifest:
		m := types.Manifest{}
		err := json.Unmarshal(b, &m)
		if err != nil {
			return fmt.Errorf("failed to parse manifest: %w%.0w", err, types.ErrManifestInvalid)
		}
		if m.MediaType != "" && m.MediaType != mt {
			return fmt.Errorf("media type mismatch, expected %s, received %s%.0w", mt, m.MediaType, types.ErrManifestInvalid)
		}
		if !*r.conf.API.Manifest.SparseIndex {
			if _, err := r.store.BlobMeta(m.Config.Digest); err != nil {
				return fmt.Errorf("blob %s not found%.0w", m.Config.Digest.String(), types.ErrManifestBlobUnknown)
			}
			for _, d := range m.Layers {
				if len(d.URLs) > 0 || types.MediaTypeForeign(d.MediaType) {
					continue
				}
				if _, err := r.store.BlobMeta(d.Digest); err != nil {
					return fmt.Errorf("blob %s not found%.0w", d.Digest.String(), types.ErrManifestBlobUnknown)
				}
			}
		}
	case types.MediaTypeDocker2ManifestList, types.MediaTypeOCI1ManifestList:
		m := types.Index{}
		err := json.Unmarshal(b, &m)
		if err != nil {
			return fmt.Errorf("failed to parse manifest: %w%.0w", err, types.ErrManifestInvalid)
		}
		if m.MediaType != "" && m.MediaType != mt {
			return fmt.Errorf("media type mismatch, expected %s, received %s%.0w", mt, m.MediaType, types.ErrManifestInvalid)
		}
		if !*r.conf.API.Manifest.SparseIndex {
			for _, d := range m.Manifests {
				if !types.MediaTypeManifest(d.MediaType) {
					return fmt.Errorf("unsupported manifest media type: %s%.0w", d.MediaType, types.ErrManifestBlobUnknown)
				}
				if _, ok := r.manifests[d.Digest]; !ok {
					return fmt.Errorf("manifest %s not found%.0w", d.Digest.String(), types.ErrManifestBlobUnknown)
				}
			}
		}
	default:
		return fmt.Errorf("unsupported manifest media type: %s%.0w", mt, types.ErrManifestInvalid)
	}
	return nil
}

// ReferrerList returns the list of referrers for a given subject.
// When successful, it returns the descriptor of the referrers response, and the extracted Index struct.
func (r *Repo) ReferrerList(subj digest.Digest) (types.Descriptor, types.Index, error) {
	if !*r.conf.API.Referrer.Enabled {
		return types.Descriptor{}, types.Index{}, types.ErrUnsupported
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	ic, ok := r.referrers[subj]
	if !ok {
		return types.Descriptor{}, types.Index{}, types.ErrNotFound
	}
	err := r.cacheGet(&ic)
	if err != nil {
		return types.Descriptor{}, types.Index{}, err
	}
	return ic.descriptor, ic.index.Copy(), nil
}

// TagDelete deletes a tag, but not the underlying manifest.
func (r *Repo) TagDelete(tag string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	mod, err := r.indexDelByAnnotation(types.AnnotRefName, tag)
	if err != nil {
		return err
	}
	if !mod {
		return types.ErrNotFound
	}
	// update the index
	r.mod = time.Now()
	return r.store.IndexSet(r.index)
}

// TagGet returns the digest for a specific tag.
func (r *Repo) TagGet(tag string) (digest.Digest, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	dig, ok := r.tags[tag]
	if !ok {
		return digest.Digest{}, fmt.Errorf("tag %s not found%.0w", tag, types.ErrNotFound)
	}
	return dig, nil
}

// TagHistory returns the history of events for a specific tag.
func (r *Repo) TagHistory(tag string) (types.Descriptor, types.Index, error) {
	if !*r.conf.API.TagHistory.Enabled {
		return types.Descriptor{}, types.Index{}, types.ErrUnsupported
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	ic, ok := r.history[tag]
	if !ok {
		return types.Descriptor{}, types.Index{}, types.ErrNotFound
	}
	err := r.cacheGet(&ic)
	if err != nil {
		return types.Descriptor{}, types.Index{}, fmt.Errorf("failed to retrieve the history: %w", err)
	}
	return ic.descriptor.Copy(), ic.index.Copy(), nil
}

// TagList returns a list of tags and their descriptors.
func (r *Repo) TagList() (map[string]types.Descriptor, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tags := map[string]types.Descriptor{}
	for _, desc := range r.index.Manifests {
		if t, ok := desc.AnnotationVal(types.AnnotRefName); ok {
			tags[t] = desc.Copy()
		}
	}
	return tags, nil
}

// UploadCancel aborts a running upload.
func (r *Repo) UploadCancel(sessionID string) error {
	us, err := r.uploads.Get(sessionID)
	if err != nil {
		return fmt.Errorf("session %s: %w", sessionID, err)
	}
	err = us.bc.Cancel()
	if err != nil {
		return err
	}
	return r.uploads.Delete(sessionID)
}

// UploadCreate begins a new upload.
func (r *Repo) UploadCreate(opts ...BlobOpt) (string, error) {
	conf := blobConfig{
		algo: digest.Canonical,
	}
	for _, opt := range opts {
		err := opt(&conf)
		if err != nil {
			return "", err
		}
	}
	// verify blob does not already exist
	if !conf.expect.IsZero() {
		if _, err := r.store.BlobMeta(conf.expect); err == nil {
			return "", types.ErrBlobExists
		}
	}
	// if bytes were passed, create blob directly
	if conf.b != nil {
		d, err := conf.algo.FromBytes(conf.b)
		if err != nil {
			return "", err
		}
		if !conf.expect.IsZero() && !conf.expect.Equal(d) {
			return "", fmt.Errorf("digest mismatch, expected %s, received %s%.0w", conf.expect.String(), d.String(), types.ErrDigestInvalid)
		}
		err = r.store.BlobPut(d, conf.b)
		if err != nil {
			return "", err
		}
		r.mod = time.Now()
		return "", nil
	}
	// if a reader was passed, create the blob directly
	if conf.rdr != nil {
		bc, err := r.store.BlobCreate()
		if err != nil {
			return "", err
		}
		w := digest.NewWriter(bc, conf.algo)
		_, err = io.Copy(w, conf.rdr)
		if err != nil {
			_ = bc.Cancel()
			return "", err
		}
		d, err := w.Digest()
		if err != nil {
			_ = bc.Cancel()
			return "", err
		}
		if !conf.expect.IsZero() && !conf.expect.Equal(d) {
			_ = bc.Cancel()
			return "", fmt.Errorf("digest mismatch, expected %s, received %s%.0w", conf.expect.String(), d.String(), types.ErrDigestInvalid)
		}
		err = bc.Save(d)
		if err != nil {
			return "", err
		}
		r.mod = time.Now()
		return "", nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// create a new session
	sessionID, err := genSessionID()
	if err != nil {
		return "", fmt.Errorf("failed generating sessionID: %w", err)
	}
	_, err = r.uploads.Get(sessionID)
	if err == nil {
		return "", fmt.Errorf("session ID collision")
	}
	bc, err := r.store.BlobCreate()
	if err != nil {
		return "", err
	}
	w := digest.NewWriter(bc, conf.algo)
	session := &uploadSession{
		bc:        bc,
		w:         w,
		alg:       conf.algo,
		sessionID: sessionID,
	}
	r.uploads.Set(sessionID, session)
	return sessionID, nil
}

// UploadReadFrom adds data from the reader to a given offset.
// Set offset to -1 to append to the end of the current upload.
// If the offset is specified, this verifies it is the end of the upload and otherwise fails.
func (r *Repo) UploadReadFrom(sessionID string, rdr io.Reader, offset int64) error {
	us, err := r.uploads.Get(sessionID)
	if err != nil {
		return err
	}
	us.mu.Lock()
	defer us.mu.Unlock()
	if offset >= 0 && us.size != offset {
		return fmt.Errorf("invalid offset on upload, expected %d, received %d%.0w", us.size, offset, types.ErrBlobUploadInvalid)
	}
	n, err := io.Copy(us.w, rdr)
	if n > 0 {
		us.size += n
	}
	return err
}

// UploadSave verifies an upload and make the blob available.
// The value of the digest is verified before saving.
func (r *Repo) UploadSave(sessionID string, d digest.Digest) error {
	us, err := r.uploads.Get(sessionID)
	if err != nil {
		return err
	}
	if d.IsZero() {
		return fmt.Errorf("digest must be defined to save the blob")
	}
	us.mu.Lock()
	defer us.mu.Unlock()
	// remove the upload session from the cache after saving
	defer r.uploads.Delete(sessionID)
	// save only after verifying the digest
	if us.w.Verify(d) {
		r.mu.Lock()
		r.mod = time.Now()
		r.mu.Unlock()
		return us.bc.Save(d)
	}
	dComp, err := us.w.Digest()
	if err != nil {
		return fmt.Errorf("failed computing digest of blob: %w", err)
	}
	// handle a change in the digest algorithm
	if !dComp.Algorithm().Equal(d.Algorithm()) {
		rdr := us.bc.Reader()
		if rdr == nil {
			return fmt.Errorf("failed to get reader to recompute blob digest")
		}
		dComp, err = d.Algorithm().FromReader(rdr)
		if err != nil {
			return fmt.Errorf("failed recomputing digest of blob: %w", err)
		}
		if d.Equal(dComp) {
			r.mu.Lock()
			r.mod = time.Now()
			r.mu.Unlock()
			return us.bc.Save(d)
		}
	}
	// failed to verify, cancel and return the error
	_ = us.bc.Cancel()
	return fmt.Errorf("digest mismatch, expected %s, received %s%.0w", d.String(), dComp.String(), types.ErrDigestInvalid)
}

// UploadStatus returns the current progress of an upload.
func (r *Repo) UploadStatus(sessionID string) (UploadStatus, error) {
	us, err := r.uploads.Get(sessionID)
	if err != nil {
		return UploadStatus{}, fmt.Errorf("session %s: %w", sessionID, err)
	}
	us.mu.Lock()
	defer us.mu.Unlock()
	return UploadStatus{Size: us.size}, nil
}

// cacheNew returns an empty cache entry ready to add entries to the index manifest list.
func cacheNew() indexCache {
	return indexCache{
		index: types.Index{
			SchemaVersion: 2,
			MediaType:     types.MediaTypeOCI1ManifestList,
			Manifests:     []types.Descriptor{},
		},
	}
}

// cacheGet loads the cache from the underlying blob store if it is not already loaded.
func (r *Repo) cacheGet(ic *indexCache) error {
	if ic == nil {
		return fmt.Errorf("nil index cache encountered")
	}
	if len(ic.raw) > 0 {
		return nil // already loaded, noop
	}
	if ic.descriptor.Digest.IsZero() {
		return fmt.Errorf("index cache cannot get a zero digest")
	}
	// load content from the cache
	rdr, err := r.store.BlobGet(ic.descriptor.Digest)
	if err != nil {
		return err
	}
	ic.raw, err = io.ReadAll(rdr)
	_ = rdr.Close()
	if err != nil {
		return err
	}
	return json.Unmarshal(ic.raw, &ic.index)
}

// cachePut rebuilds the cache from the index, and conditionally pushes it to the blob store
func (r *Repo) cachePut(ic *indexCache) error {
	if ic == nil {
		return fmt.Errorf("nil index cache encountered")
	}
	var err error
	ic.raw, err = json.Marshal(ic.index)
	if err != nil {
		return err
	}
	algo := digest.Canonical
	if !ic.descriptor.Digest.IsZero() {
		algo = ic.descriptor.Digest.Algorithm()
	}
	dig, err := algo.FromBytes(ic.raw)
	if err != nil {
		return err
	}
	desc := types.Descriptor{
		MediaType: types.MediaTypeOCI1ManifestList,
		Digest:    dig,
		Size:      int64(len(ic.raw)),
	}
	r.manifests[dig] = desc.Copy()
	ic.descriptor = desc
	if !*r.conf.Storage.ReadOnly {
		err := r.store.BlobPut(dig, ic.raw)
		if err != nil {
			return fmt.Errorf("failed to create cache entry: %w", err)
		}
		// trim in memory cache
		ic.index = types.Index{}
		ic.raw = nil
	}
	return nil
}

// close frees up the resources from a repo.
func (r *Repo) close() error {
	errs := []error{}
	if !*r.conf.Storage.ReadOnly {
		if err := r.uploads.DeleteAll(); err != nil {
			r.conf.Log.Debug("failed to delete uploads", "repo", r.name, "err", err)
			errs = append(errs, fmt.Errorf("failed to delete uploads from repo %s: %w", r.name, err))
		}
		r.gc(time.Now())
	}
	r.mu.Lock()
	if r.store != nil {
		if err := r.store.Close(); err != nil {
			r.conf.Log.Debug("failed to close store repo", "repo", r.name, "err", err)
			errs = append(errs, fmt.Errorf("failed to close repo %s: %w", r.name, err))
		}
		r.store = nil
	}
	r.mu.Unlock()
	return errors.Join(errs...)
}

// gc runs a garbage collection against the repo.
func (r *Repo) gc(cutoff time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.store == nil {
		r.conf.Log.Debug("repo gc skipped, store was closed", "repo", r.name)
		return // repo was closed
	}
	r.conf.Log.Debug("repo gc starting", "repo", r.name)
	keep := map[digest.Digest]bool{}
	// keep all tagged manifests
	for _, dig := range r.tags {
		keep[dig] = true
	}
	cutoffStr := cutoff.UTC().Format(time.RFC3339)
	for _, desc := range r.index.Manifests {
		// skip internal manifests (referrers and tag history)
		if desc.AnnotationsInternal() {
			continue
		}
		if *r.conf.Storage.GC.Untagged {
			// keep everything including untagged manifests
			keep[desc.Digest] = true
		} else {
			// keep only manifests pushed after the cutoff and those with custom annotations
			if created, ok := desc.AnnotationVal(types.AnnotCreated); (ok && created > cutoffStr) || desc.AnnotationsCustom() {
				keep[desc.Digest] = true
			}
		}
	}
	// walk to get all digests being directly preserved
	walkList := []types.Descriptor{}
	for dig := range keep {
		if d, ok := r.manifests[dig]; ok {
			walkList = append(walkList, d)
		}
	}
	for step := range r.store.Walk(types.ParseBlobs, false, walkList...) {
		keep[step.Desc.Digest] = true
	}
	// retain referrers responses
	if *r.conf.API.Referrer.Enabled {
		walkList = []types.Descriptor{}
		for subj, ic := range r.referrers {
			if keep[ic.descriptor.Digest] {
				continue // already preserved
			}
			if *r.conf.Storage.GC.ReferrersDangling || keep[subj] {
				walkList = append(walkList, ic.descriptor)
			}
		}
		for step := range r.store.Walk(types.ParseBlobs, false, walkList...) {
			keep[step.Desc.Digest] = true
		}
	}
	// retain the tag history entries if the repo is not being deleted.
	// do not walk the history contents, only keep the single manifest.
	if *r.conf.API.TagHistory.Enabled && (len(keep) > 0 || !*r.conf.Storage.GC.EmptyRepo) {
		for _, ic := range r.history {
			keep[ic.descriptor.Digest] = true
		}
	}
	// prune entries from the index.json that we aren't keeping
	start := len(r.index.Manifests)
	r.index.Manifests = slices.DeleteFunc(r.index.Manifests, func(cur types.Descriptor) bool { return !keep[cur.Digest] })
	if len(r.index.Manifests) != start {
		r.conf.Log.Debug("repo gc pruned manifest", "start", start, "end", len(r.index.Manifests))
		err := r.store.IndexSet(r.index.Copy())
		if err != nil {
			r.conf.Log.Info("failed to update index.json during GC", "err", err, "repo", r.name)
		}
	}
	// cleanup r.manifests map
	maps.DeleteFunc(r.manifests, func(key digest.Digest, _ types.Descriptor) bool { return !keep[key] })
	// prune underlying storage
	err := r.store.GC(cutoff, keep)
	if err != nil {
		r.conf.Log.Info("repo gc failed", "repo", r.name, "err", err)
	} else {
		r.conf.Log.Debug("repo gc finished", "repo", r.name)
	}
}

// historyAdd appends an event to the tag history.
// This is EXPERIMENTAL, pending the merge of https://github.com/opencontainers/distribution-spec/pull/606
func (r *Repo) historyAdd(tag string, event string, desc types.Descriptor, t *time.Time) (bool, error) {
	if !*r.conf.API.TagHistory.Enabled {
		return false, nil
	}
	ic, ok := r.history[tag]
	if ok {
		err := r.cacheGet(&ic)
		if err != nil && ic.index.MediaType == "" {
			ic = cacheNew()
		}
	} else {
		ic = cacheNew()
	}
	newDesc := descriptorTrim(desc)
	if newDesc.Annotations == nil {
		newDesc.Annotations = map[string]string{}
	}
	newDesc.Annotations[types.AnnotTagEvent] = event
	if t == nil {
		now := reproducible.TimeNow()
		t = &now
	}
	newDesc.Annotations[types.AnnotTagTimestamp] = t.UTC().Format(time.RFC3339)
	// dedup, if the last event is the same as the current event, skip with a noop
	if len(ic.index.Manifests) > 0 && ic.index.Manifests[0].Digest.Equal(desc.Digest) &&
		ic.index.Manifests[0].Annotations != nil && ic.index.Manifests[0].Annotations[types.AnnotTagEvent] == event {
		return false, nil
	}
	// add the new event
	ic.index.Manifests = slices.Insert(ic.index.Manifests, 0, newDesc)
	// prune history beyond a fixed length limit and/or a cutoff time
	if len(ic.index.Manifests) > r.conf.API.TagHistory.PruneMin {
		icLen := len(ic.index.Manifests)
		maxPrune := icLen - r.conf.API.TagHistory.PruneMin
		firstPrune := icLen - 1 - maxPrune
		cutoff := time.Now().Add(r.conf.API.TagHistory.PruneAge * -1).UTC().Format(time.RFC3339)
		// find the last entry after the cutoff, pruning that and previous entries
		for i := range maxPrune {
			if val, ok := ic.index.Manifests[firstPrune+i].AnnotationVal(types.AnnotTagTimestamp); !ok || val < cutoff {
				ic.index.Manifests = ic.index.Manifests[:firstPrune+i]
				break
			}
		}
	}
	err := r.cachePut(&ic)
	if err != nil {
		return false, err
	}
	r.history[tag] = ic
	return r.indexAddByAnnotation(ic.descriptor, types.AnnotTagHistory, tag)
}

// indexAddByDigest inserts a descriptor into the index by digest.
// The bool is true when the index is modified.
func (r *Repo) indexAddByDigest(desc types.Descriptor) (bool, error) {
	if !desc.AnnotationsEmpty() {
		return false, fmt.Errorf("cannot insert by digest with annotations set")
	}
	if slices.ContainsFunc(r.index.Manifests, func(cur types.Descriptor) bool { return cur.Digest.Equal(desc.Digest) && cur.AnnotationsEmpty() }) {
		// entry already exists, noop
		return false, nil
	}
	newDesc := desc.Copy()
	if newDesc.Annotations == nil {
		newDesc.Annotations = map[string]string{}
	}
	newDesc.Annotations[types.AnnotCreated] = reproducible.TimeNow().Format(time.RFC3339)
	r.index.Manifests = append(r.index.Manifests, newDesc)
	return true, nil
}

// indexAddByAnnotation inserts a descriptor into the index by annotation.
// Other entries with the same annotation are removed/replaced.
// When a "tag" annotation is added, the history and tag pointers are also updated.
func (r *Repo) indexAddByAnnotation(desc types.Descriptor, key, val string) (bool, error) {
	newDesc := desc.Copy()
	if newDesc.Annotations == nil {
		newDesc.Annotations = map[string]string{}
	}
	newDesc.Annotations[types.AnnotCreated] = reproducible.TimeNow().Format(time.RFC3339)
	newDesc.Annotations[key] = val
	replaced := false
	for i, cur := range r.index.Manifests {
		if cur.Same(newDesc) {
			// entry already exists, noop
			return false, nil
		}
		if annotationEq(cur, key, val) {
			// replace existing entry
			r.index.Manifests[i] = newDesc
			replaced = true
			break
		}
	}
	if !replaced {
		// add new entry
		r.index.Manifests = append(r.index.Manifests, newDesc)
	}
	if key == types.AnnotRefName {
		r.tags[val] = desc.Digest
		_, err := r.historyAdd(val, types.AnnotTagEventCreated, desc, nil)
		if err != nil {
			return true, err
		}
	}
	return true, nil
}

// indexDelByDigest removes all descriptors from the index with a given digest.
func (r *Repo) indexDelByDigest(desc types.Descriptor) (bool, error) {
	start := len(r.index.Manifests)
	indexMod := false
	// handle deleting tags
	for _, cur := range r.index.Manifests {
		if cur.Digest.Equal(desc.Digest) {
			if curTag, ok := cur.AnnotationVal(types.AnnotRefName); ok && curTag != "" {
				mod, err := r.historyAdd(curTag, types.AnnotTagEventDeleted, cur, nil)
				if err != nil {
					return mod, err
				}
				if mod {
					indexMod = true
				}
				delete(r.tags, curTag)
			}
		}
	}
	r.index.Manifests = slices.DeleteFunc(r.index.Manifests, func(cur types.Descriptor) bool { return cur.Digest.Equal(desc.Digest) })
	return indexMod || len(r.index.Manifests) != start, nil
}

// indexDelByAnnotation removes add descriptors from the index with a matching annotation.
func (r *Repo) indexDelByAnnotation(key, val string) (bool, error) {
	indexMod := false
	if key == types.AnnotRefName {
		dig, okTag := r.tags[val]
		desc, okMan := r.manifests[dig]
		if okTag && okMan {
			mod, err := r.historyAdd(val, types.AnnotTagEventDeleted, desc, nil)
			if err != nil {
				return true, err
			}
			if mod {
				indexMod = true
			}
			// when untagging, ensure one descriptor remains with "empty" annotations
			if !slices.ContainsFunc(r.index.Manifests, func(cur types.Descriptor) bool { return cur.Digest.Equal(dig) && cur.AnnotationsEmpty() }) {
				addDesc := descriptorTrim(desc)
				addDesc.Annotations = map[string]string{}
				if created, ok := desc.AnnotationVal(types.AnnotCreated); ok && created != "" {
					addDesc.Annotations[types.AnnotCreated] = created
				} else {
					addDesc.Annotations[types.AnnotCreated] = reproducible.TimeNow().Format(time.RFC3339)
				}
				r.index.Manifests = append(r.index.Manifests, addDesc)
				indexMod = true
			}
		}
		delete(r.tags, val)
	}
	start := len(r.index.Manifests)
	r.index.Manifests = slices.DeleteFunc(r.index.Manifests, func(cur types.Descriptor) bool { return annotationEq(cur, key, val) })
	return indexMod || len(r.index.Manifests) != start, nil
}

// referrersAdd parses a manifest for the subject and when found, adds it to the referrers response.
func (r *Repo) referrersAdd(b []byte, mt string, dig digest.Digest) (digest.Digest, bool, error) {
	if !*r.conf.API.Referrer.Enabled {
		return digest.Digest{}, false, nil
	}
	subj := manifestSubject{}
	err := json.Unmarshal(b, &subj)
	if err != nil || subj.Subject == nil || subj.Subject.Digest.IsZero() {
		return digest.Digest{}, false, nil
	}
	// build the descriptor that goes in the referrers response
	at := subj.ArtifactType
	if at == "" {
		at = subj.Config.MediaType
	}
	respEntry := types.Descriptor{
		MediaType:    mt,
		ArtifactType: at,
		Digest:       dig,
		Size:         int64(len(b)),
		Annotations:  subj.Annotations,
	}
	respCache, ok := r.referrers[subj.Subject.Digest]
	if ok {
		err := r.cacheGet(&respCache)
		if err != nil && respCache.index.MediaType == "" {
			respCache = cacheNew()
		}
	} else {
		respCache = cacheNew()
	}
	if slices.ContainsFunc(respCache.index.Manifests, func(cur types.Descriptor) bool { return cur.Digest.Equal(respEntry.Digest) }) {
		// response already exists, noop
		return subj.Subject.Digest, false, nil
	}
	// update the response with this entry
	respCache.index.Manifests = append(respCache.index.Manifests, respEntry)
	err = r.cachePut(&respCache)
	if err != nil {
		return digest.Digest{}, false, fmt.Errorf("failed to update referrers response: %w", err)
	}
	r.referrers[subj.Subject.Digest] = respCache
	// update the index
	mod, err := r.indexAddByAnnotation(respCache.descriptor, types.AnnotReferrerSubject, subj.Subject.Digest.String())
	if err != nil {
		return subj.Subject.Digest, mod, err
	}
	return subj.Subject.Digest, mod, nil
}

// referrersDel adjust referrers to handle the deletion of dig.
// It removes all referrers with the subject set to dig when ReferrersWithSubj is set in the GC config.
// It pulls the manifest to parse for a subject, and if found removes it from the appropriate referrers response.
func (r *Repo) referrersDel(dig digest.Digest) (bool, error) {
	if !*r.conf.API.Referrer.Enabled {
		return false, nil
	}
	// if ReferrersWithSubj set, remove all referrers pointing to this dig
	indexMod := false
	if ic, ok := r.referrers[dig]; ok && *r.conf.Storage.GC.ReferrersWithSubj {
		err := r.cacheGet(&ic)
		if err != nil {
			return indexMod, err
		}
		keepDesc := []types.Descriptor{}
		for _, desc := range ic.index.Manifests {
			// always keep tagged entries
			isTagged := false
			for _, dig := range r.tags {
				if desc.Digest.Equal(dig) {
					isTagged = true
					break
				}
			}
			if isTagged {
				keepDesc = append(keepDesc, desc)
				continue
			}
			// prune all untagged entries
			mod, err := r.indexDelByDigest(desc)
			if mod {
				indexMod = true
			}
			if err != nil {
				return indexMod, err
			}
		}
		if len(keepDesc) > 0 {
			ic.index.Manifests = keepDesc
			err := r.cachePut(&ic)
			if err != nil {
				return indexMod, err
			}
			r.referrers[dig] = ic
			mod, err := r.indexAddByAnnotation(ic.descriptor, types.AnnotReferrerSubject, dig.String())
			if mod {
				indexMod = mod
			}
			if err != nil {
				return indexMod, err
			}
		} else {
			delete(r.referrers, dig)
			mod, err := r.indexDelByAnnotation(types.AnnotReferrerSubject, dig.String())
			if mod {
				indexMod = mod
			}
			if err != nil {
				return indexMod, err
			}
		}
	}
	// read the manifest to extract the subject
	rdr, err := r.store.BlobGet(dig)
	if err != nil {
		return indexMod, err
	}
	b, err := io.ReadAll(rdr)
	_ = rdr.Close()
	if err != nil {
		return indexMod, err
	}
	subj := manifestSubject{}
	err = json.Unmarshal(b, &subj)
	// stop if there is no subject
	if err != nil || subj.Subject == nil || subj.Subject.Digest.IsZero() {
		return indexMod, nil
	}
	// update the referrers response for this subject
	respCache, ok := r.referrers[subj.Subject.Digest]
	if ok {
		err := r.cacheGet(&respCache)
		if err != nil && respCache.index.MediaType == "" {
			respCache = cacheNew()
		}
	} else {
		respCache = cacheNew()
	}
	lenStart := len(respCache.index.Manifests)
	respCache.index.Manifests = slices.DeleteFunc(respCache.index.Manifests, func(cur types.Descriptor) bool { return cur.Digest.Equal(dig) })
	// if all entries removed, delete the response
	if len(respCache.index.Manifests) == 0 {
		delete(r.referrers, subj.Subject.Digest)
		return r.indexDelByAnnotation(types.AnnotReferrerSubject, subj.Subject.Digest.String())
	}
	// if response was unchanged, stop
	if len(respCache.index.Manifests) == lenStart {
		return indexMod, nil
	}
	// save updated referrers response
	err = r.cachePut(&respCache)
	if err != nil {
		return indexMod, err
	}
	r.referrers[subj.Subject.Digest] = respCache
	mod, err := r.indexAddByAnnotation(respCache.descriptor, types.AnnotReferrerSubject, subj.Subject.Digest.String())
	if mod {
		indexMod = mod
	}
	if err != nil {
		return indexMod, err
	}
	return indexMod, nil
}

// annotationEq returns true if the annotation is set to a given value.
func annotationEq(desc types.Descriptor, key, val string) bool {
	if desc.Annotations == nil {
		return false
	}
	curVal, ok := desc.Annotations[key]
	return ok && val == curVal
}

// descriptorTrim returns the minimal descriptor fields
func descriptorTrim(desc types.Descriptor) types.Descriptor {
	return types.Descriptor{
		MediaType: desc.MediaType,
		Digest:    desc.Digest,
		Size:      desc.Size,
	}
}

var fallbackReplace = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

// fallbackTag returns the referrers response fallback tag for a given subject digest.
func fallbackTag(dig digest.Digest) string {
	algStr := fallbackReplace.ReplaceAllString(dig.Algorithm().String(), "-")
	encStr := fallbackReplace.ReplaceAllString(dig.Encoded(), "-")
	return fmt.Sprintf("%.32s-%.64s", algStr, encStr)
}

// genSessionID returns a random ID safe for use in a URL.
func genSessionID() (string, error) {
	sb := make([]byte, 16)
	_, err := rand.Read(sb)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(sb), nil
}
