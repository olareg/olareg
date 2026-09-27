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

import "testing"

func TestMediaTypeBase(t *testing.T) {
	t.Parallel()
	tt := []struct {
		name   string
		orig   string
		expect string
	}{
		{
			name:   "OCI Index",
			orig:   MediaTypeOCI1ManifestList,
			expect: MediaTypeOCI1ManifestList,
		},
		{
			name:   "OCI Index with charset",
			orig:   "application/vnd.oci.image.index.v1+json; charset=utf-8",
			expect: MediaTypeOCI1ManifestList,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			result := MediaTypeBase(tc.orig)
			if tc.expect != result {
				t.Errorf("invalid result: expected \"%s\", received \"%s\"", tc.expect, result)
			}
		})
	}
}

func TestMediaTypeAccepts(t *testing.T) {
	t.Parallel()
	tt := []struct {
		name    string
		mt      string
		accepts []string
		expect  bool
	}{
		{
			name:    "Empty",
			mt:      MediaTypeOCI1ManifestList,
			accepts: []string{},
			expect:  true,
		},
		{
			name:    "Single",
			mt:      MediaTypeOCI1ManifestList,
			accepts: []string{MediaTypeOCI1ManifestList},
			expect:  true,
		},
		{
			name:    "Mismatch",
			mt:      MediaTypeOCI1Manifest,
			accepts: []string{MediaTypeOCI1ManifestList},
			expect:  false,
		},
		{
			name:    "Multiple",
			mt:      MediaTypeOCI1ManifestList,
			accepts: []string{MediaTypeOCI1Manifest, MediaTypeOCI1ManifestList, MediaTypeDocker2Manifest, MediaTypeDocker2ManifestList},
			expect:  true,
		},
		{
			name:    "Comma separated",
			mt:      MediaTypeOCI1ManifestList,
			accepts: []string{"application/vnd.oci.image.manifest.v1+json; charset=utf-8, application/vnd.oci.image.index.v1+json; charset=utf-8"},
			expect:  true,
		},
		{
			name:    "Wildcard",
			mt:      MediaTypeOCI1ManifestList,
			accepts: []string{"*/*"},
			expect:  true,
		},
		{
			name:    "Partial wildcard",
			mt:      MediaTypeOCI1ManifestList,
			accepts: []string{"application/*"},
			expect:  true,
		},
		{
			name:    "Invalid wildcard",
			mt:      MediaTypeOCI1ManifestList,
			accepts: []string{"*/vnd.oci.image.index.v1+json"},
			expect:  false,
		},
		{
			name:    "Mismatch wildcard",
			mt:      MediaTypeOCI1ManifestList,
			accepts: []string{"text/*"},
			expect:  false,
		},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			result := MediaTypeAccepts(tc.mt, tc.accepts)
			if tc.expect != result {
				t.Errorf("invalid result: expected %t, received %t", tc.expect, result)
			}
		})
	}
}

func TestMediaTypeClassifiers(t *testing.T) {
	t.Parallel()
	tt := []struct {
		name      string
		mt        string
		isForeign bool
		isIndex   bool
		isImage   bool
	}{
		{
			name:    "oci-index",
			mt:      MediaTypeOCI1ManifestList,
			isIndex: true,
		},
		{
			name:    "oci-image",
			mt:      MediaTypeOCI1Manifest,
			isImage: true,
		},
		{
			name: "oci-config",
			mt:   MediaTypeOCI1ImageConfig,
		},
		{
			name: "oci-layer",
			mt:   MediaTypeOCI1Layer,
		},
		{
			name: "oci-layer-gzip",
			mt:   MediaTypeOCI1LayerGzip,
		},
		{
			name:      "oci-foreign-layer",
			mt:        MediaTypeOCI1ForeignLayer,
			isForeign: true,
		},
		{
			name:    "docker-list",
			mt:      MediaTypeDocker2ManifestList,
			isIndex: true,
		},
		{
			name:    "docker-image",
			mt:      MediaTypeDocker2Manifest,
			isImage: true,
		},
		{
			name:      "docker-foreign-layer",
			mt:        MediaTypeDocker2ForeignLayer,
			isForeign: true,
		},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			if result := MediaTypeForeign(tc.mt); tc.isForeign != result {
				t.Errorf("foreign media type, expected %t, received %t", tc.isForeign, result)
			}
			if result := MediaTypeIndex(tc.mt); tc.isIndex != result {
				t.Errorf("index media type, expected %t, received %t", tc.isIndex, result)
			}
			if result := MediaTypeImage(tc.mt); tc.isImage != result {
				t.Errorf("image media type, expected %t, received %t", tc.isImage, result)
			}
			if result := MediaTypeManifest(tc.mt); (tc.isIndex || tc.isImage) != result {
				t.Errorf("manifest media type, expected %t, received %t", (tc.isIndex || tc.isImage), result)
			}
		})
	}
}

