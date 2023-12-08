package types

import "regexp"

var (
	// RefTagRE is a regexp for a valid tag.
	RefTagRE = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}$`)
)
