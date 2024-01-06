// Package store is used to interface with different types of storage (memory, disk)
package store

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"time"

	// imports required for go-digest
	_ "crypto/sha256"
	_ "crypto/sha512"

	"github.com/opencontainers/go-digest"

	"github.com/olareg/olareg/config"
	"github.com/olareg/olareg/internal/slog"
	"github.com/olareg/olareg/types"
)

const (
	freqCheck  = time.Second
	indexFile  = "index.json"
	layoutFile = "oci-layout"
	blobsDir   = "blobs"
	uploadDir  = "_uploads"
)

var (
	referrerTagRe = regexp.MustCompile(`^(sha256|sha512)-([0-9a-f]{64})$`)
)

type Store interface {
	RepoGet(repoStr string) (Repo, error)
}

type Repo interface {
	// IndexGet returns the current top level index for a repo.
	IndexGet() (types.Index, error)
	// IndexInsert adds a new entry to the index and writes the change to index.json.
	IndexInsert(desc types.Descriptor, opts ...types.IndexOpt) error
	// IndexRemove deletes an entry from the index and writes the change to index.json.
	IndexRemove(desc types.Descriptor) error

	// BlobGet returns a reader to an entry from the CAS.
	BlobGet(d digest.Digest) (io.ReadSeekCloser, error)
	// BlobCreate is used to create a new blob.
	BlobCreate(opts ...BlobOpt) (BlobCreator, error)
	// BlobDelete removes an entry from the CAS.
	BlobDelete(d digest.Digest) error

	// blobGet is an internal method for accessing blobs from other store methods.
	blobGet(d digest.Digest, locked bool) (io.ReadSeekCloser, error)
}

type BlobOpt func(*blobConfig)

type blobConfig struct {
	algo   digest.Algorithm
	expect digest.Digest
}

func BlobWithAlgorithm(a digest.Algorithm) BlobOpt {
	return func(bc *blobConfig) {
		bc.algo = a
	}
}

func BlobWithDigest(d digest.Digest) BlobOpt {
	return func(bc *blobConfig) {
		bc.expect = d
		bc.algo = d.Algorithm()
	}
}

// BlobCreator is used to upload new blobs.
type BlobCreator interface {
	// WriteCloser is used to push the blob content.
	io.WriteCloser
	// Cancel is used to stop an upload.
	Cancel()
	// Size reports the number of bytes pushed.
	Size() int64
	// Digest is used to get the current digest of the content.
	Digest() digest.Digest
	// Verify ensures a digest matches the content.
	Verify(digest.Digest) error
}

// Opts includes options for the directory store.
type Opts func(*storeConf)

type storeConf struct {
	log slog.Logger
}

// WithLog includes a logger on the directory store.
func WithLog(log slog.Logger) Opts {
	return func(sc *storeConf) {
		sc.log = log
	}
}

// indexIngest processes an index, adding child descriptors, and converting referrers if appropriate.
// return is true when index has been modified.
func indexIngest(repo Repo, index *types.Index, conf config.Config, locked bool) (bool, error) {
	mod := false
	// error if referrer API not enabled and annotation indicates this is already converted, this repo should not writable
	if !*conf.API.Referrer.Enabled && index.Annotations != nil && index.Annotations[types.AnnotReferrerConvert] == "true" {
		return mod, fmt.Errorf("index.json has referrers converted with the API disabled")
	}
	// ensure index has schema and media type
	if index.SchemaVersion != 2 {
		index.SchemaVersion = 2
		mod = true
	}
	if index.MediaType != types.MediaTypeOCI1ManifestList {
		index.MediaType = types.MediaTypeOCI1ManifestList
		mod = true
	}

	seen := map[digest.Digest]bool{}
	scanChildren := []types.Descriptor{}
	referrerResponse := map[string]types.Descriptor{}
	digestTags := []types.Descriptor{}
	// loop over manifests
	for _, desc := range index.Manifests {
		desc := desc
		seen[desc.Digest] = true
		if desc.MediaType == types.MediaTypeOCI1ManifestList && desc.Annotations != nil {
			if referrerTagRe.MatchString(desc.Annotations[types.AnnotRefName]) {
				digestTags = append(digestTags, desc)
			}
			if desc.Annotations[types.AnnotReferrerSubject] != "" {
				referrerResponse[desc.Annotations[types.AnnotReferrerSubject]] = desc
			}
		}
		if types.MediaTypeIndex(desc.MediaType) {
			scanChildren = append(scanChildren, desc)
		}
	}

	// convert referrers
	if *conf.API.Referrer.Enabled && (index.Annotations == nil || index.Annotations[types.AnnotReferrerConvert] != "true") {
		// for each fallback tag, validate it
		addResp := map[string][]types.Descriptor{}
		rmDesc := []types.Descriptor{}
		for _, desc := range digestTags {
			curResp, err := repoGetIndex(repo, desc, locked)
			if err != nil || curResp.Manifests == nil {
				continue
			}
			valid, refSubj, refResp := indexValidReferrer(repo, curResp, locked)
			// check for a different response already in the index
			if valid {
				if resp, ok := referrerResponse[refSubj.String()]; ok && resp.Digest != desc.Digest {
					valid = false
				}
			}
			// if the response is good, convert to a referrer
			if valid {
				newDesc := desc
				newDesc.Annotations = map[string]string{
					types.AnnotReferrerSubject: refSubj.String(),
				}
				index.AddDesc(newDesc)
				mod = true
			}
			// if the response cannot be quickly converted, save for later
			if !valid {
				for refSubj := range refResp {
					addResp[refSubj.String()] = append(addResp[refSubj.String()], refResp[refSubj]...)
				}
				rmDesc = append(rmDesc, desc)
			}
		}

		// generate new responses when needed
		for subj, respList := range addResp {
			if refDesc, ok := referrerResponse[subj]; ok {
				resp, err := repoGetIndex(repo, refDesc, locked)
				if err == nil && resp.Manifests != nil {
					respList = append(respList, resp.Manifests...)
				}
			}
			resp := types.Index{
				SchemaVersion: 2,
				MediaType:     types.MediaTypeOCI1ManifestList,
				Manifests:     referrerListDedup(respList),
			}
			respRaw, err := json.Marshal(resp)
			if err != nil {
				return mod, fmt.Errorf("failed to marshal referrers response: %w", err)
			}
			dig := digest.Canonical.FromBytes(respRaw)
			bc, err := repo.BlobCreate(BlobWithDigest(dig))
			if err != nil {
				return mod, err
			}
			_, err = bc.Write(respRaw)
			if err != nil {
				_ = bc.Close()
				return mod, err
			}
			err = bc.Close()
			if err != nil {
				return mod, err
			}
			index.AddDesc(types.Descriptor{
				MediaType: types.MediaTypeOCI1ManifestList,
				Digest:    dig,
				Size:      int64(len(respRaw)),
				Annotations: map[string]string{
					types.AnnotReferrerSubject: subj,
				},
			})
			mod = true
		}
		// cleanup processed fallback tags
		for _, d := range rmDesc {
			index.RmDesc(d)
		}
		if index.Annotations == nil {
			index.Annotations = map[string]string{types.AnnotReferrerConvert: "true"}
		} else {
			index.Annotations[types.AnnotReferrerConvert] = "true"
		}
		mod = true
	}

	// load child descriptors
	for len(scanChildren) > 0 {
		childIndex, err := repoGetIndex(repo, scanChildren[0], locked)
		if err != nil {
			scanChildren = scanChildren[1:]
			continue
		}
		if childIndex.Manifests != nil {
			for _, desc := range childIndex.Manifests {
				if !seen[desc.Digest] {
					index.AddChildren([]types.Descriptor{desc})
					if types.MediaTypeIndex(desc.MediaType) {
						scanChildren = append(scanChildren, desc)
					}
					seen[desc.Digest] = true
				}
			}
		}
		scanChildren = scanChildren[1:]
	}

	return mod, nil
}

