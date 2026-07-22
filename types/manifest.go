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
	"maps"
	"slices"

	digest "github.com/sudo-bmitch/oci-digest"
)

// Index references manifests for various platforms.
type Index struct {
	// SchemaVersion is the image manifest schema that this image follows
	SchemaVersion int `json:"schemaVersion"`

	// MediaType specifies the type of this document data structure e.g. `application/vnd.oci.image.index.v1+json`
	MediaType string `json:"mediaType,omitempty"`

	// ArtifactType specifies the IANA media type of artifact when the manifest is used for an artifact.
	ArtifactType string `json:"artifactType,omitempty"`

	// Manifests references platform specific manifests.
	Manifests []Descriptor `json:"manifests"`

	// Subject is an optional link from the image manifest to another manifest forming an association between the image manifest and the other manifest.
	Subject *Descriptor `json:"subject,omitempty"`

	// Annotations contains arbitrary metadata for the image index.
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Manifest defines an OCI image
type Manifest struct {
	// SchemaVersion is the image manifest schema that this image follows
	SchemaVersion int `json:"schemaVersion"`

	// MediaType specifies the type of this document data structure e.g. `application/vnd.oci.image.manifest.v1+json`
	MediaType string `json:"mediaType,omitempty"`

	// ArtifactType specifies the IANA media type of artifact when the manifest is used for an artifact.
	ArtifactType string `json:"artifactType,omitempty"`

	// Config references a configuration object for a container, by digest.
	// The referenced configuration object is a JSON blob that the runtime uses to set up the container.
	Config Descriptor `json:"config"`

	// Layers is an indexed list of layers referenced by the manifest.
	Layers []Descriptor `json:"layers"`

	// Subject is an optional link from the image manifest to another manifest forming an association between the image manifest and the other manifest.
	Subject *Descriptor `json:"subject,omitempty"`

	// Annotations contains arbitrary metadata for the image manifest.
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Copy returns a deep copy of the index to avoid data races
func (i Index) Copy() Index {
	i2 := i
	i2.Manifests = make([]Descriptor, len(i.Manifests))
	for im := range i.Manifests {
		i2.Manifests[im] = i.Manifests[im].Copy()
	}
	if i.Subject != nil {
		d := i.Subject.Copy()
		i2.Subject = &d
	}
	if i.Annotations != nil {
		i2.Annotations = make(map[string]string)
		maps.Copy(i2.Annotations, i.Annotations)
	}
	return i2
}

// GetByAnnotation finds an entry with a matching annotation.
func (i *Index) GetByAnnotation(key, val string) (Descriptor, error) {
	var dRet Descriptor
	mi := slices.IndexFunc(i.Manifests, func(cur Descriptor) bool {
		if cur.Annotations == nil {
			return false
		}
		curVal, ok := cur.Annotations[key]
		return ok && (val == "" || val == curVal)
	})
	if mi >= 0 {
		return i.Manifests[mi], nil
	}
	return dRet, ErrNotFound
}

type referrerParse struct {
	MediaType    string            `json:"mediaType,omitempty"`
	ArtifactType string            `json:"artifactType,omitempty"`
	Config       *Descriptor       `json:"config"`
	Subject      *Descriptor       `json:"subject,omitempty"`
	Annotations  map[string]string `json:"annotations,omitempty"`
}

// ManifestReferrerDescriptor parses a manifest to generate the descriptor used in the referrer response.
// Two descriptors are returned, the subject, and the entry for the referrers response.
// The descriptor should be provided with a valid MediaType and Digest, otherwise they will be generated as a best effort.
func ManifestReferrerDescriptor(raw []byte, d Descriptor) (Descriptor, Descriptor, error) {
	rd := d
	subject := Descriptor{}
	referrer := referrerParse{}
	err := json.Unmarshal(raw, &referrer)
	if err != nil {
		return subject, rd, err
	}
	if referrer.Subject == nil || referrer.Subject.Digest.IsZero() {
		return subject, rd, ErrNotFound
	}
	subject = *referrer.Subject
	// build descriptor, pulling up artifact type and annotations
	if referrer.MediaType != "" {
		rd.MediaType = referrer.MediaType
	}
	if rd.Digest.IsZero() {
		rd.Digest, err = digest.Canonical.FromBytes(raw)
		if err != nil {
			return subject, rd, err
		}
	}
	rd.Size = int64(len(raw))
	if referrer.ArtifactType != "" {
		rd.ArtifactType = referrer.ArtifactType
	} else if referrer.Config != nil {
		rd.ArtifactType = referrer.Config.MediaType
	}
	rd.Annotations = referrer.Annotations
	return subject, rd, nil
}
