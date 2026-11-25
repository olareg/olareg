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

import "regexp"

var pathPart = `[a-z0-9]+(?:(?:\.|_|__|-+)[a-z0-9]+)*`

var (
	// PathPartRE is regexp for a subdirectory in a repository name
	PathPartRE = regexp.MustCompile(`^` + pathPart + `$`)
	// PathRE is the regexp for a full repository path
	PathRE = regexp.MustCompile(`^` + pathPart + `(?:\/` + pathPart + `)*$`)
	// RefTagRE is a regexp for a valid tag.
	RefTagRE = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}$`)
	// ReferrerTagRE is the syntax for fallback tags.
	// Algorithm is truncated to 32 characters, and encoding is truncated to 64.
	// This regexp is over simplified to only support sha256 and sha512 algorithms to avoid false positives.
	ReferrerTagRE = regexp.MustCompile(`^(sha256|sha512)-([0-9a-f]{64})$`)
)
