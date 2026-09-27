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
	"testing"
	"time"

	digest "github.com/sudo-bmitch/oci-digest"
)

func TestDescriptorBool(t *testing.T) {
	t.Parallel()
	dig, err := digest.FromString("test")
	if err != nil {
		t.Fatalf("digest creation failed: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	tt := []struct {
		name       string
		d          Descriptor
		isCustom   bool
		isEmpty    bool
		isInternal bool
	}{
		{
			name: "nil",
			d: Descriptor{
				MediaType: MediaTypeOCI1ManifestList,
				Digest:    dig,
				Size:      42,
			},
			isCustom:   false,
			isEmpty:    true,
			isInternal: false,
		},
		{
			name: "empty",
			d: Descriptor{
				MediaType:   MediaTypeOCI1ManifestList,
				Digest:      dig,
				Size:        42,
				Annotations: map[string]string{},
			},
			isCustom:   false,
			isEmpty:    true,
			isInternal: false,
		},
		{
			name: "created",
			d: Descriptor{
				MediaType: MediaTypeOCI1ManifestList,
				Digest:    dig,
				Size:      42,
				Annotations: map[string]string{
					AnnotCreated: now,
				},
			},
			isCustom:   false,
			isEmpty:    true,
			isInternal: false,
		},
		{
			name: "tagged",
			d: Descriptor{
				MediaType: MediaTypeOCI1ManifestList,
				Digest:    dig,
				Size:      42,
				Annotations: map[string]string{
					AnnotCreated: now,
					AnnotRefName: "tag",
				},
			},
			isCustom:   false,
			isEmpty:    false,
			isInternal: false,
		},
		{
			name: "custom",
			d: Descriptor{
				MediaType: MediaTypeOCI1ManifestList,
				Digest:    dig,
				Size:      42,
				Annotations: map[string]string{
					AnnotCreated:             now,
					"org.example.custom.key": "value",
				},
			},
			isCustom:   true,
			isEmpty:    false,
			isInternal: false,
		},
		{
			name: "referrer",
			d: Descriptor{
				MediaType: MediaTypeOCI1ManifestList,
				Digest:    dig,
				Size:      42,
				Annotations: map[string]string{
					AnnotCreated:         now,
					AnnotReferrerSubject: dig.String(),
				},
			},
			isCustom:   false,
			isEmpty:    false,
			isInternal: true,
		},
		{
			name: "tag-history",
			d: Descriptor{
				MediaType: MediaTypeOCI1ManifestList,
				Digest:    dig,
				Size:      42,
				Annotations: map[string]string{
					AnnotCreated:    now,
					AnnotTagHistory: "tag",
				},
			},
			isCustom:   false,
			isEmpty:    false,
			isInternal: true,
		},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			if tc.d.AnnotationsCustom() != tc.isCustom {
				t.Errorf("AnnotationsCustom expected %t, received %t", tc.isCustom, tc.d.AnnotationsCustom())
			}
			if tc.d.AnnotationsEmpty() != tc.isEmpty {
				t.Errorf("AnnotationsEmpty expected %t, received %t", tc.isEmpty, tc.d.AnnotationsEmpty())
			}
			if tc.d.AnnotationsInternal() != tc.isInternal {
				t.Errorf("AnnotationsInternal expected %t, received %t", tc.isInternal, tc.d.AnnotationsInternal())
			}
		})
	}
}

func TestDescriptorAnnotationVal(t *testing.T) {
	t.Parallel()
	dig, err := digest.FromString("test")
	if err != nil {
		t.Fatalf("digest creation failed: %v", err)
	}
	tt := []struct {
		name     string
		d        Descriptor
		key, val string
		ok       bool
	}{
		{
			name: "nil",
			d: Descriptor{
				MediaType: MediaTypeOCI1ManifestList,
				Digest:    dig,
				Size:      42,
			},
			key: "key",
			ok:  false,
		},
		{
			name: "empty",
			d: Descriptor{
				MediaType:   MediaTypeOCI1ManifestList,
				Digest:      dig,
				Size:        42,
				Annotations: map[string]string{},
			},
			key: "key",
			ok:  false,
		},
		{
			name: "set",
			d: Descriptor{
				MediaType: MediaTypeOCI1ManifestList,
				Digest:    dig,
				Size:      42,
				Annotations: map[string]string{
					"key": "val",
				},
			},
			key: "key",
			val: "val",
			ok:  true,
		},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			val, ok := tc.d.AnnotationVal(tc.key)
			if ok != tc.ok {
				t.Errorf("AnnotationVal ok expected %t, received %t", tc.ok, ok)
			}
			if !tc.ok {
				return
			}
			if val != tc.val {
				t.Errorf("AnnotationVal expected %s, received %s", tc.val, val)
			}
		})
	}
}