// indexValidReferrer checks all descriptors in an index to be correct for the referrer response.
// Any entries for a different subject, or with incorrect values (pulled up artifactType and annotations) are flagged as invalid.
// The return is true for valid responses, the digest is for the subject if valid.
// The returned map is of subjects with a list of descriptors to include in the referrers response to that subject.
// Errors getting manifests are ignored and those descriptors referencing those manifests are discarded.
func indexValidReferrer(repo Repo, index types.Index, locked bool) (bool, digest.Digest, map[digest.Digest][]types.Descriptor) {
	var subject digest.Digest
	valid := true
	responses := map[digest.Digest][]types.Descriptor{}
	for _, desc := range index.Manifests {
		rdr, err := repo.blobGet(desc.Digest, locked)
		if err != nil {
			// errors result in entry being dropped from response list
			valid = false
			continue
		}
		raw, err := io.ReadAll(rdr)
		_ = rdr.Close()
		if err != nil {
			valid = false
			continue
		}
		refSubj, refDesc, err := types.ManifestReferrerDescriptor(raw, desc)
		if err != nil {
			valid = false
			continue
		}
		// ensure all referrers point to the same subject
		if subject == "" {
			subject = refSubj.Digest
		} else if subject != refSubj.Digest {
			valid = false
		}
		// ensure all descriptors match expected contents
		if valid {
			if desc.MediaType != refDesc.MediaType || desc.Size != refDesc.Size || desc.ArtifactType != refDesc.ArtifactType || len(desc.Annotations) != len(refDesc.Annotations) {
				valid = false
			} else if refDesc.Annotations != nil {
				for k, v := range refDesc.Annotations {
					if desc.Annotations[k] != v {
						valid = false
					}
				}
			}
		}
		// add descriptor to the list of referrers for this digest
		responses[refSubj.Digest] = append(responses[refSubj.Digest], refDesc)
	}
	if !valid {
		subject = ""
	}
	return valid, subject, responses
}

func layoutVerify(b []byte) bool {
	l := types.Layout{}
	err := json.Unmarshal(b, &l)
	if err != nil {
		return false
	}
	if l.Version != types.LayoutVersion {
		return false
	}
	return true
}

func referrerListDedup(rl []types.Descriptor) []types.Descriptor {
	if rl == nil {
		return nil
	}
	seen := map[digest.Digest]bool{}
	i := 0
	for i < len(rl) {
		if seen[rl[i].Digest] {
			// delete entry from slice
			rl[i] = rl[len(rl)-1]
			rl = rl[:len(rl)-1]
			continue
		}
		seen[rl[i].Digest] = true
		i++
	}
	return rl
}

func repoGetIndex(repo Repo, d types.Descriptor, locked bool) (types.Index, error) {
	i := types.Index{}
	rdr, err := repo.blobGet(d.Digest, locked)
	if err != nil {
		return i, err
	}
	err = json.NewDecoder(rdr).Decode(&i)
	_ = rdr.Close()
	if err != nil {
		return i, err
	}
	return i, nil
}
