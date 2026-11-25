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

package types

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	digest "github.com/sudo-bmitch/oci-digest"
)

func TestIndexCopy(t *testing.T) {
	t.Parallel()
	dig1, err := digest.FromString("digest 1")
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	dig2, err := digest.FromString("digest 2")
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	dig3, err := digest.FromString("digest 3")
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	subj, err := digest.FromString("subject")
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	then := time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
	orig := Index{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCI1ManifestList,
		ArtifactType:  "application/example.artifact",
		Manifests: []Descriptor{
			{
				MediaType: MediaTypeOCI1Manifest,
				Digest:    dig1,
				Size:      42,
				URLs:      []string{"http://example.org/dig1"},
				Annotations: map[string]string{
					AnnotCreated: now,
				},
				Data: []byte("digest 1"),
				Platform: &Platform{
					OS:           "linux",
					Architecture: "amd64",
				},
				ArtifactType: "application/example.artifact",
			},
			{
				MediaType: MediaTypeOCI1Manifest,
				Digest:    dig2,
				Size:      42,
				URLs:      []string{"http://example.org/dig2"},
				Annotations: map[string]string{
					AnnotCreated: now,
				},
				Data: []byte("digest 2"),
				Platform: &Platform{
					OS:           "linux",
					Architecture: "arm64",
				},
				ArtifactType: "application/example.artifact",
			},
			{
				MediaType: MediaTypeOCI1Manifest,
				Digest:    dig3,
				Size:      42,
				URLs:      []string{"http://example.org/dig3"},
				Annotations: map[string]string{
					AnnotCreated: now,
				},
				Data: []byte("digest 3"),
				Platform: &Platform{
					OS:           "linux",
					Architecture: "arm",
					Variant:      "v7",
				},
				ArtifactType: "application/example.artifact",
			},
		},
		Subject: &Descriptor{
			MediaType: MediaTypeOCI1Manifest,
			Digest:    subj,
			Size:      42,
		},
		Annotations: map[string]string{
			AnnotCreated: now,
			AnnotRefName: "index",
		},
	}
	copy := orig.Copy()
	if copy.SchemaVersion != orig.SchemaVersion || copy.MediaType != orig.MediaType || copy.ArtifactType != orig.ArtifactType ||
		!copy.Subject.Equal(*orig.Subject) ||
		!maps.Equal(copy.Annotations, orig.Annotations) ||
		len(copy.Manifests) != len(orig.Manifests) {
		t.Errorf("copy does not match orig, expected %v, received %v", copy, orig)
	}
	for ind, cd := range copy.Manifests {
		if !cd.Equal(orig.Manifests[ind]) {
			t.Errorf("copy descriptor %d does not match orig, expected %v, received %v", ind, cd, orig.Manifests[ind])
		}
	}
	copy.Annotations[AnnotCreated] = then
	copy.Subject.Size = 67
	copy.Manifests[0].URLs[0] = "http://copy.example.org/dig1"
	copy.Manifests[0].Annotations[AnnotCreated] = then
	if orig.Annotations[AnnotCreated] != now || orig.Subject.Size != 42 ||
		orig.Manifests[0].URLs[0] != "http://example.org/dig1" ||
		orig.Manifests[0].Annotations[AnnotCreated] != now {
		t.Errorf("orig changed after updates to copy: %v", orig)
	}
}

