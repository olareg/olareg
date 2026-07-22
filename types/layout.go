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
	"slices"
	"time"

	digest "github.com/sudo-bmitch/oci-digest"

	"github.com/olareg/olareg/internal/reproducible"
)

const (
	// LayoutVersion is the supported release of the OCI Layout file definition.
	LayoutVersion = "1.0.0"
)

// Layout is the JSON contents of the oci-layout file.
type Layout struct {
	// Version is the implemented OCI Layout version in a given directory.
	Version string `json:"imageLayoutVersion"`
}

// LayoutIndex is an extended Index with added fields for the index.json file.
type LayoutIndex struct {
	Index

	// History is a map of tags to a slice of history entries known for that tag.
	History map[string][]LayoutHistory `json:"org.olareg.history,omitempty"`

	// childManifests is used to recursively include child manifests from nested indexes.
	childManifests []Descriptor
}

type LayoutHistory struct {
	Time       time.Time  `json:"time"`
	Deleted    bool       `json:"deleted,omitempty"`
	Descriptor Descriptor `json:"descriptor"`
}

type layoutIndexConf struct {
	children []Descriptor
}

type LayoutIndexOpt func(*layoutIndexConf)

// LayoutWithChildren is used to specify child descriptors to move from the embedded index to childManifest descriptor list.
func LayoutWithChildren(children []Descriptor) LayoutIndexOpt {
	return func(ic *layoutIndexConf) {
		ic.children = append(ic.children, children...)
	}
}

// AddChildren is used by store implementations to track descriptors from nested manifests (in a child index).
func (li *LayoutIndex) AddChildren(children []Descriptor) {
	li.childManifests = append(li.childManifests, children...)
}

// Copy returns a deep copy of the index to avoid data races
func (li LayoutIndex) Copy() LayoutIndex {
	li2 := li
	li2.Index = li.Index.Copy()
	// deep copy childManifests
	li2.childManifests = make([]Descriptor, len(li.childManifests))
	for im := range li.childManifests {
		li2.childManifests[im] = li.childManifests[im].Copy()
	}
	// deep copy the history
	li2.History = make(map[string][]LayoutHistory)
	for tag := range li.History {
		li2.History[tag] = make([]LayoutHistory, len(li.History[tag]))
		for ih := range li.History[tag] {
			li2.History[tag][ih] = li.History[tag][ih]
			li2.History[tag][ih].Descriptor = li.History[tag][ih].Descriptor.Copy()
		}
	}
	return li2
}

// AddDesc adds an entry to the LayoutIndex with deduplication.
// A timestamp is added to the inserted descriptor if not already set.
// If the descriptor exists as a child, it is removed from the child entries.
// Alternate references to a tag or referrers response are removed.
// If a descriptor exists but a tag or referrer annotation is being added, an existing descriptor will be updated.
// This method ignores and may lose unrecognized fields and annotations.
// The "WithChildren" option moves matching descriptors without annotations to child manifest list.
func (li *LayoutIndex) AddDesc(d Descriptor, opts ...LayoutIndexOpt) {
	conf := layoutIndexConf{children: []Descriptor{}}
	for _, opt := range opts {
		opt(&conf)
	}
	d = d.Copy() // do not modify the original descriptor (Annotations are a pointer)
	if d.Annotations == nil {
		d.Annotations = map[string]string{}
	}
	tag := d.Annotations[AnnotRefName]
	referrer := d.Annotations[AnnotReferrerSubject]
	// Set creation time on descriptor
	timestamp := reproducible.TimeNow().Format(time.RFC3339)
	if d.Annotations[AnnotCreated] == "" {
		d.Annotations[AnnotCreated] = timestamp
	}

	// Move entries from WithChildren option to childManifest list.
	// These are from nested manifests that were pushed before the parent index (`d`).
	for _, child := range conf.children {
		if mi := slices.IndexFunc(li.Manifests, func(cur Descriptor) bool {
			// the digest must match and this cannot have other selectors in the annotations
			return cur.Digest.Equal(child.Digest) && annotationsEmpty(cur)
		}); mi >= 0 {
			li.childManifests = append(li.childManifests, child)
			li.Manifests = slices.Delete(li.Manifests, mi, mi+1)
		}
	}

	// Remove this entry from the child list
	li.childManifests = slices.DeleteFunc(li.childManifests, func(cur Descriptor) bool { return cur.Digest.Equal(d.Digest) })

	// If this is the referrers response, replace existing response or append this response, and return
	if referrer != "" {
		if mi := slices.IndexFunc(li.Manifests, func(cur Descriptor) bool {
			return cur.annotationVal(AnnotReferrerSubject) == referrer
		}); mi >= 0 {
			li.Manifests[mi] = d
		} else {
			li.Manifests = append(li.Manifests, d)
		}
		return
	}

	// If this is a tagged entry, untag any previous tag targets to different digests
	if tag != "" {
		for mi := len(li.Manifests) - 1; mi >= 0; mi-- {
			if !li.Manifests[mi].Digest.Equal(d.Digest) && li.Manifests[mi].annotationVal(AnnotRefName) == tag {
				// remove tag pointing to different digest, make a new descriptor in case there are other annotations
				if slices.IndexFunc(li.Manifests, func(cur Descriptor) bool {
					return cur.Digest.Equal(li.Manifests[mi].Digest) && cur.annotationVal(AnnotRefName) != tag
				}) >= 0 {
					// another descriptor exists for the same digest, delete this tagged entry
					li.Manifests = slices.Delete(li.Manifests, mi, mi+1)
				} else {
					// untagged this entry (by deleting the annotation)
					delete(li.Manifests[mi].Annotations, AnnotRefName)
				}
				// ensure mi is not past the end of the list after the next `mi--`
				if mi > len(li.Manifests) {
					mi = len(li.Manifests)
				}
			}
		}
	}

	// Search for a compatible entry with the same digest, if tagged find an entry with no tag or a matching tag, else if untagged match any digest
	// Update only if tag is being added, else return
	if mi := slices.IndexFunc(li.Manifests, func(cur Descriptor) bool {
		return cur.Digest.Equal(d.Digest) &&
			(tag == "" || annotationsEmpty(cur) || cur.annotationVal(AnnotRefName) == tag)
	}); mi >= 0 {
		// add tag to an existing entry without a tag
		if tag != "" && annotationsEmpty(li.Manifests[mi]) {
			li.Manifests[mi] = d
		}
		// all other matches are a noop to avoid changing the created time
	} else {
		li.Manifests = append(li.Manifests, d)
	}

	// track history in the index of tag creates
	li.addHistory(d, false)
}

