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
	"strings"
	"testing"
	"time"

	digest "github.com/sudo-bmitch/oci-digest"
)

func TestLayoutCopy(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Format(time.RFC3339)
	then := time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
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
	desc1 := Descriptor{
		MediaType: MediaTypeOCI1ManifestList,
		Digest:    dig1,
		Size:      42,
		Annotations: map[string]string{
			AnnotCreated: now,
		},
	}
	desc2 := Descriptor{
		MediaType: MediaTypeOCI1Manifest,
		Digest:    dig2,
		Size:      42,
		Annotations: map[string]string{
			AnnotCreated: now,
		},
	}
	desc2Tag := desc2.Copy()
	desc2Tag.Annotations[AnnotRefName] = "tag"
	desc3 := Descriptor{
		MediaType: MediaTypeOCI1Manifest,
		Digest:    dig3,
		Size:      42,
	}
	orig := LayoutIndex{
		Index: Index{
			SchemaVersion: 2,
			MediaType:     MediaTypeOCI1ManifestList,
			Manifests:     []Descriptor{desc1, desc2},
			Annotations: map[string]string{
				"org.example.annot": "orig",
			},
		},
		OlaregExt: LayoutOlaregExt{
			ChildManifests: true,
			Referrers:      false,
			TagHistory:     false,
		},
	}
	copy := orig.Copy()
	if copy.SchemaVersion != 2 || copy.MediaType != MediaTypeOCI1ManifestList ||
		copy.Annotations["org.example.annot"] != "orig" || copy.ArtifactType != "" ||
		!copy.OlaregExt.ChildManifests || copy.OlaregExt.Referrers || copy.OlaregExt.TagHistory ||
		len(copy.Manifests) != 2 || !copy.Manifests[0].Equal(desc1) || !copy.Manifests[1].Equal(desc2) {
		t.Errorf("copy does not match original: %v", copy)
	}
	// insert some entries and verify orig is unchanged
	copy.Manifests[1].Annotations[AnnotCreated] = then
	copy.Manifests = append(copy.Manifests, desc2Tag, desc3)
	copy.Annotations["org.example.annot"] = "copy"
	copy.OlaregExt.Referrers = true
	copy.OlaregExt.TagHistory = true
	if len(orig.Manifests) != 2 || orig.Manifests[1].Annotations[AnnotCreated] != now ||
		orig.Annotations["org.example.annot"] != "orig" ||
		orig.OlaregExt.Referrers || orig.OlaregExt.TagHistory {
		t.Errorf("orig is changed after modifying copy: %v", orig)
	}
}

func TestLayoutVerify(t *testing.T) {
	t.Parallel()
	tt := []struct {
		name  string
		text  string
		valid bool
	}{
		{
			name:  "valid-compact",
			text:  `{"imageLayoutVersion":"1.0.0"}`,
			valid: true,
		},
		{
			name:  "valid-multi-line",
			text:  "{\n" + `  "imageLayoutVersion": "1.0.0"` + "\n}\n",
			valid: true,
		},
		{
			name: "invalid-truncated",
			text: `{"imageLayoutVersion":"1.0.0`,
		},
		{
			name: "invalid-version",
			text: `{"imageLayoutVersion":"2.0.0"}`,
		},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			err := LayoutVerify(strings.NewReader(tc.text))
			if tc.valid && err != nil {
				t.Errorf("expected valid layout, received %v", err)
			}
			if !tc.valid && err == nil {
				t.Errorf("verify did not fail")
			}
		})
	}
}
