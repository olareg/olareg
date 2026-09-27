// Copyright the olareg contributors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package types

import (
	"encoding/json"
	"fmt"
	"io"
)

const (
	// LayoutVersion is the supported release of the OCI Layout file definition.
	LayoutVersion = "1.0.0"
)

// Layout is the JSON contents of the oci-layout file.
type Layout struct {
	// Version is the implemented OCI Layout version in a given directory.
	Version string `json:"imageLayoutVersion"`
}

// LayoutIndex is an extended Index with added fields for the index.json file.
type LayoutIndex struct {
	Index

	OlaregExt LayoutOlaregExt `json:"org.olareg"` // extensions for olareg
}

type LayoutOlaregExt struct {
	ChildManifests bool `json:"childManifests,omitempty"` // if child manifests are listed in the index
	Referrers      bool `json:"referrers,omitempty"`      // if referrers have been converted
	TagHistory     bool `json:"tagHistory,omitempty"`     // if tag history has been converted
}

// Copy returns a deep copy of the index to avoid data races
func (li LayoutIndex) Copy() LayoutIndex {
	li2 := li
	li2.Index = li.Index.Copy()
	return li2
}

// LayoutVerify checks for a valid OCI Layout file.
func LayoutVerify(rdr io.Reader) error {
	l := Layout{}
	err := json.NewDecoder(rdr).Decode(&l)
	if err != nil {
		return fmt.Errorf("failed to parse layout file: %w", err)
	}
	// TODO: add semver parsing of the version
	if l.Version != LayoutVersion {
		return fmt.Errorf("unsupported layout version %s", l.Version)
	}
	return nil
}