func TestDescriptorCmp(t *testing.T) {
	t.Parallel()
	dig1, err := digest.FromString("test1")
	if err != nil {
		t.Fatalf("digest creation failed: %v", err)
	}
	dig2, err := digest.FromString("test2")
	if err != nil {
		t.Fatalf("digest creation failed: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	then := time.Now().Add(time.Second).UTC().Format(time.RFC3339)
	tt := []struct {
		name    string
		a, b    Descriptor
		isSame  bool
		isEqual bool
	}{
		{
			name: "equal-minimal",
			a: Descriptor{
				Digest:    dig1,
				MediaType: MediaTypeOCI1ManifestList,
				Size:      42,
			},
			b: Descriptor{
				Digest:    dig1,
				MediaType: MediaTypeOCI1ManifestList,
				Size:      42,
			},
			isSame:  true,
			isEqual: true,
		},
		{
			name: "equal-complex",
			a: Descriptor{
				Digest:    dig1,
				MediaType: MediaTypeOCI1ManifestList,
				Size:      42,
				Platform: &Platform{
					OS:           "linux",
					Architecture: "amd64",
					OSFeatures:   []string{"os-feature1", "os-feature2"},
					Features:     []string{"feature1", "feature2"},
				},
				URLs: []string{
					"https://example.com/manifest",
					"https://backup.example.com/manifest",
				},
				Annotations: map[string]string{
					AnnotCreated:      now,
					AnnotRefName:      "tag1",
					"org.example.key": "val",
				},
				Data:         []byte("test1"),
				ArtifactType: "application/example.artifact",
			},
			b: Descriptor{
				Digest:    dig1,
				MediaType: MediaTypeOCI1ManifestList,
				Size:      42,
				Platform: &Platform{
					OS:           "linux",
					Architecture: "amd64",
					OSFeatures:   []string{"os-feature1", "os-feature2"},
					Features:     []string{"feature1", "feature2"},
				},
				URLs: []string{
					"https://example.com/manifest",
					"https://backup.example.com/manifest",
				},
				Annotations: map[string]string{
					AnnotCreated:      now,
					AnnotRefName:      "tag1",
					"org.example.key": "val",
				},
				Data:         []byte("test1"),
				ArtifactType: "application/example.artifact",
			},
			isSame:  true,
			isEqual: true,
		},
		{
			name: "diff-digest",
			a: Descriptor{
				Digest:    dig1,
				MediaType: MediaTypeOCI1ManifestList,
				Size:      42,
			},
			b: Descriptor{
				Digest:    dig2,
				MediaType: MediaTypeOCI1ManifestList,
				Size:      42,
			},
			isSame:  false,
			isEqual: false,
		},
		{
			name: "diff-timestamp",
			a: Descriptor{
				Digest:    dig1,
				MediaType: MediaTypeOCI1ManifestList,
				Size:      42,
				Annotations: map[string]string{
					AnnotCreated: now,
				},
			},
			b: Descriptor{
				Digest:    dig1,
				MediaType: MediaTypeOCI1ManifestList,
				Size:      42,
				Annotations: map[string]string{
					AnnotCreated: then,
				},
			},
			isSame:  true,
			isEqual: false,
		},
		{
			name: "missing-platform",
			a: Descriptor{
				Digest:    dig1,
				MediaType: MediaTypeOCI1ManifestList,
				Size:      42,
				Platform: &Platform{
					OS:           "linux",
					Architecture: "amd64",
				},
			},
			b: Descriptor{
				Digest:    dig1,
				MediaType: MediaTypeOCI1ManifestList,
				Size:      42,
			},
			isSame:  false,
			isEqual: false,
		},
		{
			name: "diff-data",
			a: Descriptor{
				Digest:    dig1,
				MediaType: MediaTypeOCI1ManifestList,
				Size:      42,
				Platform: &Platform{
					OS:           "linux",
					Architecture: "amd64",
					OSFeatures:   []string{"os-feature1", "os-feature2"},
					Features:     []string{"feature1", "feature2"},
				},
				URLs: []string{
					"https://example.com/manifest",
					"https://backup.example.com/manifest",
				},
				Annotations: map[string]string{
					AnnotCreated:      now,
					AnnotRefName:      "tag1",
					"org.example.key": "val",
				},
				Data:         []byte("test1"),
				ArtifactType: "application/example.artifact",
			},
			b: Descriptor{
				Digest:    dig1,
				MediaType: MediaTypeOCI1ManifestList,
				Size:      42,
				Platform: &Platform{
					OS:           "linux",
					Architecture: "amd64",
					OSFeatures:   []string{"os-feature1", "os-feature2"},
					Features:     []string{"feature1", "feature2"},
				},
				URLs: []string{
					"https://example.com/manifest",
					"https://backup.example.com/manifest",
				},
				Annotations: map[string]string{
					AnnotCreated:      now,
					AnnotRefName:      "tag1",
					"org.example.key": "val",
				},
				Data:         []byte("test2"),
				ArtifactType: "application/example.artifact",
			},
			isSame:  false,
			isEqual: false,
		},
		{
			name: "diff-url",
			a: Descriptor{
				Digest:    dig1,
				MediaType: MediaTypeOCI1ManifestList,
				Size:      42,
				Platform: &Platform{
					OS:           "linux",
					Architecture: "amd64",
					OSFeatures:   []string{"os-feature1", "os-feature2"},
					Features:     []string{"feature1", "feature2"},
				},
				URLs: []string{
					"https://example.com/manifest",
					"https://backup.example.com/manifest",
				},
				Annotations: map[string]string{
					AnnotCreated:      now,
					AnnotRefName:      "tag1",
					"org.example.key": "val",
				},
				Data:         []byte("test1"),
				ArtifactType: "application/example.artifact",
			},
			b: Descriptor{
				Digest:    dig1,
				MediaType: MediaTypeOCI1ManifestList,
				Size:      42,
				Platform: &Platform{
					OS:           "linux",
					Architecture: "amd64",
					OSFeatures:   []string{"os-feature1", "os-feature2"},
					Features:     []string{"feature1", "feature2"},
				},
				URLs: []string{
					"https://example.com/manifest2",
					"https://backup.example.com/manifest2",
				},
				Annotations: map[string]string{
					AnnotCreated:      now,
					AnnotRefName:      "tag1",
					"org.example.key": "val",
				},
				Data:         []byte("test1"),
				ArtifactType: "application/example.artifact",
			},
			isSame:  false,
			isEqual: false,
		},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			if result := tc.a.Equal(tc.b); result != tc.isEqual {
				t.Errorf("Equal expected %t, received %t", tc.isEqual, result)
			}
			if result := tc.a.Same(tc.b); result != tc.isSame {
				t.Errorf("Same expected %t, received %t", tc.isSame, result)
			}
		})
	}
}

