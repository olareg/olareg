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
	// AnnotCreated is the date and time of the descriptor, conforming to RFC 3339
	AnnotCreated = "org.opencontainers.image.created"
	// AnnotRefName is the annotation key for the tag, set on a descriptor in the OCI Layout index.json.
	AnnotRefName = "org.opencontainers.image.ref.name"
	// AnnotReferrerSubject is used on descriptors that point to the referrer response in an OCI layout index.json.
	// The value is the digest of the subject.
	AnnotReferrerSubject = "org.olareg.referrer.subject"
	// AnnotReferrerConvert is set to true to indicate any referrers in the OCI Layout pushed with the fallback tag have been converted to the referrer.subject annotation.
	AnnotReferrerConvert = "org.olareg.referrer.convert"
)