func TestMediaTypeDetect(t *testing.T) {
	t.Parallel()
	tt := []struct {
		name string
		in   []byte
		mt   string
	}{
		{
			name: "empty-json",
			in:   []byte("{}"),
			mt:   "",
		},
		{
			name: "media-type-field",
			in: []byte(`
{
  "mediaType": "application/example.custom"
}
`),
			mt: "application/example.custom",
		},
		{
			name: "parse-failure",
			in:   []byte("does not parse"),
			mt:   "",
		},
		{
			name: "detect-oci-index",
			in: []byte(`
{
  "schemaVersion": 2,
  "manifests": [
    {
      "mediaType": "application/vnd.oci.image.manifest.v1+json",
      "digest": "sha256:ddb491a4f92012e1b6e97326146eb7f0bbe9627ca0dd90b8d5e710d25afc370f",
      "size": 1289,
      "platform": {
        "architecture": "amd64",
        "os": "linux"
      }
    }
  ]
}
`),
			mt: MediaTypeOCI1ManifestList,
		},
		{
			name: "detect-oci-manifest",
			in: []byte(`
{
  "schemaVersion": 2,
  "config": {
    "mediaType": "application/vnd.oci.image.config.v1+json",
    "digest": "sha256:bf58602aff201e003d642f1205adf9dbf48a06a15be2ef93f5e4aceeae8430ff",
    "size": 2816
  },
  "layers": [
    {
      "mediaType": "application/vnd.oci.image.layer.v1.tar+gzip",
      "digest": "sha256:94eef15d57036d42ecbaafc81967320647467eaafb1b88f8bd1cb86c96cb5148",
      "size": 106313
    },
    {
      "mediaType": "application/vnd.oci.image.layer.v1.tar+gzip",
      "digest": "sha256:f865f9de71e2828b26f2fea4349092ad97f34f67296f92757a63c166d113488e",
      "size": 5321862
    }
  ]
 }
`),
			mt: MediaTypeOCI1Manifest,
		},
		{
			name: "detect-docker-list",
			in: []byte(`
{
  "schemaVersion": 2,
  "manifests": [
    {
      "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
      "digest": "sha256:ddb491a4f92012e1b6e97326146eb7f0bbe9627ca0dd90b8d5e710d25afc370f",
      "size": 1289,
      "platform": {
        "architecture": "amd64",
        "os": "linux"
      }
    }
  ]
}
`),
			mt: MediaTypeDocker2ManifestList,
		},
		{
			name: "detect-docker-manifest",
			in: []byte(`
{
  "schemaVersion": 2,
  "config": {
    "mediaType": "application/vnd.docker.container.image.v1+json",
    "digest": "sha256:bf58602aff201e003d642f1205adf9dbf48a06a15be2ef93f5e4aceeae8430ff",
    "size": 2816
  },
  "layers": [
    {
      "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
      "digest": "sha256:94eef15d57036d42ecbaafc81967320647467eaafb1b88f8bd1cb86c96cb5148",
      "size": 106313
    },
    {
      "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
      "digest": "sha256:f865f9de71e2828b26f2fea4349092ad97f34f67296f92757a63c166d113488e",
      "size": 5321862
    }
  ]
 }
`),
			mt: MediaTypeDocker2Manifest,
		},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			mt := MediaTypeDetect(tc.in)
			if mt != tc.mt {
				t.Errorf("expected %s, received %s", tc.mt, mt)
			}
		})
	}
}
