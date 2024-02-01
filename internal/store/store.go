// Package store is used to interface with different types of storage (memory, disk)
package store

import (
	"crypto/rand"
	"encoding/base64"
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

// Store interface is used to abstract access to a backend storage system for repositories.
type Store interface {
	// RepoGet returns a repo from the store.
	// When finished, the method [Repo.Done] must be called.
	RepoGet(repoStr string) (Repo, error)

	// Close releases resources used by the store.
	// The store should not be used after being closed.
	Close() error
}

// Repo interface is used to access a CAS and the index managing known manifests.
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
	BlobCreate(opts ...BlobOpt) (BlobCreator, string, error)
	// BlobDelete removes an entry from the CAS.
	BlobDelete(d digest.Digest) error
	// BlobSession is used to retrieve an upload session
	BlobSession(sessionID string) (BlobCreator, error)

	// Done indicates the routine using this repo is finished.
	// This must be called exactly once for every instance of [Store.RepoGet].
	Done()

	// blobDelete is the internal method for deleting a blob.
	blobDelete(d digest.Digest, locked bool) error
	// blobGet is an internal method for accessing blobs from other store methods.
	blobGet(d digest.Digest, locked bool) (io.ReadSeekCloser, error)
	// blobList returns a list of all known blobs in the repo
	blobList(locked bool) ([]digest.Digest, error)
	// blobMeta returns metadata on a blob.
	blobMeta(d digest.Digest, locked bool) (blobMeta, error)
	// gc runs the garbage collect
	gc() error
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

// blobMeta includes metadata available for blobs.
type blobMeta struct {
	mod time.Time
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

// genSessionID returns a random ID safe for use in a URL.
func genSessionID() (string, error) {
	sb := make([]byte, 16)
	_, err := rand.Read(sb)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(sb), nil
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
			bc, _, err := repo.BlobCreate(BlobWithDigest(dig))
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

// repoGarbageCollect runs a GC against the repo.
// The repo should be locked before calling this.
// Changes to the index will be returned and should be saved to the store.
func repoGarbageCollect(repo Repo, conf config.Config, index types.Index, locked bool) (types.Index, bool, error) {
	var cutoff time.Time
	if conf.Storage.GC.GracePeriod >= 0 {
		cutoff = time.Now().Add(conf.Storage.GC.GracePeriod * -1)
	}
	manifests := make([]types.Descriptor, 0, len(index.Manifests))
	subjects := map[digest.Digest]types.Descriptor{}
	inIndex := map[digest.Digest]bool{}
	// build a list of manifests and subjects to scan
	for _, d := range index.Manifests {
		inIndex[d.Digest] = true
		keep := false
		// keep tagged entries or every entry if untagged entries are not GCed
		if !*conf.Storage.GC.Untagged || (d.Annotations != nil && d.Annotations[types.AnnotRefName] != "") {
			keep = true
		}
		// keep new blobs
		if !keep && conf.Storage.GC.GracePeriod >= 0 {
			if meta, err := repo.blobMeta(d.Digest, locked); err == nil && meta.mod.After(cutoff) {
				keep = true
			}
		}
		// referrers responses
		if d.Annotations != nil && d.Annotations[types.AnnotReferrerSubject] != "" {
			dig, _ := digest.Parse(d.Annotations[types.AnnotReferrerSubject])
			subjExists := (dig != "")
			if _, err := repo.blobMeta(dig, locked); subjExists && err != nil {
				subjExists = false
			}
			if *conf.Storage.GC.ReferrersWithSubj && subjExists {
				// track a map of responses only preserved when their subject remains
				subjects[dig] = d.Copy()
				keep = false
			} else if !*conf.Storage.GC.ReferrersDangling {
				// keep if dangling aren't GCed
				keep = true
			} else if subjExists {
				// subject exists but need to delete dangling
				if meta, err := repo.blobMeta(d.Digest, locked); err == nil && conf.Storage.GC.GracePeriod >= 0 && meta.mod.After(cutoff) {
					// always keep new entries
					keep = true
				} else {
					// else preserve only if subject remains
					subjects[dig] = d.Copy()
					keep = false
				}
			}
		}
		if keep {
			manifests = append(manifests, d.Copy())
		}
	}
	seen := map[digest.Digest]bool{}
	// walk all manifests to note seen digests
	for len(manifests) > 0 {
		// work from tail to make deletes easier
		d := manifests[len(manifests)-1]
		manifests = manifests[:len(manifests)-1]
		inIndex[d.Digest] = true
		if seen[d.Digest] {
			continue
		}
		br, err := repo.blobGet(d.Digest, locked)
		if err != nil {
			continue
		}
		seen[d.Digest] = true
		// parse manifests for descriptors (manifests, config, layers)
		if types.MediaTypeIndex(d.MediaType) {
			man := types.Index{}
			err = json.NewDecoder(br).Decode(&man)
			errClose := br.Close()
			if err != nil || errClose != nil {
				continue
			}
			for _, child := range man.Manifests {
				manifests = append(manifests, child.Copy())
			}
		} else if types.MediaTypeImage(d.MediaType) {
			man := types.Manifest{}
			err = json.NewDecoder(br).Decode(&man)
			errClose := br.Close()
			if err != nil || errClose != nil {
				continue
			}
			seen[man.Config.Digest] = true
			for _, layer := range man.Layers {
				seen[layer.Digest] = true
			}
		}
		// if there are referrers to this manifest
		if referrer, ok := subjects[d.Digest]; ok {
			manifests = append(manifests, referrer)
		}
	}
	// clean old blobs that were not seen
	mod := false
	blobExists := map[digest.Digest]bool{}
	dl, err := repo.blobList(locked)
	if err != nil {
		return index, false, fmt.Errorf("failed to list blobs to GC: %w", err)
	}
	for _, d := range dl {
		blobExists[d] = true
		if seen[d] {
			continue
		}
		bInfo, errMeta := repo.blobMeta(d, locked)
		if errMeta == nil && conf.Storage.GC.GracePeriod >= 0 && bInfo.mod.After(cutoff) && !inIndex[d] {
			// keep recently uploaded blobs (manifests handled above)
			continue
		}
		// prune from index, check existence directly since some index entries may not be accessible
		if _, err := index.GetDesc(d.String()); err == nil {
			mod = true
			index.RmDesc(types.Descriptor{Digest: d})
		}
		// attempt to prune from blob store, ignoring errors
		_ = repo.blobDelete(d, locked)
	}
	// cleanup index entries without a backing blob
	for d := range inIndex {
		if !blobExists[d] {
			mod = true
			index.RmDesc(types.Descriptor{Digest: d})
		}
	}
	return index, mod, nil
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
