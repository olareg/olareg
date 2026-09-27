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

// Package gendata is used to generate sample image data for various package tests.
package gendata

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"maps"
	"math/rand"
	"slices"
	"testing"
	"time"

	digest "github.com/sudo-bmitch/oci-digest"

	"github.com/olareg/olareg/types"
)

type (
	// Repo is the generated data matching that of a repository.
	Repo struct {
		t             *testing.T
		rng           *rand.Rand
		desc          map[digest.Digest]types.Descriptor
		blobs         map[digest.Digest][]byte
		manifests     map[digest.Digest][]byte
		manifestOrder []digest.Digest
		referrers     map[digest.Digest][]types.Descriptor
		tags          map[string]digest.Digest
	}
	OptsIndex struct {
		ArtifactType string
		Annotations  map[string]string
		DigAlgo      digest.Algorithm
		ImageDesc    []types.Descriptor
		ImageOpts    []OptsImage
		Subject      types.Descriptor
		Tags         []string
	}
	OptsImage struct {
		ArtifactType string
		Annotations  map[string]string
		DigAlgo      digest.Algorithm
		ConfDesc     types.Descriptor
		LayerDesc    []types.Descriptor
		LayerOpts    []OptsLayer
		Platform     types.Platform
		Subject      types.Descriptor
		Tags         []string
	}
	OptsLayer struct {
		MediaType   string
		Size        int
		DigAlgo     digest.Algorithm
		CompFn      func(io.Writer) io.WriteCloser
		Foreign     bool
		ForeignURLs []string
	}
	// Step is returned for each iteration of [Repo.Walk]
	Step struct {
		Desc  types.Descriptor
		Bytes []byte
		Tags  []string
	}
	// minImage contains the minimal fields required for an OCI Image config.
	minImage struct {
		Create *time.Time `json:"created,omitempty"`
		types.Platform
		Config minConfig `json:"config"`
		RootFS minRootFS `json:"rootfs"`
	}
	minConfig struct {
		User       string            `json:"User,omitempty"`
		Env        []string          `json:"Env,omitempty"`
		Entrypoint []string          `json:"Entrypoint,omitempty"`
		Cmd        []string          `json:"Cmd,omitempty"`
		WorkingDir string            `json:"WorkingDir,omitempty"`
		Labels     map[string]string `json:"Labels,omitempty"`
	}
	minRootFS struct {
		Type    string          `json:"type"`
		DiffIDs []digest.Digest `json:"diff_ids"`
	}
)

// New returns a new [Repo] where all generated data is stored.
func New(t *testing.T) *Repo {
	t.Helper()
	now := time.Now()
	seed := now.UnixNano()
	t.Logf("random seed for sample data initialized to %d", seed)
	return &Repo{
		t:             t,
		rng:           rand.New(rand.NewSource(seed)), //#nosec G404 intentionally reproducible
		desc:          map[digest.Digest]types.Descriptor{},
		blobs:         map[digest.Digest][]byte{},
		manifests:     map[digest.Digest][]byte{},
		manifestOrder: []digest.Digest{},
		referrers:     map[digest.Digest][]types.Descriptor{},
		tags:          map[string]digest.Digest{},
	}
}

// Copy duplicates a [Repo].
func (r *Repo) Copy() *Repo {
	referrers := map[digest.Digest][]types.Descriptor{}
	for dig, list := range r.referrers {
		referrers[dig] = slices.Clone(list)
	}
	return &Repo{
		t:             r.t,
		rng:           r.rng,
		desc:          maps.Clone(r.desc),
		blobs:         maps.Clone(r.blobs),
		manifests:     maps.Clone(r.manifests),
		manifestOrder: slices.Clone(r.manifestOrder),
		referrers:     referrers,
		tags:          maps.Clone(r.tags),
	}
}

// Blob returns the descriptor and bytes for a given blob, or false if the blob does not exist.
func (r *Repo) Blob(dig digest.Digest) (types.Descriptor, []byte, bool) {
	desc, ok1 := r.desc[dig]
	b, ok2 := r.blobs[dig]
	return desc, b, ok1 && ok2
}

// Desc returns the descriptor for a digest, or false if the descriptor was not found.
func (r *Repo) Desc(dig digest.Digest) (types.Descriptor, bool) {
	desc, ok := r.desc[dig]
	return desc.Copy(), ok
}

// Manifest returns the descriptor and bytes for a given manifest, or false if the manifest does not exist.
func (r *Repo) Manifest(dig digest.Digest) (types.Descriptor, []byte, bool) {
	desc, ok1 := r.desc[dig]
	b, ok2 := r.manifests[dig]
	return desc, b, ok1 && ok2
}

// Referrers returns the descriptors with a subject set to the digest.
func (r *Repo) Referrers(dig digest.Digest) []types.Descriptor {
	if list, ok := r.referrers[dig]; ok {
		return slices.Clone(list)
	}
	return nil
}

// Tag returns the digest pointed to by a given tag, or false if the tag does not exist.
func (r *Repo) Tag(t string) (digest.Digest, bool) {
	dig, ok := r.tags[t]
	return dig, ok
}

