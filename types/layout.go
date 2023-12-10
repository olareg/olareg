package types

const (
	// LayoutVersion is the supported release of the OCI Layout file definition.
	LayoutVersion = "1.0.0"
)

// Layout is the JSON contents of the oci-layout file.
type Layout struct {
	// Version is the implemented OCI Layout version in a given directory.
	Version string `json:"imageLayoutVersion"`
}
