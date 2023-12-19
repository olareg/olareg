package types

const (
	// AnnotRefName is the annotation key for the tag, set on a descriptor in the OCI Layout index.json.
	AnnotRefName = "org.opencontainers.image.ref.name"
	// AnnotReferrerSubject is used on descriptors that point to the referrer response in an OCI layout index.json.
	// The value is the digest of the subject.
	AnnotReferrerSubject = "org.opencontainers.image.referrer.subject"
	// AnnotReferrerConvert is set to true to indicate any referrers in the OCI Layout pushed with the fallback tag have been converted to the referrer.subject annotation.
	AnnotReferrerConvert = "org.opencontainers.image.referrer.convert"
)
