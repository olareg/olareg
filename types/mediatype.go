package types

import "strings"

const (
	// MediaTypeDocker2Manifest is the media type when pulling manifests from a v2 registry.
	MediaTypeDocker2Manifest = "application/vnd.docker.distribution.manifest.v2+json"
	// MediaTypeDocker2ManifestList is the media type when pulling a manifest list from a v2 registry.
	MediaTypeDocker2ManifestList = "application/vnd.docker.distribution.manifest.list.v2+json"
	// MediaTypeDocker2ImageConfig is for the configuration json object media type.
	MediaTypeDocker2ImageConfig = "application/vnd.docker.container.image.v1+json"
	// MediaTypeOCI1Manifest OCI v1 manifest media type.
	MediaTypeOCI1Manifest = "application/vnd.oci.image.manifest.v1+json"
	// MediaTypeOCI1ManifestList OCI v1 manifest list media type.
	MediaTypeOCI1ManifestList = "application/vnd.oci.image.index.v1+json"
	// MediaTypeOCI1ImageConfig OCI v1 configuration json object media type.
	MediaTypeOCI1ImageConfig = "application/vnd.oci.image.config.v1+json"
	// MediaTypeDocker2ForeignLayer is the default compressed layer for foreign layers in docker schema2.
	MediaTypeDocker2ForeignLayer = "application/vnd.docker.image.rootfs.foreign.diff.tar.gzip"
	// MediaTypeOCI1ForeignLayer is the foreign layer for OCI v1.
	MediaTypeOCI1ForeignLayer = "application/vnd.oci.image.layer.nondistributable.v1.tar"
	// MediaTypeOCI1ForeignLayerGzip is the gzip compressed foreign layer for OCI v1.
	MediaTypeOCI1ForeignLayerGzip = "application/vnd.oci.image.layer.nondistributable.v1.tar+gzip"
	// MediaTypeOCI1ForeignLayerZstd is the zstd compressed foreign layer for OCI v1.
	MediaTypeOCI1ForeignLayerZstd = "application/vnd.oci.image.layer.nondistributable.v1.tar+zstd"
)

// MediaTypeBase cleans the Content-Type header to return only the lower case base media type.
func MediaTypeBase(orig string) string {
	base, _, _ := strings.Cut(orig, ";")
	return strings.TrimSpace(strings.ToLower(base))
}

// MediaTypeAccepts returns true when the descriptor is listed in the accept list.
func MediaTypeAccepts(mt string, accepts []string) bool {
	for _, a := range accepts {
		if MediaTypeBase(a) == mt {
			return true
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