// Tags returns a map of all tags to their digest.
func (r *Repo) Tags() map[string]digest.Digest {
	return maps.Clone(r.tags)
}

// GenIndex creates a new index based on [OptsIndex] and stores it in the repo.
func (r *Repo) GenIndex(opts OptsIndex) (digest.Digest, error) {
	if opts.DigAlgo.IsZero() {
		opts.DigAlgo = digest.Canonical
	}
	if len(opts.ImageDesc) == 0 && len(opts.ImageOpts) == 0 {
		opts.ImageOpts = []OptsImage{
			{Platform: types.Platform{OS: "linux", Architecture: "amd64"}},
			{Platform: types.Platform{OS: "linux", Architecture: "arm64"}},
		}
	}
	if len(opts.ImageDesc) == 0 {
		for _, imageOpt := range opts.ImageOpts {
			if imageOpt.DigAlgo.IsZero() {
				imageOpt.DigAlgo = opts.DigAlgo
			}
			if imageOpt.Platform.OS == "" || imageOpt.Platform.Architecture == "" {
				imageOpt.Platform.OS = "linux"
				imageOpt.Platform.Architecture = "amd64"
			}
			manDig, err := r.GenImage(imageOpt)
			if err != nil {
				return digest.Digest{}, err
			}
			manDesc, ok := r.Desc(manDig)
			if !ok {
				return digest.Digest{}, fmt.Errorf("GenImage did not return a valid digest")
			}
			manDesc.Platform = &imageOpt.Platform
			manDesc.Annotations = imageOpt.Annotations
			opts.ImageDesc = append(opts.ImageDesc, manDesc)
		}
	}
	ind := types.Index{
		SchemaVersion: 2,
		MediaType:     types.MediaTypeOCI1ManifestList,
		ArtifactType:  opts.ArtifactType,
		Manifests:     opts.ImageDesc,
		Annotations:   opts.Annotations,
	}
	if !opts.Subject.Digest.IsZero() {
		ind.Subject = &opts.Subject
	}
	indJSON, err := json.Marshal(ind)
	if err != nil {
		return digest.Digest{}, err
	}
	indDig, err := digest.Canonical.FromBytes(indJSON)
	if err != nil {
		return digest.Digest{}, err
	}
	r.manifests[indDig] = indJSON
	r.manifestOrder = append(r.manifestOrder, indDig)
	r.desc[indDig] = types.Descriptor{
		MediaType: types.MediaTypeOCI1ManifestList,
		Digest:    indDig,
		Size:      int64(len(indJSON)),
	}
	for _, t := range opts.Tags {
		r.tags[t] = indDig
	}
	if !opts.Subject.Digest.IsZero() {
		subjDesc, respDesc, err := types.ManifestReferrerDescriptor(indJSON, r.desc[indDig])
		if err != nil {
			return digest.Digest{}, fmt.Errorf("failed to compute referrers response: %v", err)
		}
		r.referrers[subjDesc.Digest] = append(r.referrers[subjDesc.Digest], respDesc)
	}
	return indDig, nil
}