// GetDesc returns a descriptor for a tag or digest, including child descriptors.
func (li LayoutIndex) GetDesc(arg string) (Descriptor, error) {
	if RefTagRE.MatchString(arg) {
		// search for tag
		mi := slices.IndexFunc(li.Manifests, func(cur Descriptor) bool { return cur.annotationVal(AnnotRefName) == arg })
		if mi >= 0 {
			return li.Manifests[mi].Copy(), nil
		}
	} else {
		// else, attempt to parse digest
		dig, err := digest.Parse(arg)
		if err != nil {
			return Descriptor{}, err
		}
		// return a matching descriptor, but stripped of any annotations to avoid mixing with tags
		mi := slices.IndexFunc(li.Manifests, func(cur Descriptor) bool { return cur.Digest.Equal(dig) })
		if mi >= 0 {
			return Descriptor{
				MediaType: li.Manifests[mi].MediaType,
				Digest:    li.Manifests[mi].Digest,
				Size:      li.Manifests[mi].Size,
			}, nil
		}
		// search child manifests
		mi = slices.IndexFunc(li.childManifests, func(cur Descriptor) bool { return cur.Digest.Equal(dig) })
		if mi >= 0 {
			return Descriptor{
				MediaType: li.childManifests[mi].MediaType,
				Digest:    li.childManifests[mi].Digest,
				Size:      li.childManifests[mi].Size,
			}, nil
		}
	}
	return Descriptor{}, ErrNotFound
}

// RmDesc deletes a descriptor from the index.json.
// If the provided descriptor is tagged, at least one reference to the digest will be preserved.
// Otherwise all descriptors matching the digest are deleted.
func (li *LayoutIndex) RmDesc(d Descriptor) {
	tag := d.annotationVal(AnnotRefName)
	if tag != "" {
		if slices.IndexFunc(li.Manifests, func(cur Descriptor) bool {
			return cur.Digest.Equal(d.Digest) && cur.annotationVal(AnnotRefName) != tag
		}) >= 0 {
			// another descriptor exists for the same digest, delete this tagged descriptor
			li.Manifests = slices.DeleteFunc(li.Manifests, func(cur Descriptor) bool {
				if cur.Digest.Equal(d.Digest) && cur.annotationVal(AnnotRefName) == tag {
					li.addHistory(cur, true)
					return true
				}
				return false
			})
		} else {
			// no other descriptors for the given digest, remove the tag annotation
			mi := slices.IndexFunc(li.Manifests, func(cur Descriptor) bool { return cur.Digest.Equal(d.Digest) && cur.annotationVal(AnnotRefName) == tag })
			if mi >= 0 {
				li.addHistory(li.Manifests[mi], true)
				delete(li.Manifests[mi].Annotations, AnnotRefName)
			}
		}
	} else {
		// remove all references to a digest
		li.childManifests = slices.DeleteFunc(li.childManifests, func(cur Descriptor) bool { return cur.Digest.Equal(d.Digest) })
		li.Manifests = slices.DeleteFunc(li.Manifests, func(cur Descriptor) bool {
			if cur.Digest.Equal(d.Digest) {
				li.addHistory(cur, true)
				return true
			}
			return false
		})
	}
}

func (li *LayoutIndex) addHistory(d Descriptor, del bool) {
	tag := d.annotationVal(AnnotRefName)
	if tag == "" {
		return
	}
	timestamp := time.Time{}
	if !del {
		if t, err := time.Parse(time.RFC3339, d.annotationVal(AnnotCreated)); err == nil {
			timestamp = t
		}
	}
	if timestamp.IsZero() {
		timestamp = reproducible.TimeNow()
	}
	dh := Descriptor{
		MediaType: d.MediaType,
		Digest:    d.Digest,
		Size:      d.Size,
	}
	if li.History == nil {
		li.History = map[string][]LayoutHistory{}
	}
	li.History[tag] = append(li.History[tag], LayoutHistory{
		Time:       timestamp,
		Deleted:    del,
		Descriptor: dh,
	})
}

func annotationsEmpty(d Descriptor) bool {
	for k := range d.Annotations {
		switch k {
		// list annotations excluded from an empty check here
		case AnnotCreated:
			// ignore
		default:
			return false
		}
	}
	return true
}