func TestManifestPaginate(t *testing.T) {
	t.Parallel()
	dig, err := digest.FromString("artifact")
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	bigIndex := Index{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCI1ManifestList,
		ArtifactType:  "application/example.artifact",
		Manifests:     []Descriptor{},
		Annotations: map[string]string{
			AnnotCreated:     now,
			"org.olareg.big": strings.Repeat("A", 1024),
		},
	}
	for i := range 1000 {
		d := Descriptor{
			MediaType: MediaTypeOCI1Manifest,
			Digest:    dig,
			Size:      42,
			Data:      []byte("artifact"),
			Annotations: map[string]string{
				AnnotCreated:       now,
				"org.olareg.index": fmt.Sprintf("%d", i),
			},
		}
		if i%10 == 0 {
			// every 10, starting at 0, add a large chunk to make it easy to guess offsets in test cases and to test the skip code
			d.Annotations["org.olareg.big"] = strings.Repeat("A", 10240)
		}
		bigIndex.Manifests = append(bigIndex.Manifests, d)
	}
	tt := []struct {
		name   string
		limit  int64
		offset int
		err    error
	}{
		{
			name:   "zero-limit",
			limit:  1024,
			offset: 0, // not possible to pack any single descriptor
			err:    ErrPaginationSkippedDescriptor,
		},
		{
			name:   "skip-first",
			limit:  1024 * 5,
			offset: 10, // 1-9, skipping 0
			err:    ErrPaginationSkippedDescriptor,
		},
		{
			name:   "first-ten",
			limit:  10240 + 1024*5,
			offset: 10, // 0-9
		},
		{
			name:   "first-twenty",
			limit:  10240*2 + 1024*7,
			offset: 20, // 0-19
		},
		{
			name:   "full-list",
			limit:  10240 * 150,
			offset: -1, // all entries returned
		},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			out, offset, err := bigIndex.Paginate(tc.limit)
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Errorf("expected error %v, received %v", tc.err, err)
				}
			} else if err != nil {
				t.Errorf("received unexpected error: %v", err)
			}
			if offset != tc.offset {
				t.Errorf("expected offset %d, received %d\n%s", tc.offset, offset, out)
			}
		})
	}
}