// GenImage creates a new image based on [OptsImage] and stores it in the repo.
func (r *Repo) GenImage(opts OptsImage) (digest.Digest, error) {
	if opts.DigAlgo.IsZero() {
		opts.DigAlgo = digest.Canonical
	}
	if opts.Platform.OS == "" || opts.Platform.Architecture == "" {
		opts.Platform = types.Platform{
			OS:           "linux",
			Architecture: "amd64",
		}
	}

	if len(opts.LayerDesc) == 0 || opts.ConfDesc.Digest.IsZero() {
		if len(opts.LayerOpts) == 0 {
			opts.LayerOpts = make([]OptsLayer, 2)
		}
		now := time.Now()
		conf := minImage{
			Create:   &now,
			Platform: opts.Platform,
			Config: minConfig{
				User:       "root",
				Env:        []string{"PATH=/"},
				Entrypoint: []string{"/entrypoint.sh"},
				Cmd:        []string{"hello", "world"},
				WorkingDir: "/",
				Labels: map[string]string{
					"config-type": "test",
				},
			},
			RootFS: minRootFS{
				Type:    "layers",
				DiffIDs: make([]digest.Digest, len(opts.LayerOpts)),
			},
		}
		opts.LayerDesc = make([]types.Descriptor, len(opts.LayerOpts))
		for l, layerOpt := range opts.LayerOpts {
			if layerOpt.MediaType == "" {
				layerOpt.MediaType = types.MediaTypeOCI1LayerGzip
			}
			if layerOpt.DigAlgo.IsZero() {
				layerOpt.DigAlgo = opts.DigAlgo
			}
			digOrig, digComp, err := r.genLayer(layerOpt)
			if err != nil {
				return digest.Digest{}, err
			}
			conf.RootFS.DiffIDs[l] = digOrig
			layerDesc, ok := r.Desc(digComp)
			if !ok {
				return digest.Digest{}, fmt.Errorf("GenLayer did not return a valid compressed digest")
			}
			layerDesc.URLs = layerOpt.ForeignURLs
			opts.LayerDesc[l] = layerDesc
		}
		confJSON, err := json.Marshal(conf)
		if err != nil {
			return digest.Digest{}, err
		}
		confDig, err := opts.DigAlgo.FromBytes(confJSON)
		if err != nil {
			return digest.Digest{}, err
		}
		r.blobs[confDig] = confJSON
		r.desc[confDig] = types.Descriptor{
			MediaType: types.MediaTypeOCI1ImageConfig,
			Size:      int64(len(confJSON)),
			Digest:    confDig,
		}
		opts.ConfDesc = r.desc[confDig]
	}

	man := types.Manifest{
		SchemaVersion: 2,
		MediaType:     types.MediaTypeOCI1Manifest,
		ArtifactType:  opts.ArtifactType,
		Config:        opts.ConfDesc,
		Layers:        opts.LayerDesc,
		Annotations:   opts.Annotations,
	}
	if !opts.Subject.Digest.IsZero() {
		man.Subject = &opts.Subject
	}
	manJSON, err := json.Marshal(man)
	if err != nil {
		return digest.Digest{}, err
	}
	manDig, err := opts.DigAlgo.FromBytes(manJSON)
	if err != nil {
		return digest.Digest{}, err
	}
	r.manifests[manDig] = manJSON
	r.manifestOrder = append(r.manifestOrder, manDig)
	r.desc[manDig] = types.Descriptor{
		MediaType: types.MediaTypeOCI1Manifest,
		Digest:    manDig,
		Size:      int64(len(manJSON)),
	}
	for _, t := range opts.Tags {
		r.tags[t] = manDig
	}
	if !opts.Subject.Digest.IsZero() {
		subjDesc, respDesc, err := types.ManifestReferrerDescriptor(manJSON, r.desc[manDig])
		if err != nil {
			return digest.Digest{}, fmt.Errorf("failed to compute referrers response: %v", err)
		}
		r.referrers[subjDesc.Digest] = append(r.referrers[subjDesc.Digest], respDesc)
	}
	return manDig, nil
}

// genLayer creates a new layer blob based on [OptsLayer] and stores it in the repo.
// The two digests returned are the uncompressed followed by the compressed blob digest.
func (r *Repo) genLayer(opts OptsLayer) (digest.Digest, digest.Digest, error) {
	// init unset opts
	if r.rng == nil {
		r.rng = rand.New(rand.NewSource(time.Now().UnixNano())) //#nosec G404 intentionally reproducible
	}
	if opts.Size <= 0 {
		opts.Size = 10240
	}
	if opts.DigAlgo.IsZero() {
		opts.DigAlgo = digest.Canonical
	}
	if opts.CompFn == nil {
		opts.CompFn = func(w io.Writer) io.WriteCloser { return gzip.NewWriter(w) }
	}

	layerOrigBytes := make([]byte, opts.Size)
	if _, err := r.rng.Read(layerOrigBytes); err != nil {
		return digest.Digest{}, digest.Digest{}, err
	}
	var layerCompBuf bytes.Buffer
	compW := opts.CompFn(&layerCompBuf)
	if _, err := compW.Write(layerOrigBytes); err != nil {
		return digest.Digest{}, digest.Digest{}, err
	}
	if err := compW.Close(); err != nil {
		return digest.Digest{}, digest.Digest{}, err
	}
	layerCompBytes := layerCompBuf.Bytes()
	digOrig, err := opts.DigAlgo.FromBytes(layerOrigBytes)
	if err != nil {
		return digest.Digest{}, digest.Digest{}, err
	}
	digComp, err := opts.DigAlgo.FromBytes(layerCompBytes)
	if err != nil {
		return digest.Digest{}, digest.Digest{}, err
	}
	if !opts.Foreign {
		r.blobs[digComp] = layerCompBytes
	}
	r.desc[digComp] = types.Descriptor{
		MediaType: opts.MediaType,
		Digest:    digComp,
		Size:      int64(len(layerCompBytes)),
	}
	return digOrig, digComp, nil
}

// WalkBlobs iterates through all of the blobs in the repo.
func (r *Repo) WalkBlobs() iter.Seq[Step] {
	return func(yield func(Step) bool) {
		for dig, b := range r.blobs {
			desc, ok := r.Desc(dig)
			if !ok {
				continue
			}
			if !yield(Step{Desc: desc, Bytes: b}) {
				return
			}
		}
	}
}

// WalkManifests iterates through all of the manifests in the repo, always yielding a child manifest before its parent.
func (r *Repo) WalkManifests() iter.Seq[Step] {
	tags := map[digest.Digest][]string{}
	for t, dig := range r.tags {
		tags[dig] = append(tags[dig], t)
	}
	return func(yield func(Step) bool) {
		for _, dig := range r.manifestOrder {
			desc, ok := r.Desc(dig)
			if !ok {
				continue
			}
			b, ok := r.manifests[dig]
			if !ok {
				continue
			}
			if !yield(Step{Desc: desc, Bytes: b, Tags: tags[dig]}) {
				return
			}
		}
	}
}
