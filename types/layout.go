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

const (
	// LayoutVersion is the supported release of the OCI Layout file definition.
	LayoutVersion = "1.0.0"
)

// Layout is the JSON contents of the oci-layout file.
type Layout struct {
	// Version is the implemented OCI Layout version in a given directory.
	Version string `json:"imageLayoutVersion"`
}
