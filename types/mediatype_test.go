package types

import "testing"

func TestMediaTypeBase(t *testing.T) {
	t.Parallel()
	tt := []struct {
		name   string
		orig   string
		expect string
	}{
		{
			name:   "OCI Index",
			orig:   MediaTypeOCI1ManifestList,
			expect: MediaTypeOCI1ManifestList,
		},
		{
			name:   "OCI Index with charset",
			orig:   "application/vnd.oci.image.index.v1+json; charset=utf-8",
			expect: MediaTypeOCI1ManifestList,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			result := MediaTypeBase(tc.orig)
			if tc.expect != result {
				t.Errorf("invalid result: expected \"%s\", received \"%s\"", tc.expect, result)
			}
		})
	}
}

func TestMediaTypeAccepts(t *testing.T) {
	t.Parallel()
	tt := []struct {
		name    string
		mt      string
		accepts []string
		expect  bool
	}{
		{
			name:    "Empty",
			mt:      MediaTypeOCI1ManifestList,
			accepts: []string{},
			expect:  true,
		},
		{
			name:    "Single",
			mt:      MediaTypeOCI1ManifestList,
			accepts: []string{MediaTypeOCI1ManifestList},
			expect:  true,
		},
		{
			name:    "Mismatch",
			mt:      MediaTypeOCI1Manifest,
			accepts: []string{MediaTypeOCI1ManifestList},
			expect:  false,
		},
		{
			name:    "Multiple",
			mt:      MediaTypeOCI1ManifestList,
			accepts: []string{MediaTypeOCI1Manifest, MediaTypeOCI1ManifestList, MediaTypeDocker2Manifest, MediaTypeDocker2ManifestList},
			expect:  true,
		},
		{
			name:    "Comma separated",
			mt:      MediaTypeOCI1ManifestList,
			accepts: []string{"application/vnd.oci.image.manifest.v1+json; charset=utf-8, application/vnd.oci.image.index.v1+json; charset=utf-8"},
			expect:  true,
		},
		{
			name:    "Wildcard",
			mt:      MediaTypeOCI1ManifestList,
			accepts: []string{"*/*"},
			expect:  true,
		},
		{
			name:    "Partial wildcard",
			mt:      MediaTypeOCI1ManifestList,
			accepts: []string{"application/*"},
			expect:  true,
		},
		{
			name:    "Invalid wildcard",
			mt:      MediaTypeOCI1ManifestList,
			accepts: []string{"*/vnd.oci.image.index.v1+json"},
			expect:  false,
		},
		{
			name:    "Mismatch wildcard",
			mt:      MediaTypeOCI1ManifestList,
			accepts: []string{"text/*"},
			expect:  false,
		},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			result := MediaTypeAccepts(tc.mt, tc.accepts)
			if tc.expect != result {
				t.Errorf("invalid result: expected %t, received %t", tc.expect, result)
			}
		})
	}
}
