package types

import (
	// imports required for go-digest
	_ "crypto/sha256"
	_ "crypto/sha512"

	"github.com/opencontainers/go-digest"
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