func TestManifestParse(t *testing.T) {
	t.Parallel()
	emptyDig, err := digest.FromString("{}")
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	emptyDesc := Descriptor{
		MediaType: MediaTypeOCI1Empty,
		Digest:    emptyDig,
		Size:      2,
		Data:      []byte("{}"),
	}
	digSubj, err := digest.FromString("subject")
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	descSubj := Descriptor{
		MediaType: MediaTypeOCI1ManifestList,
		Digest:    digSubj,
		Size:      7,
	}
	digConfig, err := digest.FromString("config")
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	descConfig := Descriptor{
		MediaType: MediaTypeOCI1ImageConfig,
		Digest:    digConfig,
		Size:      42,
	}
	digLayer1, err := digest.FromString("layer 1")
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	descLayer1 := Descriptor{
		MediaType: MediaTypeOCI1Layer,
		Digest:    digLayer1,
		Size:      42,
	}
	digLayer2, err := digest.FromString("layer 2")
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	descLayer2 := Descriptor{
		MediaType: MediaTypeOCI1Layer,
		Digest:    digLayer2,
		Size:      42,
	}
	man1Subj, err := json.Marshal(Manifest{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCI1Manifest,
		ArtifactType:  "application/example.artifact",
		Config:        emptyDesc,
		Layers:        []Descriptor{},
		Annotations: map[string]string{
			"org.example.annot": "example",
		},
		Subject: &descSubj,
	})
	if err != nil {
		t.Fatalf("failed to marshal JSON: %v", err)
	}
	man1Dig, err := digest.FromBytes(man1Subj)
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	man1Desc := Descriptor{
		MediaType: MediaTypeOCI1Manifest,
		Digest:    man1Dig,
		Size:      int64(len(man1Subj)),
	}
	man1DescSubj := Descriptor{
		MediaType:    MediaTypeOCI1Manifest,
		Digest:       man1Dig,
		Size:         int64(len(man1Subj)),
		ArtifactType: "application/example.artifact",
		Annotations: map[string]string{
			"org.example.annot": "example",
		},
	}
	man2Subj, err := json.Marshal(Manifest{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCI1Manifest,
		Config:        descConfig,
		Layers:        []Descriptor{descLayer1, descLayer2},
		Subject:       &descSubj,
	})
	if err != nil {
		t.Fatalf("failed to marshal JSON: %v", err)
	}
	man2Dig, err := digest.FromBytes(man2Subj)
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	man2Desc := Descriptor{
		MediaType: MediaTypeOCI1Manifest,
		Digest:    man2Dig,
		Size:      int64(len(man2Subj)),
	}
	man2DescSubj := Descriptor{
		MediaType:    MediaTypeOCI1Manifest,
		Digest:       man2Dig,
		Size:         int64(len(man2Subj)),
		ArtifactType: MediaTypeOCI1ImageConfig,
	}
	man3NoSubj, err := json.Marshal(Manifest{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCI1Manifest,
		Config:        descConfig,
		Layers:        []Descriptor{descLayer1, descLayer2},
	})
	if err != nil {
		t.Fatalf("failed to marshal JSON: %v", err)
	}
	man3Dig, err := digest.FromBytes(man3NoSubj)
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	man3Desc := Descriptor{
		MediaType: MediaTypeOCI1Manifest,
		Digest:    man3Dig,
		Size:      int64(len(man3NoSubj)),
	}
	man4Docker, err := json.Marshal(Manifest{
		SchemaVersion: 2,
		MediaType:     MediaTypeDocker2Manifest,
		Config:        descConfig,
		Layers:        []Descriptor{descLayer1, descLayer2},
	})
	if err != nil {
		t.Fatalf("failed to marshal JSON: %v", err)
	}
	man4Dig, err := digest.FromBytes(man4Docker)
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	man4Desc := Descriptor{
		MediaType: MediaTypeDocker2Manifest,
		Digest:    man4Dig,
		Size:      int64(len(man4Docker)),
	}
	ind1Subj, err := json.Marshal(Index{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCI1ManifestList,
		ArtifactType:  "application/example.artifact",
		Subject:       &descSubj,
		Annotations: map[string]string{
			"org.example.annot": "example",
		},
	})
	if err != nil {
		t.Fatalf("failed to marshal JSON: %v", err)
	}
	ind1Dig, err := digest.FromBytes(ind1Subj)
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	ind1Desc := Descriptor{
		MediaType: MediaTypeOCI1ManifestList,
		Digest:    ind1Dig,
		Size:      int64(len(ind1Subj)),
	}
	ind1DescSubj := Descriptor{
		MediaType:    MediaTypeOCI1ManifestList,
		Digest:       ind1Dig,
		Size:         int64(len(ind1Subj)),
		ArtifactType: "application/example.artifact",
		Annotations: map[string]string{
			"org.example.annot": "example",
		},
	}
	ind2NoSubj, err := json.Marshal(Index{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCI1ManifestList,
		Manifests:     []Descriptor{man1Desc, man2Desc, man3Desc},
	})
	if err != nil {
		t.Fatalf("failed to marshal JSON: %v", err)
	}
	ind2Dig, err := digest.FromBytes(ind2NoSubj)
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	ind2Desc := Descriptor{
		MediaType: MediaTypeOCI1ManifestList,
		Digest:    ind2Dig,
		Size:      int64(len(ind2NoSubj)),
	}
	ind3Docker, err := json.Marshal(Index{
		SchemaVersion: 2,
		MediaType:     MediaTypeDocker2ManifestList,
		Manifests:     []Descriptor{man1Desc, man2Desc, man3Desc},
	})
	if err != nil {
		t.Fatalf("failed to marshal JSON: %v", err)
	}
	ind3Dig, err := digest.FromBytes(ind3Docker)
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	ind3Desc := Descriptor{
		MediaType: MediaTypeOCI1ManifestList,
		Digest:    ind3Dig,
		Size:      int64(len(ind3Docker)),
	}
	t.Run("ReferrerDescriptor", func(t *testing.T) {
		tt := []struct {
			name         string
			inBytes      []byte
			inDesc       Descriptor
			expSubj      Descriptor
			expDescEntry Descriptor
			expErr       error
		}{
			{
				name:         "manifest1-artifact-with-subject",
				inBytes:      man1Subj,
				inDesc:       man1Desc,
				expSubj:      descSubj,
				expDescEntry: man1DescSubj,
			},
			{
				name:         "manifest2-subject",
				inBytes:      man2Subj,
				inDesc:       man2Desc,
				expSubj:      descSubj,
				expDescEntry: man2DescSubj,
			},
			{
				name:    "manifest3-no-subject",
				inBytes: man3NoSubj,
				inDesc:  man3Desc,
				expErr:  ErrNotFound,
			},
			{
				name:         "index1-artifact-subject",
				inBytes:      ind1Subj,
				inDesc:       ind1Desc,
				expSubj:      descSubj,
				expDescEntry: ind1DescSubj,
			},
			{
				name:    "index2-no-subject",
				inBytes: ind2NoSubj,
				inDesc:  ind2Desc,
				expErr:  ErrNotFound,
			},
			{
				name:    "parse-error",
				inBytes: man1Subj[:100],
				inDesc:  man1Desc,
				expErr:  ErrParsingFailed,
			},
		}
		for _, tc := range tt {
			t.Run(tc.name, func(t *testing.T) {
				descSubj, descEntry, err := ManifestReferrerDescriptor(tc.inBytes, tc.inDesc)
				if tc.expErr != nil {
					if !errors.Is(err, tc.expErr) {
						t.Errorf("expected error %v, received %v", tc.expErr, err)
					}
					return
				}
				if err != nil {
					t.Fatalf("unexpected error %v", err)
				}
				if !descSubj.Equal(tc.expSubj) {
					t.Errorf("expected subject %v, received %v", tc.expSubj, descSubj)
				}
				if !descEntry.Equal(tc.expDescEntry) {
					t.Errorf("expected entry %v, received %v", tc.expDescEntry, descEntry)
				}
			})
		}
	})
	t.Run("ParseDescriptors", func(t *testing.T) {
		tt := []struct {
			name        string
			inBytes     []byte
			inDesc      Descriptor
			inDepth     ManifestParseDepth
			expDescList []Descriptor
			expErr      error
		}{
			{
				name:        "index1-none",
				inBytes:     ind1Subj,
				inDesc:      ind1Desc,
				inDepth:     ParseNone,
				expDescList: []Descriptor{},
			},
			{
				name:        "manifest1-manifests",
				inBytes:     man1Subj,
				inDesc:      man1Desc,
				inDepth:     ParseManifests,
				expDescList: []Descriptor{},
			},
			{
				name:        "manifest1-blobs",
				inBytes:     man1Subj,
				inDesc:      man1Desc,
				inDepth:     ParseBlobs,
				expDescList: []Descriptor{emptyDesc},
			},
			{
				name:        "manifest2-blobs",
				inBytes:     man2Subj,
				inDesc:      man2Desc,
				inDepth:     ParseBlobs,
				expDescList: []Descriptor{descConfig, descLayer1, descLayer2},
			},
			{
				name:        "manifest3-blobs",
				inBytes:     man3NoSubj,
				inDesc:      man3Desc,
				inDepth:     ParseBlobs,
				expDescList: []Descriptor{descConfig, descLayer1, descLayer2},
			},
			{
				name:        "manifest4-docker-blobs",
				inBytes:     man4Docker,
				inDesc:      man4Desc,
				inDepth:     ParseBlobs,
				expDescList: []Descriptor{descConfig, descLayer1, descLayer2},
			},
			{
				name:        "index1-manifests",
				inBytes:     ind1Subj,
				inDesc:      ind1Desc,
				inDepth:     ParseManifests,
				expDescList: []Descriptor{},
			},
			{
				name:        "index2-blobs",
				inBytes:     ind2NoSubj,
				inDesc:      ind2Desc,
				inDepth:     ParseBlobs,
				expDescList: []Descriptor{man1Desc, man2Desc, man3Desc},
			},
			{
				name:        "index3-docker-manifests",
				inBytes:     ind3Docker,
				inDesc:      ind3Desc,
				inDepth:     ParseManifests,
				expDescList: []Descriptor{man1Desc, man2Desc, man3Desc},
			},
			{
				name:        "layer",
				inBytes:     []byte("unparsable bytes"),
				inDesc:      descLayer1,
				inDepth:     ParseBlobs,
				expDescList: []Descriptor{},
			},
			{
				name:    "manifest-parse-error",
				inBytes: man1Subj[:100],
				inDesc:  man1Desc,
				inDepth: ParseBlobs,
				expErr:  ErrParsingFailed,
			},
			{
				name:    "index-parse-error",
				inBytes: ind1Subj[:100],
				inDesc:  ind1Desc,
				inDepth: ParseBlobs,
				expErr:  ErrParsingFailed,
			},
		}
		for _, tc := range tt {
			t.Run(tc.name, func(t *testing.T) {
				result, err := ManifestParseDescriptors(tc.inBytes, tc.inDesc, tc.inDepth)
				if tc.expErr != nil {
					if !errors.Is(err, tc.expErr) {
						t.Errorf("expected error %v, received %v", tc.expErr, err)
					}
					return
				}
				if err != nil {
					t.Fatalf("unexpected error %v", err)
				}
				for _, d := range tc.expDescList {
					if !slices.ContainsFunc(result, func(cur Descriptor) bool { return cur.Equal(d) }) {
						t.Errorf("missing descriptor: %v", d)
					}
				}
				for _, d := range result {
					if !slices.ContainsFunc(tc.expDescList, func(cur Descriptor) bool { return cur.Equal(d) }) {
						t.Errorf("unexpected descriptor: %v", d)
					}
				}
			})
		}
	})
}
