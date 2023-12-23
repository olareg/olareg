package types

import (
	"encoding/json"

	// imports required for go-digest
	_ "crypto/sha256"
	_ "crypto/sha512"

	"github.com/opencontainers/go-digest"
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

// GetDesc returns a descriptor for a tag or digest, including child descriptors.
func (i Index) GetDesc(arg string) (Descriptor, error) {
	var dRet Descriptor
	if i.Manifests == nil {
		return dRet, ErrNotFound
	}
	if RefTagRE.MatchString(arg) {
		// search for tag
		for _, d := range i.Manifests {
			if d.Annotations != nil && d.Annotations[AnnotRefName] == arg {
				return d, nil
			}
		}
	} else {
		// else, attempt to parse digest
		dig, err := digest.Parse(arg)
		if err != nil {
			return dRet, err
		}
		// return a matching descriptor, but stripped of any annotations to avoid mixing with tags
		for _, d := range i.Manifests {
			if d.Digest == dig {
				return Descriptor{
					MediaType: d.MediaType,
					Digest:    d.Digest,
					Size:      d.Size,
				}, nil
			}
		}
		if i.childManifests != nil {
			for _, d := range i.childManifests {
				if d.Digest == dig {
					return Descriptor{
						MediaType: d.MediaType,
						Digest:    d.Digest,
						Size:      d.Size,
					}, nil
				}
			}
		}
	}
	return dRet, ErrNotFound
}

// GetByAnnotation finds an entry with a matching annotation.
func (i *Index) GetByAnnotation(key, val string) (Descriptor, error) {
	var dRet Descriptor
	if i.Manifests == nil {
		return dRet, ErrNotFound
	}
	for _, d := range i.Manifests {
		if d.Annotations == nil {
			continue
		}
		if cur, ok := d.Annotations[key]; ok && (val == "" || val == cur) {
			return d, nil
		}
	}
	return dRet, ErrNotFound
}

// AddDesc adds an entry to the Index with deduplication.
// If a descriptor exists but a tag or referrer annotation are being added, an existing descriptor will be updated if found.
// If the descriptor exists as a child, it is removed from the child entries.
// This method ignores and may lose other fields and annotations other than the OCI reference annotation.
// The "WithChildren" option moves matching descriptors without annotations to child manifest list.
func (i *Index) AddDesc(d Descriptor, opts ...IndexOpt) {
	conf := indexConf{children: []Descriptor{}}
	for _, opt := range opts {
		opt(&conf)
	}
	tag := ""
	referrer := ""
	if d.Annotations != nil {
		tag = d.Annotations[AnnotRefName]
		referrer = d.Annotations[AnnotReferrerSubject]
	}
	// search for another descriptor to untag and referrers to delete
	if tag != "" || referrer != "" {
		for mi := len(i.Manifests) - 1; mi >= 0; mi-- {
			if i.Manifests[mi].Digest != d.Digest && i.Manifests[mi].Annotations != nil {
				if i.Manifests[mi].Annotations[AnnotRefName] == tag {
					delete(i.Manifests[mi].Annotations, AnnotRefName)
				}
				if i.Manifests[mi].Annotations[AnnotReferrerSubject] == referrer {
					i.Manifests = append(i.Manifests[:mi], i.Manifests[mi+1:]...)
				}
			}
		}
	}
	// remove child entry if found
	for ci := 0; ci < len(i.childManifests); ci++ {
		if i.childManifests[ci].Digest == d.Digest {
			i.childManifests[ci] = i.childManifests[len(i.childManifests)-1]
			i.childManifests = i.childManifests[:len(i.childManifests)-1]
			break
		}
	}
	// Move entries from WithChildren option to childManifest list.
	// These are from child descriptors when an index is later pushed.
	for _, cd := range conf.children {
		for mi := range i.Manifests {
			if i.Manifests[mi].Digest == cd.Digest && len(i.Manifests[mi].Annotations) == 0 {
				i.Manifests[mi] = i.Manifests[len(i.Manifests)-1]
				i.Manifests = i.Manifests[:len(i.Manifests)-1]
				i.childManifests = append(i.childManifests, cd)
				break
			}
		}
	}
	// search for matching or compatible entry
	for mi, md := range i.Manifests {
		if md.Digest == d.Digest {
			if tag == "" && referrer == "" {
				return
			}
			if md.Annotations == nil ||
				((tag == "" || md.Annotations[AnnotRefName] == "" || md.Annotations[AnnotRefName] == tag) &&
					(referrer == "" || md.Annotations[AnnotReferrerSubject] == "" || md.Annotations[AnnotReferrerSubject] == referrer)) {
				i.Manifests[mi] = d
				return
			}
		}
	}
	// append entry if no match found
	i.Manifests = append(i.Manifests, d)
}

// RmDesc deletes a descriptor from the index.
// If the descriptor has a digest and the tag value set, one reference to the untagged digest is preserved.
// If the digest is blank and either tag or referrer annotations are provided, all matching tags/referrers are deleted.
// Otherwise all references to the digest are removed.
func (i *Index) RmDesc(d Descriptor) {
	tag := ""
	referrer := ""
	if d.Annotations != nil {
		if d.Annotations[AnnotRefName] != "" {
			tag = d.Annotations[AnnotRefName]
		}
		if d.Annotations[AnnotReferrerSubject] != "" {
			referrer = d.Annotations[AnnotReferrerSubject]
		}
	}
	if tag == "" && d.Digest != "" {
		for mi := len(i.childManifests) - 1; mi >= 0; mi-- {
			if i.childManifests[mi].Digest == d.Digest {
				i.childManifests[mi] = i.childManifests[len(i.childManifests)-1]
				i.childManifests = i.childManifests[:len(i.childManifests)-1]
			}
		}
	}
	found := false
	for mi := len(i.Manifests) - 1; mi >= 0; mi-- {
		if d.Digest != "" && i.Manifests[mi].Digest == d.Digest {
			if tag != "" {
				// deleting a tag leaves one untagged manifest entry
				if found && (i.Manifests[mi].Annotations == nil || i.Manifests[mi].Annotations[AnnotRefName] == tag) {
					i.Manifests[mi] = i.Manifests[len(i.Manifests)-1]
					i.Manifests = i.Manifests[:len(i.Manifests)-1]
				} else if i.Manifests[mi].Annotations != nil && i.Manifests[mi].Annotations[AnnotRefName] == tag {
					delete(i.Manifests[mi].Annotations, AnnotRefName)
				}
				found = true
			} else {
				i.Manifests[mi] = i.Manifests[len(i.Manifests)-1]
				i.Manifests = i.Manifests[:len(i.Manifests)-1]
			}
		} else if d.Digest == "" && i.Manifests[mi].Annotations != nil &&
			((tag != "" && i.Manifests[mi].Annotations[AnnotRefName] == tag) ||
				(referrer != "" && i.Manifests[mi].Annotations[AnnotReferrerSubject] == referrer)) {
			// delete all entries pointing to a tag or referrer when digest isn't defined
			i.Manifests[mi] = i.Manifests[len(i.Manifests)-1]
			i.Manifests = i.Manifests[:len(i.Manifests)-1]
		}
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
	if referrer.Subject == nil || referrer.Subject.Digest == "" {
		return subject, rd, ErrNotFound
	}
	subject = *referrer.Subject
	// build descriptor, pulling up artifact type and annotations
	if referrer.MediaType != "" {
		rd.MediaType = referrer.MediaType
	}
	if rd.Digest == "" {
		rd.Digest = digest.Canonical.FromBytes(raw)
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
