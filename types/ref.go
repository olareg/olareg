package types

import "regexp"

// RefTagRE is a regexp for a valid tag.
var RefTagRE = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}$`)
