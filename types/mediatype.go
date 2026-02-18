package types

import (
	"encoding/json"
	"strings"
)

const (
	// MediaTypeDockerPrefix is used to identify Docker media types.
	MediaTypeDockerPrefix = "application/vnd.docker."
	// MediaTypeDocker2Manifest is the media type when pulling manifests from a v2 registry.
	MediaTypeDocker2Manifest = "application/vnd.docker.distribution.manifest.v2+json"
	// MediaTypeDocker2ManifestList is the media type when pulling a manifest list from a v2 registry.
	MediaTypeDocker2ManifestList = "application/vnd.docker.distribution.manifest.list.v2+json"
	// MediaTypeDocker2ImageConfig is for the configuration json object media type.
	MediaTypeDocker2ImageConfig = "application/vnd.docker.container.image.v1+json"
	// MediaTypeOCIPrefix is used to identify OCI media types.
	MediaTypeOCIPrefix = "application/vnd.oci."
	// MediaTypeOCI1Manifest OCI v1 manifest media type.
	MediaTypeOCI1Manifest = "application/vnd.oci.image.manifest.v1+json"
	// MediaTypeOCI1ManifestList OCI v1 manifest list media type.
	MediaTypeOCI1ManifestList = "application/vnd.oci.image.index.v1+json"
	// MediaTypeOCI1ImageConfig OCI v1 configuration json object media type.
	MediaTypeOCI1ImageConfig = "application/vnd.oci.image.config.v1+json"
	// MediaTypeOCI1Layer is the uncompressed layer for OCIv1.
	MediaTypeOCI1Layer = "application/vnd.oci.image.layer.v1.tar"
	// MediaTypeOCI1LayerGzip is the gzip compressed layer for OCI v1.
	MediaTypeOCI1LayerGzip = "application/vnd.oci.image.layer.v1.tar+gzip"
	// MediaTypeDocker2ForeignLayer is the default compressed layer for foreign layers in docker schema2.
	MediaTypeDocker2ForeignLayer = "application/vnd.docker.image.rootfs.foreign.diff.tar.gzip"
	// MediaTypeOCI1ForeignLayer is the foreign layer for OCI v1.
	MediaTypeOCI1ForeignLayer = "application/vnd.oci.image.layer.nondistributable.v1.tar"
	// MediaTypeOCI1ForeignLayerGzip is the gzip compressed foreign layer for OCI v1.
	MediaTypeOCI1ForeignLayerGzip = "application/vnd.oci.image.layer.nondistributable.v1.tar+gzip"
	// MediaTypeOCI1ForeignLayerZstd is the zstd compressed foreign layer for OCI v1.
	MediaTypeOCI1ForeignLayerZstd = "application/vnd.oci.image.layer.nondistributable.v1.tar+zstd"
	// MediaTypeOCI1Empty is used for blobs containing the empty JSON data `{}`.
	MediaTypeOCI1Empty = "application/vnd.oci.empty.v1+json"
)

// MediaTypeBase cleans the Content-Type header to return only the lower case base media type.
func MediaTypeBase(orig string) string {
	base, _, _ := strings.Cut(orig, ";")
	return strings.TrimSpace(strings.ToLower(base))
}

// MediaTypeAccepts returns true when the descriptor is listed in the accept list.
func MediaTypeAccepts(mt string, accepts []string) bool {
	for _, a := range accepts {
		for entry := range strings.SplitSeq(a, ",") {
			if MediaTypeBase(entry) == mt {
				return true
			}
		}
	}
	return false
}

// MediaTypeForeign returns true if the media type is a known foreign layer value.
func MediaTypeForeign(mt string) bool {
	switch mt {
	case MediaTypeDocker2ForeignLayer,
		MediaTypeOCI1ForeignLayer,
		MediaTypeOCI1ForeignLayerGzip,
		MediaTypeOCI1ForeignLayerZstd:
		return true
	}
	return false
}

// MediaTypeIndex returns true if the media type is an Index/ManifestList.
func MediaTypeIndex(mt string) bool {
	switch mt {
	case MediaTypeDocker2ManifestList, MediaTypeOCI1ManifestList:
		return true
	}
	return false
}

// MediaTypeImage returns true if the media type is an Image Manifest.
func MediaTypeImage(mt string) bool {
	switch mt {
	case MediaTypeDocker2Manifest, MediaTypeOCI1Manifest:
		return true
	}
	return false
}

// mtDetect is a combination of index and image manifest contents.
// The fields JSON is able to unmarshal help detect the media type of the raw manifest.
type mtDetect struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType,omitempty"`
	Manifests     []Descriptor `json:"manifests"`
	Config        Descriptor   `json:"config"`
	Layers        []Descriptor `json:"layers"`
}

// MediaTypeDetect determines the most likely media type for a raw manifest.
// The returned string is empty if detection fails.
func MediaTypeDetect(raw []byte) string {
	m := mtDetect{}
	err := json.Unmarshal(raw, &m)
	if err != nil {
		return ""
	}
	// MediaType should be defined which makes this easy
	if m.MediaType != "" {
		return m.MediaType
	}
	// Index/ManifestList will have the Manifests slice defined
	if len(m.Manifests) > 0 {
		if strings.HasPrefix(m.Manifests[0].MediaType, MediaTypeDockerPrefix) {
			return MediaTypeDocker2ManifestList
		}
		return MediaTypeOCI1ManifestList
	}
	// Fall back to image manifest with a config and layers.
	// Both are required in v2, but could be missing from bad clients.
	if m.Config.MediaType == "" {
		return "" // may be docker schema v1 which isn't supported
	}
	if strings.HasPrefix(m.Config.MediaType, MediaTypeDockerPrefix) {
		return MediaTypeDocker2Manifest
	}
	return MediaTypeOCI1Manifest
}
