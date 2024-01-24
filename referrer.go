package olareg

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/opencontainers/go-digest"

	"github.com/olareg/olareg/internal/store"
	"github.com/olareg/olareg/types"
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
		w.WriteHeader(http.StatusOK)
		defer rdr.Close()
		_, err = io.Copy(w, rdr)
		if err != nil {
			s.log.Info("failed to write referrers response", "err", err, "repo", repoStr, "arg", arg)
		}
	}
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
	dOld, err := index.GetByAnnotation(types.AnnotReferrerSubject, subject.String())
	if err == nil {
		rdr, err := repo.BlobGet(dOld.Digest)
		if err != nil {
			return err
		}
		iRaw, err := io.ReadAll(rdr)
		_ = rdr.Close()
		if err != nil {
			return err
		}
		err = json.Unmarshal(iRaw, &refResp)
		if err != nil {
			return err
		}
	}
	// add descriptor to index and push into blob store
	refResp.AddDesc(desc)
	iRaw, err := json.Marshal(refResp)
	if err != nil {
		return err
	}
	dig := digest.Canonical.FromBytes(iRaw)
	bc, err := repo.BlobCreate(store.BlobWithDigest(dig))
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
	bc, err := repo.BlobCreate(store.BlobWithDigest(dig))
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
