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
	"time"

	digest "github.com/sudo-bmitch/oci-digest"

	"github.com/olareg/olareg/internal/reproducible"
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

	// childManifests is used to recursively include child manifests from nested indexes.
	childManifests []Descriptor
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

type indexConf struct {
	children []Descriptor
}

type IndexOpt func(*indexConf)

// IndexWithChildren is used by [Index.AddDesc] to specify child descriptors to move from Manifest to childManifest descriptor list.
func IndexWithChildren(children []Descriptor) IndexOpt {
	return func(ic *indexConf) {
		ic.children = append(ic.children, children...)
	}
}

// AddChildren is used by store implementations to track descriptors from nested manifests (in a child index).
func (i *Index) AddChildren(children []Descriptor) {
	i.childManifests = append(i.childManifests, children...)
}

// Copy returns a deep copy of the index to avoid data races
func (i Index) Copy() Index {
	i2 := i
	i2.Manifests = make([]Descriptor, len(i.Manifests))
	for im := range i.Manifests {
		i2.Manifests[im] = i.Manifests[im].Copy()
	}
	i2.childManifests = make([]Descriptor, len(i.childManifests))
	for im := range i.childManifests {
		i2.childManifests[im] = i.childManifests[im].Copy()
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

// GetDesc returns a descriptor for a tag or digest, including child descriptors.
func (i Index) GetDesc(arg string) (Descriptor, error) {
	var dZero Descriptor
	if len(i.Manifests) == 0 {
		return dZero, ErrNotFound
	}
	if RefTagRE.MatchString(arg) {
		// search for tag
		mi := slices.IndexFunc(i.Manifests, func(cur Descriptor) bool { return annotationEqual(cur.Annotations, AnnotRefName, arg) })
		if mi >= 0 {
			return i.Manifests[mi].Copy(), nil
		}
	} else {
		// else, attempt to parse digest
		dig, err := digest.Parse(arg)
		if err != nil {
			return dZero, err
		}
		// return a matching descriptor, but stripped of any annotations to avoid mixing with tags
		mi := slices.IndexFunc(i.Manifests, func(cur Descriptor) bool { return cur.Digest.Equal(dig) })
		if mi >= 0 {
			return Descriptor{
				MediaType: i.Manifests[mi].MediaType,
				Digest:    i.Manifests[mi].Digest,
				Size:      i.Manifests[mi].Size,
			}, nil
		}
		// fallback to searching child manifests
		mi = slices.IndexFunc(i.childManifests, func(cur Descriptor) bool { return cur.Digest.Equal(dig) })
		if mi >= 0 {
			return Descriptor{
				MediaType: i.childManifests[mi].MediaType,
				Digest:    i.childManifests[mi].Digest,
				Size:      i.childManifests[mi].Size,
			}, nil
		}
	}
	return dZero, ErrNotFound
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

// AddDesc adds an entry to the Index with deduplication.
// Alternate references to a tag or referrers response are removed.
// If a descriptor exists but a tag or referrer annotation is being added, an existing descriptor will be updated.
// If the descriptor exists as a child, it is removed from the child entries.
// This method ignores and may lose unrecognized fields and annotations.
// The "WithChildren" option moves matching descriptors without annotations to child manifest list.
func (i *Index) AddDesc(d Descriptor, opts ...IndexOpt) {
	conf := indexConf{children: []Descriptor{}}
	for _, opt := range opts {
		opt(&conf)
	}
	d = d.Copy() // do not modify the original descriptor (Annotations are a pointer)
	if d.Annotations == nil {
		d.Annotations = map[string]string{}
	}
	// Set creation time on descriptor
	d.Annotations[AnnotCreated] = reproducible.TimeNow().Format(time.RFC3339)
	// extract tag and referrer details if set
	tag := d.Annotations[AnnotRefName]
	referrer := d.Annotations[AnnotReferrerSubject]

	// Move entries from WithChildren option to childManifest list.
	// These are from nested manifests that were pushed before the parent index (`d`).
	for _, child := range conf.children {
		if mi := slices.IndexFunc(i.Manifests, func(cur Descriptor) bool {
			// the digest must match and this cannot have other selectors in the annotations
			return cur.Digest.Equal(child.Digest) && annotationsEmpty(cur.Annotations)
		}); mi >= 0 {
			i.childManifests = append(i.childManifests, child)
			i.Manifests = slices.Delete(i.Manifests, mi, mi+1)
		}
	}

	// Remove this entry from the child list
	i.childManifests = slices.DeleteFunc(i.childManifests, func(cur Descriptor) bool { return cur.Digest.Equal(d.Digest) })

	// If this is the referrers response, replace existing response or append this response, and return
	if referrer != "" {
		if mi := slices.IndexFunc(i.Manifests, func(cur Descriptor) bool {
			return annotationEqual(cur.Annotations, AnnotReferrerSubject, referrer)
		}); mi >= 0 {
			i.Manifests[mi] = d
		} else {
			i.Manifests = append(i.Manifests, d)
		}
		return
	}

	// If this is a tagged entry, untag any previous tag targets to different digests
	if tag != "" {
		for mi := len(i.Manifests) - 1; mi >= 0; mi-- {
			if !i.Manifests[mi].Digest.Equal(d.Digest) && annotationEqual(i.Manifests[mi].Annotations, AnnotRefName, tag) {
				// remove tag pointing to different digest, make a new descriptor in case there are other annotations
				i.RmDesc(Descriptor{
					MediaType: i.Manifests[mi].MediaType,
					Digest:    i.Manifests[mi].Digest,
					Size:      i.Manifests[mi].Size,
					Annotations: map[string]string{
						AnnotRefName: tag,
					},
				})
				// ensure mi is not past the end of the list after the next `mi--`
				if mi > len(i.Manifests) {
					mi = len(i.Manifests)
				}
			}
		}
	}

	// Search for a compatible entry with the same digest, if tagged find an entry with no tag or a matching tag, else if untagged match any digest
	// Update only if tag is being added, else return
	if mi := slices.IndexFunc(i.Manifests, func(cur Descriptor) bool {
		return cur.Digest.Equal(d.Digest) &&
			(tag == "" || annotationsEmpty(cur.Annotations) || annotationEqual(cur.Annotations, AnnotRefName, tag))
	}); mi >= 0 {
		// add tag to an existing entry without a tag
		if tag != "" && annotationsEmpty(i.Manifests[mi].Annotations) {
			i.Manifests[mi] = d
		}
		// all other matches are a noop to avoid changing the created time
	} else {
		i.Manifests = append(i.Manifests, d)
	}

	// TODO: track history in the index of tag create/delete
}

// RmDesc deletes a descriptor from the index.
// If the digest is set, but not a tag or referrer annotation, all references to the digest are deleted.
// If the tag is set, the specific digest is untagged if the digest is provided, otherwise all descriptors with the tag are deleted.
// If the referrer annotation is set, all descriptors pointing to the referrer are deleted.
// If the digest is unset, and the tag and referrer annotations are unset, no action is performed.
func (i *Index) RmDesc(d Descriptor) {
	tag := ""
	referrer := ""
	if d.Annotations != nil {
		tag = d.Annotations[AnnotRefName]
		referrer = d.Annotations[AnnotReferrerSubject]
	}
	switch {
	case tag == "" && referrer == "" && !d.Digest.IsZero():
		// delete all references to a digest
		i.childManifests = slices.DeleteFunc(i.childManifests, func(cur Descriptor) bool { return cur.Digest.Equal(d.Digest) })
		i.Manifests = slices.DeleteFunc(i.Manifests, func(cur Descriptor) bool { return cur.Digest.Equal(d.Digest) })
	case tag != "" && d.Digest.IsZero():
		// remove tags
		i.Manifests = slices.DeleteFunc(i.Manifests, func(cur Descriptor) bool { return annotationEqual(cur.Annotations, AnnotRefName, tag) })
	case tag != "" && !d.Digest.IsZero():
		// delete a specific tag, but leave one descriptor pointing to the digest
		mi := slices.IndexFunc(i.Manifests, func(cur Descriptor) bool {
			return cur.Digest.Equal(d.Digest) && annotationEqual(cur.Annotations, AnnotRefName, tag)
		})
		if mi < 0 {
			break
		}
		if slices.IndexFunc(i.Manifests, func(cur Descriptor) bool {
			return cur.Digest.Equal(d.Digest) && !annotationEqual(cur.Annotations, AnnotRefName, tag)
		}) >= 0 {
			// another descriptor exists for the same digest, delete this tagged entry
			i.Manifests = slices.Delete(i.Manifests, mi, mi+1)
		} else {
			// untagged this entry (by deleting the annotation)
			delete(i.Manifests[mi].Annotations, AnnotRefName)
		}
	case referrer != "":
		// remove referrer response
		i.Manifests = slices.DeleteFunc(i.Manifests, func(cur Descriptor) bool { return annotationEqual(cur.Annotations, AnnotReferrerSubject, referrer) })
	}
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

func annotationEqual(annotations map[string]string, key, value string) bool {
	return annotations != nil && annotations[key] == value
}

func annotationsEmpty(annotations map[string]string) bool {
	return len(annotations) == 0 || (len(annotations) == 1 && annotations[AnnotCreated] != "")
}
