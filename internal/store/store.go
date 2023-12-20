// Package store is used to interface with different types of storage (memory, disk)
package store

import (
	"encoding/json"
	"io"
	"regexp"

	// imports required for go-digest
	_ "crypto/sha256"
	_ "crypto/sha512"

	"github.com/opencontainers/go-digest"

	"github.com/olareg/olareg/internal/slog"
	"github.com/olareg/olareg/types"
)

type Store interface {
	RepoGet(repoStr string) (Repo, error)
}

type Repo interface {
	// IndexGet returns the current top level index for a repo.
	IndexGet() (types.Index, error)
	// IndexAdd adds a new entry to the index and writes the change to index.json.
	IndexAdd(desc types.Descriptor, opts ...types.IndexOpt) error
	// IndexRm removes an entry from the index and writes the change to index.json.
	IndexRm(desc types.Descriptor) error

	// BlobGet returns a reader to an entry from the CAS.
	BlobGet(d digest.Digest) (io.ReadSeekCloser, error)
	// BlobCreate is used to create a new blob.
	BlobCreate(opts ...BlobOpt) (BlobCreator, error)
	// BlobDelete deletes an entry from the CAS.
	BlobDelete(d digest.Digest) error
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

var (
	referrerTagRe = regexp.MustCompile(`^(sha256|sha512)-([0-9a-f]{64})$`)
)

// referrerConvert runs when repo loaded to ensure fallback tags have been have been converted to referrers.
// return bool value indicates if index was changed.
func referrerConvert(repo Repo, index *types.Index) (bool, error) {
	// if index already converted, return
	if index.Annotations != nil && index.Annotations[types.AnnotReferrerConvert] == "true" {
		return false, nil
	}
	// gather a list of descriptors to convert and already converted referrers
	convertList := []types.Descriptor{}
	referrerList := map[string]types.Descriptor{}
	for _, d := range index.Manifests {
		if d.MediaType == types.MediaTypeOCI1ManifestList && d.Annotations != nil {
			if referrerTagRe.MatchString(d.Annotations[types.AnnotRefName]) {
				convertList = append(convertList, d)
			}
			if d.Annotations[types.AnnotReferrerSubject] != "" {
				referrerList[d.Annotations[types.AnnotReferrerSubject]] = d
			}
		}
	}
	// if no entries to convert, mark index as done
	if len(convertList) == 0 {
		if index.Annotations == nil {
			index.Annotations = map[string]string{types.AnnotReferrerConvert: "true"}
		} else {
			index.Annotations[types.AnnotReferrerConvert] = "true"
		}
		return true, nil
	}
	// convert each referrer
	referrerAdd := map[digest.Digest][]types.Descriptor{}
	rmLater := []types.Descriptor{}
	for _, d := range convertList {
		// get each manifest, parse for subject
		i, err := repoGetIndex(repo, d)
		if err != nil || i.Manifests == nil {
			continue
		}
		var subject digest.Digest
		quickConvert := true
		for _, id := range i.Manifests {
			rdr, err := repo.BlobGet(id.Digest)
			if err != nil {
				// errors result in entry being dropped from response list
				quickConvert = false
				continue
			}
			raw, err := io.ReadAll(rdr)
			_ = rdr.Close()
			if err != nil {
				quickConvert = false
				continue
			}
			curSubject, refDesc, err := types.ManifestReferrerDescriptor(raw, id)
			if err != nil {
				quickConvert = false
				continue
			}
			// ensure all referrers point to the same subject
			if subject == "" {
				subject = curSubject.Digest
			} else if subject != curSubject.Digest {
				quickConvert = false
			}
			// ensure all descriptors match expected contents
			if quickConvert {
				if id.MediaType != refDesc.MediaType || id.Size != refDesc.Size || id.ArtifactType != refDesc.ArtifactType || len(id.Annotations) != len(refDesc.Annotations) {
					quickConvert = false
				} else if refDesc.Annotations != nil {
					for k, v := range refDesc.Annotations {
						if id.Annotations[k] != v {
							quickConvert = false
						}
					}
				}
			}
			// add descriptor to the list of referrers for this digest
			referrerAdd[curSubject.Digest] = append(referrerAdd[curSubject.Digest], refDesc)
		}
		// if there is also an entry for the same subject using the referrer annotation, to a different response
		if rld, ok := referrerList[subject.String()]; ok && rld.Digest != d.Digest {
			quickConvert = false
			i, err := repoGetIndex(repo, rld)
			if err == nil && i.Manifests != nil {
				referrerAdd[subject] = append(referrerAdd[subject], i.Manifests...)
			}
		}
		if quickConvert {
			// well formed referrer response, just need to update the descriptor in the index
			newD := d
			newD.Annotations = map[string]string{
				types.AnnotReferrerSubject: subject.String(),
			}
			index.AddDesc(newD)
			delete(referrerAdd, subject)
		} else {
			rmLater = append(rmLater, d)
		}
	}
	// for each referrerAdd subject, build a new index with descriptor list
	for subject, rl := range referrerAdd {
		referrerListDedup(rl)
		i := types.Index{
			SchemaVersion: 2,
			MediaType:     types.MediaTypeOCI1ManifestList,
			Manifests:     rl,
		}
		iRaw, err := json.Marshal(i)
		if err != nil {
			return true, err
		}
		dig := digest.Canonical.FromBytes(iRaw)
		bc, err := repo.BlobCreate(BlobWithDigest(dig))
		if err != nil {
			return true, err
		}
		_, err = bc.Write(iRaw)
		if err != nil {
			_ = bc.Close()
			return true, err
		}
		err = bc.Close()
		if err != nil {
			return true, err
		}
		index.AddDesc(types.Descriptor{
			MediaType: types.MediaTypeOCI1ManifestList,
			Digest:    dig,
			Size:      int64(len(iRaw)),
			Annotations: map[string]string{
				types.AnnotReferrerSubject: subject.String(),
			},
		})
	}
	// prune digest tags that were converted to new descriptors
	for _, d := range rmLater {
		index.RmDesc(d)
	}
	// mark index as being converted
	if index.Annotations == nil {
		index.Annotations = map[string]string{types.AnnotReferrerConvert: "true"}
	} else {
		index.Annotations[types.AnnotReferrerConvert] = "true"
	}
	return true, nil
}

func referrerListDedup(rl []types.Descriptor) {
	if rl == nil {
		return
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
}

func repoGetIndex(repo Repo, d types.Descriptor) (types.Index, error) {
	i := types.Index{}
	rdr, err := repo.BlobGet(d.Digest)
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

func boolDefault(b *bool, def bool) bool {
	if b != nil {
		return *b
	}
	return def
}
