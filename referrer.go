package olareg

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/opencontainers/go-digest"

	"github.com/olareg/olareg/internal/store"
	"github.com/olareg/olareg/types"
)

const (
	referrerFilterATParam       = "artifactType"
	referrerFilterATHeaderKey   = "OCI-Filters-Applied"
	referrerFilterATHeaderValue = "artifactType"
)

type referrerKey struct {
	dig          digest.Digest
	artifactType string
}

type referrerResponses [][]byte

// referrerGet searches for the referrers response in the index.
// All errors should return an empty response, no 404's should be generated.
func (s *Server) referrerGet(repoStr, arg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filterAT := r.URL.Query().Get(referrerFilterATParam)
		cacheDig := r.URL.Query().Get("cache")
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 0 {
			page = 0
		}
		// most errors should return an empty index
		i := types.Index{
			SchemaVersion: 2,
			MediaType:     types.MediaTypeOCI1ManifestList,
		}
		repo, err := s.store.RepoGet(r.Context(), repoStr)
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
		if cacheDig != "" && page != 0 {
			dig, err := digest.Parse(cacheDig)
			if err != nil {
				s.log.Info("paged referrers request for invalid cache digest", "cache", cacheDig, "repo", repoStr, "page", page)
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoUnsupported("requested digest is not valid"))
				return
			}
			if cacheResp, err := s.referrerCache.Get(referrerKey{dig: dig, artifactType: filterAT}); err == nil && page < len(cacheResp) {
				if page+1 < len(cacheResp) {
					next := r.URL
					q := next.Query()
					q.Set("page", fmt.Sprintf("%d", page+1))
					next.RawQuery = q.Encode()
					w.Header().Add("Link", fmt.Sprintf("<%s>; rel=next", next.String()))
				}
				w.Header().Add("content-type", types.MediaTypeOCI1ManifestList)
				w.WriteHeader(http.StatusOK)
				_, err = w.Write(cacheResp[page])
				if err != nil {
					s.log.Info("failed to write referrers response", "err", err, "repo", repoStr, "arg", arg)
				}
				return
			}
			// cache search for paged data failed, regenerate from current state, only use page counter if digest matches
		}
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
		// check page cache for digest, two users requesting same referrer list
		if cacheResp, err := s.referrerCache.Get(referrerKey{dig: d.Digest, artifactType: filterAT}); err == nil {
			if page >= len(cacheResp) {
				page = 0
			}
			if page+1 < len(cacheResp) {
				next := r.URL
				q := next.Query()
				q.Set("cache", d.Digest.String())
				q.Set("page", fmt.Sprintf("%d", page+1))
				next.RawQuery = q.Encode()
				w.Header().Add("Link", fmt.Sprintf("<%s>; rel=next", next.String()))
			}
			w.WriteHeader(http.StatusOK)
			_, err = w.Write(cacheResp[page])
			if err != nil {
				s.log.Info("failed to write referrers response", "err", err, "repo", repoStr, "arg", arg)
			}
			return
		}
		rdr, err := repo.BlobGet(d.Digest)
		if err != nil {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(i)
			s.log.Info("failed to get referrers response", "err", err, "repo", repoStr, "arg", arg, "digest", d.Digest.String())
			return
		}
		out, err := io.ReadAll(rdr)
		_ = rdr.Close()
		if err != nil {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(i)
			s.log.Info("failed to read referrers response", "err", err, "repo", repoStr, "arg", arg, "digest", d.Digest.String())
			return
		}
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
		if int64(len(out)) > s.conf.API.Referrer.Limit {
			// split the response if necessary
			split, err := referrerSplit(out, s.conf.API.Referrer.Limit)
			if err != nil {
				s.log.Info("failed splitting referrer list", "err", err, "repo", repoStr, "arg", arg, "digest", d.Digest.String())
			}
			if len(split) == 0 {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(i)
				return
			}
			// cache the split
			s.referrerCache.Set(referrerKey{dig: d.Digest, artifactType: filterAT}, split)
			// set the requested page output and next link
			if page > 0 && (cacheDig != d.Digest.String() || page >= len(split)) {
				page = 0
			}
			if page+1 < len(split) {
				next := r.URL
				q := next.Query()
				q.Set("cache", d.Digest.String())
				q.Set("page", fmt.Sprintf("%d", page+1))
				next.RawQuery = q.Encode()
				w.Header().Add("Link", fmt.Sprintf("<%s>; rel=next", next.String()))
			}
			out = split[page]
		}
		w.Header().Add("content-length", fmt.Sprintf("%d", len(out)))
		w.WriteHeader(http.StatusOK)
		_, err = w.Write(out)
		if err != nil {
			s.log.Info("failed to write referrers response", "err", err, "repo", repoStr, "arg", arg)
		}
	}
}

// referrerFilter applies a filter to the returned descriptor list
func referrerFilter(inBytes []byte, filterArtifactType string) ([]byte, error) {
	in := types.Index{}
	err := json.Unmarshal(inBytes, &in)
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
	return outBytes, nil
}

// referrerSplit separates a referrer index into separate pages.
// This will fail if a single entry still exceeds the limit.
// Note this is designed for readability over efficiency.
func referrerSplit(inBytes []byte, limit int64) ([][]byte, error) {
	in := types.Index{}
	err := json.Unmarshal(inBytes, &in)
	if err != nil {
		return nil, err
	}
	result := [][]byte{}
	last := []byte{}
	cur := types.Index{
		SchemaVersion: in.SchemaVersion,
		MediaType:     in.MediaType,
		ArtifactType:  in.ArtifactType,
		Annotations:   in.Annotations,
		Manifests:     []types.Descriptor{},
		Subject:       in.Subject,
	}
	errs := []error{}
	for _, d := range in.Manifests {
		cur.Manifests = append(cur.Manifests, d)
		next, err := json.Marshal(cur)
		if err != nil {
			return nil, err
		}
		if int64(len(next)) > limit {
			result = append(result, last)
			cur.Manifests = []types.Descriptor{d}
			next, err = json.Marshal(cur)
			if err != nil {
				return nil, err
			}
			if int64(len(next)) > limit {
				errs = append(errs, fmt.Errorf("single descriptor greater than limit: %s", d.Digest))
				cur.Manifests = []types.Descriptor{}
				last = nil
			}
		}
		last = next
	}
	if last != nil {
		result = append(result, last)
	}
	if len(errs) > 0 {
		return result, errors.Join(errs...)
	}
	return result, nil
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
			err = json.NewDecoder(rdr).Decode(&refResp)
			_ = rdr.Close()
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
