package types

// Platform specifies a platform where a particular image manifest is applicable.
type Platform struct {
	// Architecture field specifies the CPU architecture, for example `amd64` or `ppc64`.
	Architecture string `json:"architecture"`

	// OS specifies the operating system, for example `linux` or `windows`.
	OS string `json:"os"`

	// OSVersion is an optional field specifying the operating system version, for example `10.0.10586`.
	OSVersion string `json:"os.version,omitempty"`

	// OSFeatures is an optional field specifying an array of strings, each listing a required OS feature (for example on Windows `win32k`).
	OSFeatures []string `json:"os.features,omitempty"`

	// Variant is an optional field specifying a variant of the CPU, for example `ppc64le` to specify a little-endian version of a PowerPC CPU.
	Variant string `json:"variant,omitempty"`

	// Features is an optional field specifying an array of strings, each listing a required CPU feature (for example `sse4` or `aes`).
	Features []string `json:"features,omitempty"`
}

// Copy returns a memory safe copy of the Platform object
func (p Platform) Copy() Platform {
	p2 := p
	if p.OSFeatures != nil {
		p2.OSFeatures = make([]string, len(p.OSFeatures))
		copy(p2.OSFeatures, p.OSFeatures)
	}
	if p.Features != nil {
		p2.Features = make([]string, len(p.Features))
		copy(p2.Features, p.Features)
	}
	return p2
}
