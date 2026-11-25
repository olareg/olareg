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
	"bytes"
	"maps"
	"slices"

	digest "github.com/sudo-bmitch/oci-digest"
)

// Descriptor is used in manifests to refer to content by media type, size, and digest.
type Descriptor struct {
	// MediaType describe the type of the content.
	MediaType string `json:"mediaType"`

	// Digest uniquely identifies the content.
	Digest digest.Digest `json:"digest"`

	// Size in bytes of content.
	Size int64 `json:"size"`

	// URLs contains the source URLs of this content.
	URLs []string `json:"urls,omitempty"`

	// Annotations contains arbitrary metadata relating to the targeted content.
	Annotations map[string]string `json:"annotations,omitempty"`

	// Data is an embedding of the targeted content. This is encoded as a base64
	// string when marshalled to JSON (automatically, by encoding/json). If
	// present, Data can be used directly to avoid fetching the targeted content.
	Data []byte `json:"data,omitempty"`

	// Platform describes the platform which the image in the manifest runs on.
	// This should only be used when referring to a manifest.
	Platform *Platform `json:"platform,omitempty"`

	// ArtifactType is the media type of the artifact this descriptor refers to.
	ArtifactType string `json:"artifactType,omitempty"`
}

// AnnotationsCustom indicates there is an unexpected annotation on this descriptor (in the index.json context).
func (d Descriptor) AnnotationsCustom() bool {
	for k := range d.Annotations {
		switch k {
		case AnnotCreated, AnnotRefName, AnnotReferrerSubject, AnnotTagHistory:
			// ignore known annotations
		default:
			return true // at least one annotation found
		}
	}
	return false
}

// AnnotationsEmpty indicates there are no annotations (other than a created timestamp).
func (d Descriptor) AnnotationsEmpty() bool {
	for k := range d.Annotations {
		switch k {
		// list annotations excluded from an empty check here
		case AnnotCreated:
			// ignore
		default:
			return false // at least one annotation found
		}
	}
	return true
}

// AnnotationsInternal indicates there are olareg internal annotations for referrers or tag history.
func (d Descriptor) AnnotationsInternal() bool {
	for k := range d.Annotations {
		switch k {
		case AnnotReferrerSubject, AnnotTagHistory:
			return true
		}
	}
	return false
}

// AnnotationVal returns a value from the annotations, or an empty string if unset.
func (d Descriptor) AnnotationVal(key string) (string, bool) {
	if d.Annotations == nil {
		return "", false
	}
	val, ok := d.Annotations[key]
	return val, ok
}

// Copy returns a copy of the descriptor
func (d Descriptor) Copy() Descriptor {
	d2 := d
	if d.URLs != nil {
		d2.URLs = make([]string, len(d.URLs))
		copy(d2.URLs, d.URLs)
	}
	if d.Data != nil {
		d2.Data = make([]byte, len(d.Data))
		copy(d2.Data, d.Data)
	}
	if d.Platform != nil {
		p := d.Platform.Copy()
		d2.Platform = &p
	}
	if d.Annotations != nil {
		d2.Annotations = make(map[string]string)
		maps.Copy(d2.Annotations, d.Annotations)
	}
	return d2
}

// Equal returns true if the two descriptors are identical.
func (d Descriptor) Equal(d2 Descriptor) bool {
	if d.MediaType != d2.MediaType || d.Size != d2.Size || d.ArtifactType != d2.ArtifactType ||
		!d.Digest.Equal(d2.Digest) || !bytes.Equal(d.Data, d2.Data) ||
		(d.Platform != d2.Platform && (d.Platform == nil || d2.Platform == nil || !d.Platform.Equal(*d2.Platform))) ||
		!maps.Equal(d.Annotations, d2.Annotations) || !slices.Equal(d.URLs, d2.URLs) {
		return false
	}
	return true
}

// Same returns true if the two descriptors are for the same content.
// This ignores timestamp differences in the creation time annotation.
func (d Descriptor) Same(d2 Descriptor) bool {
	if d.MediaType != d2.MediaType || d.Size != d2.Size || d.ArtifactType != d2.ArtifactType ||
		!d.Digest.Equal(d2.Digest) || !bytes.Equal(d.Data, d2.Data) ||
		(d.Platform != d2.Platform && (d.Platform == nil || d2.Platform == nil || !d.Platform.Equal(*d2.Platform))) ||
		!slices.Equal(d.URLs, d2.URLs) {
		return false
	}
	a1 := d.Annotations
	if a1 == nil {
		a1 = map[string]string{}
	}
	a2 := d2.Annotations
	if a2 == nil {
		a2 = map[string]string{}
	}
	c1 := len(a1)
	if _, ok := a1[AnnotCreated]; !ok {
		c1++
	}
	c2 := len(a2)
	if _, ok := a2[AnnotCreated]; !ok {
		c2++
	}
	if c1 != c2 {
		return false
	}
	for k, v := range a1 {
		if a2[k] != v && k != AnnotCreated {
			return false
		}
	}
	return true
}
