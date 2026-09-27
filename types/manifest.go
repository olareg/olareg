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

// Copy returns a deep copy of the index.
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

// Paginate splits an index into the next paginated response.
// Only the Manifests are split, all other fields are copied.
// The returned bytes are the JSON formatted page.
// The returned int is the number of manifests included (plus any skipped), -1 if all manifests are included, or 0 on failures.
// This returns an error if a single entry still exceeds the limit, but will attempt to paginate remaining entries.
func (i Index) Paginate(limit int64) ([]byte, int, error) {
	lower, upper := 1, len(i.Manifests)
	out, err := json.Marshal(i)
	if err != nil {
		return nil, 0, err
	}
	if upper == 0 || int64(len(out)) <= limit {
		return out, -1, nil
	}
	origList := i.Manifests
	errs := []error{}
	skip := 0
	// skip any descriptors that exceed the limit by themselves
	for {
		i.Manifests = origList[:1]
		out, err = json.Marshal(i)
		if err != nil {
			return nil, 0, err
		}
		if int64(len(out)) > limit {
			if len(origList) == 0 {
				errs = append(errs, fmt.Errorf("all descriptors exceed limit"))
				return nil, 0, errors.Join(errs...)
			}
			errs = append(errs, fmt.Errorf("descriptor greater than pagination limit: %s%.0w", origList[0].Digest, ErrPaginationSkippedDescriptor))
			origList = origList[1:]
			skip++
		} else {
			break
		}
	}
	try := upper
	for lower+1 < upper {
		// quickly narrow down the target index based on percentage of the limit used in last guess
		diff := -1 // avoid try being set to the same value twice when close to target
		if try == lower {
			diff = 1
		}
		try = int(float64(try)*float64(limit)/float64(len(out))) + diff
		// switch to binary search if the estimate breaks down (from unequally sized descriptors)
		if try <= lower || try >= upper {
			try = lower + (upper-lower+1)/2
		}
		i.Manifests = origList[:try]
		out, err = json.Marshal(i)
		if err != nil {
			return nil, 0, err
		}
		if int64(len(out)) <= limit {
			lower = try
		} else {
			upper = try
		}
	}
	if try != lower {
		try = lower
		i.Manifests = origList[:try]
		out, err = json.Marshal(i)
		if err != nil {
			return nil, 0, err
		}
	}
	if len(errs) > 0 {
		return out, try + skip, errors.Join(errs...)
	}
	return out, try + skip, nil
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
// If the manifest does not contain a subject, [ErrNotFOund] is returned.
func ManifestReferrerDescriptor(raw []byte, d Descriptor) (Descriptor, Descriptor, error) {
	rd := d
	subject := Descriptor{}
	referrer := referrerParse{}
	err := json.Unmarshal(raw, &referrer)
	if err != nil {
		return subject, rd, fmt.Errorf("%w%.0w", err, ErrParsingFailed)
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

// ManifestParseDescriptors reads the contents of a manifest and returns the contained descriptors up to the requested depth.
// Note that the subject descriptor is not returned at any depth.
func ManifestParseDescriptors(raw []byte, d Descriptor, depth ManifestParseDepth) ([]Descriptor, error) {
	switch d.MediaType {
	case MediaTypeOCI1ManifestList, MediaTypeDocker2ManifestList:
		if depth >= ParseManifests {
			ind := Index{}
			err := json.Unmarshal(raw, &ind)
			if err != nil {
				return nil, fmt.Errorf("%w%.0w", err, ErrParsingFailed)
			}
			return ind.Manifests, nil
		}
	case MediaTypeOCI1Manifest, MediaTypeDocker2Manifest:
		if depth >= ParseBlobs {
			man := Manifest{}
			err := json.Unmarshal(raw, &man)
			if err != nil {
				return nil, fmt.Errorf("%w%.0w", err, ErrParsingFailed)
			}
			return append(man.Layers, man.Config), nil
		}
	}
	return nil, nil
}

type ManifestParseDepth int

const (
	ParseNone      ManifestParseDepth = iota // Do not parse any descriptors
	ParseManifests                           // Parse and return only descriptors to other manifests in the index
	ParseBlobs                               // Parse and return descriptors to manifest and other blobs
)
