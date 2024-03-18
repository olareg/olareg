package olareg

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/opencontainers/go-digest"

	"github.com/olareg/olareg/internal/store"
	"github.com/olareg/olareg/types"
)

const (
	referrerFilterATParam       = "artifactType"
	referrerFilterATHeaderKey   = "OCI-Filters-Applied"
	referrerFilterATHeaderValue = "artifactType"
)

// referrerGet searches for the referrers response in the index.
// All errors should return an empty response, no 404's should be generated.
func (s *Server) referrerGet(repoStr, arg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// most errors should return an empty index
		i := types.Index{
			SchemaVersion: 2,
			MediaType:     types.MediaTypeOCI1ManifestList,
		}
		repo, err := s.store.RepoGet(repoStr)
		if err != nil {
			w.Header().Add("content-type", types.MediaTypeOCI1ManifestList)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(i)
			if !errors.Is(err, types.ErrRepoNotAllowed) {
				s.log.Info("failed to get repo", "err", err, "repo", repoStr, "arg", arg)
			}
			return
		}
		defer repo.Done()
		index, err := repo.IndexGet()
		if err != nil {
			w.Header().Add("content-type", types.MediaTypeOCI1ManifestList)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(i)
			return
		}
		if index.Annotations == nil || index.Annotations[types.AnnotReferrerConvert] != "true" {
			// referrers are not enabled for this repo, this is the one case for a 404
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Add("content-type", types.MediaTypeOCI1ManifestList)
		d, err := index.GetByAnnotation(types.AnnotReferrerSubject, arg)
		if err != nil {
			// not found, empty response
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(i)
			return
		}
		rdr, err := repo.BlobGet(d.Digest)
		if err != nil {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(i)
			s.log.Info("failed to get referrers response", "err", err, "repo", repoStr, "arg", arg, "digest", d.Digest.String())
			return
		}
		defer rdr.Close()
		var out io.Reader
		out = rdr
		filterAT := r.URL.Query().Get(referrerFilterATParam)
		if filterAT != "" {
			out, err = referrerFilter(out, filterAT)
			if err != nil {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(i)
				s.log.Info("failed to filter referrers response", "err", err, "repo", repoStr, "arg", arg, "digest", d.Digest.String())
				return
			}
			w.Header().Add(referrerFilterATHeaderKey, referrerFilterATHeaderValue)
		}
		w.WriteHeader(http.StatusOK)
		_, err = io.Copy(w, out)
		if err != nil {
			s.log.Info("failed to write referrers response", "err", err, "repo", repoStr, "arg", arg)
		}
	}
}

// referrerFilter applies a filter to the returned descriptor list
func referrerFilter(rdr io.Reader, filterArtifactType string) (io.Reader, error) {
	in := types.Index{}
	err := json.NewDecoder(rdr).Decode(&in)
	if err != nil {
		return nil, err
	}
	out := types.Index{
		SchemaVersion: in.SchemaVersion,
		MediaType:     in.MediaType,
		ArtifactType:  in.ArtifactType,
		Subject:       in.Subject,
		Annotations:   in.Annotations,
	}
	out.Manifests = make([]types.Descriptor, 0, len(in.Manifests))
	for _, d := range in.Manifests {
		if d.ArtifactType == filterArtifactType {
			out.Manifests = append(out.Manifests, d)
		}
	}
	outBytes, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(outBytes), nil
}

// referrerAdd adds a new referrer entry to a given subject.
func (s *Server) referrerAdd(repo store.Repo, subject digest.Digest, desc types.Descriptor) error {
	index, err := repo.IndexGet()
	if err != nil {
		return err
	}
	refResp := types.Index{
		SchemaVersion: 2,
		MediaType:     types.MediaTypeOCI1ManifestList,
	}
	// existing referrer response exists to update/replace, use that to populate index
	// all errors reading existing referrers result in defaulting to an initial empty response
	if dOld, err := index.GetByAnnotation(types.AnnotReferrerSubject, subject.String()); err == nil {
		func() {
			rdr, err := repo.BlobGet(dOld.Digest)
			if err != nil {
				return
			}
			iRaw, err := io.ReadAll(rdr)
			_ = rdr.Close()
			if err != nil {
				return
			}
			err = json.Unmarshal(iRaw, &refResp)
			if err != nil {
				return
			}
		}()
	}
	// add descriptor to index and push into blob store
	refResp.AddDesc(desc)
	iRaw, err := json.Marshal(refResp)
	if err != nil {
		return err
	}
	dig := digest.Canonical.FromBytes(iRaw)
	bc, _, err := repo.BlobCreate(store.BlobWithDigest(dig))
	if err != nil && !errors.Is(err, types.ErrBlobExists) {
		return err
	}
	if err == nil {
		_, err = bc.Write(iRaw)
		if err != nil {
			_ = bc.Close()
			return err
		}
		err = bc.Close()
		if err != nil {
			return err
		}
	}
	// create new descriptor for referrers response to add into index.json
	dNew := types.Descriptor{
		MediaType: types.MediaTypeOCI1ManifestList,
		Size:      int64(len(iRaw)),
		Digest:    dig,
		Annotations: map[string]string{
			types.AnnotReferrerSubject: subject.String(),
		},
	}
	// adding the new response also deletes the previous response
	err = repo.IndexInsert(dNew, types.IndexWithChildren(refResp.Manifests))
	if err != nil {
		return err
	}
	return nil
}

// referrerDelete removes a referrer entry from a subject.
func (s *Server) referrerDelete(repo store.Repo, subject digest.Digest, desc types.Descriptor) error {
	// get the index.json
	index, err := repo.IndexGet()
	if err != nil {
		return err
	}
	// search for matching referrer descriptor
	dOld, err := index.GetByAnnotation(types.AnnotReferrerSubject, subject.String())
	if err != nil {
		if errors.Is(types.ErrNotFound, err) {
			return nil
		}
		return err
	}
	// read the old referrer response into an index
	rdr, err := repo.BlobGet(dOld.Digest)
	if err != nil {
		return err
	}
	refRespRaw, err := io.ReadAll(rdr)
	_ = rdr.Close()
	if err != nil {
		return err
	}
	refResp := types.Index{}
	err = json.Unmarshal(refRespRaw, &refResp)
	if err != nil {
		return err
	}
	// remove descriptor from response
	refResp.RmDesc(desc)
	// push response back to blob store with a new digest
	refRespRaw, err = json.Marshal(refResp)
	if err != nil {
		return err
	}
	dig := digest.Canonical.FromBytes(refRespRaw)
	bc, _, err := repo.BlobCreate(store.BlobWithDigest(dig))
	if err != nil && !errors.Is(err, types.ErrBlobExists) {
		return err
	}
	if err == nil {
		_, err = bc.Write(refRespRaw)
		if err != nil {
			_ = bc.Close()
			return err
		}
		err = bc.Close()
		if err != nil {
			return err
		}
	}
	// create new descriptor for referrers response
	dNew := types.Descriptor{
		MediaType: types.MediaTypeOCI1ManifestList,
		Size:      int64(len(refRespRaw)),
		Digest:    dig,
		Annotations: map[string]string{
			types.AnnotReferrerSubject: subject.String(),
		},
	}
	// adding the new response also deletes the previous response
	err = repo.IndexInsert(dNew, types.IndexWithChildren(refResp.Manifests))
	if err != nil {
		return err
	}
	return nil
}
