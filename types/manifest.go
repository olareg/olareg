package types

import (
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

func (i *Index) AddChildren(children []Descriptor) {
	i.childManifests = append(i.childManifests, children...)
}

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
		for _, d := range i.Manifests {
			if d.Digest == dig {
				return d, nil
			}
		}
		if i.childManifests != nil {
			for _, d := range i.childManifests {
				if d.Digest == dig {
					return d, nil
				}
			}
		}
	}
	return dRet, ErrNotFound
}