func TestCopy(t *testing.T) {
	t.Parallel()
	dig, err := digest.FromString("test")
	if err != nil {
		t.Fatalf("digest creation failed: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	tt := []struct {
		name string
		d    Descriptor
	}{
		{
			name: "empty",
			d:    Descriptor{},
		},
		{
			name: "minimal",
			d: Descriptor{
				Digest:    dig,
				MediaType: MediaTypeOCI1ManifestList,
				Size:      4,
			},
		},
		{
			name: "full",
			d: Descriptor{
				Digest:    dig,
				MediaType: MediaTypeOCI1ManifestList,
				Size:      4,
				Platform: &Platform{
					OS:           "linux",
					Architecture: "amd64",
					OSFeatures:   []string{"os-feature1", "os-feature2"},
					Features:     []string{"feature1", "feature2"},
				},
				URLs: []string{
					"https://example.com/manifest",
					"https://backup.example.com/manifest",
				},
				Annotations: map[string]string{
					AnnotCreated:      now,
					AnnotRefName:      "tag1",
					"org.example.key": "val",
				},
				Data:         []byte("test"),
				ArtifactType: "application/example.artifact",
			},
		},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			copy := tc.d.Copy()
			if !tc.d.Equal(copy) {
				t.Errorf("copy does not equal input")
			}
		})
	}
	// ensure the copy does not share memory with the original
	d := Descriptor{
		Digest:    dig,
		MediaType: MediaTypeOCI1ManifestList,
		Size:      4,
		Platform: &Platform{
			OS:           "linux",
			Architecture: "amd64",
			OSFeatures:   []string{"os-feature1", "os-feature2"},
			Features:     []string{"feature1", "feature2"},
		},
		URLs: []string{
			"https://example.com/manifest",
			"https://backup.example.com/manifest",
		},
		Annotations: map[string]string{
			AnnotCreated:      now,
			AnnotRefName:      "tag1",
			"org.example.key": "val",
		},
		Data:         []byte("test"),
		ArtifactType: "application/example.artifact",
	}
	copy := d.Copy()
	copy.Annotations[AnnotRefName] = "tag2"
	copy.Platform.Architecture = "arm64"
	copy.URLs[0] = "https://copy.example.com/manifest"
	copy.Data[0] = 'A'
	if d.Annotations[AnnotRefName] == "tag2" {
		t.Errorf("annotation change affected original descriptor")
	}
	if d.Platform.Architecture == "arm64" {
		t.Errorf("platform change affected original descriptor")
	}
	if d.URLs[0] == "https://copy.example.com/manifest" {
		t.Errorf("url change affected original descriptor")
	}
	if d.Data[0] == 'A' {
		t.Errorf("data change affected original descriptor")
	}
}
