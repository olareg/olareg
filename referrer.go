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

package olareg

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"

	digest "github.com/sudo-bmitch/oci-digest"

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

// referrerGet searches for the referrers response in the index.
// All errors should return an empty response, no 404's should be generated.
func (s *Server) referrerGet(repoStr, arg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filterAT := r.URL.Query().Get(referrerFilterATParam)
		cacheDig := r.URL.Query().Get("cache")
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		if offset < 0 {
			offset = 0
		}
		dig, err := digest.Parse(arg)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoUnsupported("requested digest is not valid"))
			return
		}
		// most errors should return an empty index
		i := types.Index{
			SchemaVersion: 2,
			MediaType:     types.MediaTypeOCI1ManifestList,
			Manifests:     []types.Descriptor{},
		}
		repo, err := s.backend.RepoGet(repoStr)
		if err != nil {
			w.Header().Add("content-type", types.MediaTypeOCI1ManifestList)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(i)
			if !errors.Is(err, types.ErrRepoNotAllowed) {
				s.log.Info("failed to get repo", "err", err, "repo", repoStr, "arg", arg)
			}
			return
		}
		if cacheDig != "" && offset != 0 {
			dig, err := digest.Parse(cacheDig)
			if err != nil {
				s.log.Info("paginated referrers request for invalid cache digest", "cache", cacheDig, "repo", repoStr, "offset", offset)
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoUnsupported("requested digest is not valid"))
				return
			}
			if cacheResp, err := s.referrerCache.Get(referrerKey{dig: dig, artifactType: filterAT}); err == nil && offset < len(cacheResp.Manifests) {
				cacheResp.Manifests = cacheResp.Manifests[offset:]
				nextPage, addOffset, err := cacheResp.Paginate(s.conf.API.Referrer.Limit)
				if err != nil {
					s.log.Info("failed splitting referrer list", "err", err, "repo", repoStr, "arg", arg, "digest", dig.String())
				}
				if len(nextPage) == 0 {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				if addOffset > 0 {
					next := r.URL
					q := next.Query()
					q.Set("offset", fmt.Sprintf("%d", offset+addOffset))
					next.RawQuery = q.Encode()
					w.Header().Add("Link", fmt.Sprintf("<%s>; rel=next", next.String()))
				}
				if filterAT != "" {
					w.Header().Add(referrerFilterATHeaderKey, referrerFilterATHeaderValue)
				}
				w.Header().Add("content-type", types.MediaTypeOCI1ManifestList)
				w.Header().Add("content-length", fmt.Sprintf("%d", len(nextPage)))
				w.WriteHeader(http.StatusOK)
				_, err = w.Write(nextPage) //#nosec G705
				if err != nil {
					s.log.Info("failed to write referrers response", "err", err, "repo", repoStr, "arg", arg)
				}
				return
			}
			// cache search for paged data failed, regenerate from current state, only use page counter if digest matches
		}
		d, rl, err := repo.ReferrerList(dig)
		if err != nil {
			w.Header().Add("content-type", types.MediaTypeOCI1ManifestList)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(i)
			if !errors.Is(err, types.ErrNotFound) {
				s.log.Info("failed to list referrers", "err", err, "repo", repoStr, "arg", arg)
			}
			return
		}
		// filter the result, cache it
		if filterAT != "" {
			rl.Manifests = slices.DeleteFunc(rl.Manifests, func(cur types.Descriptor) bool { return cur.ArtifactType != filterAT })
			w.Header().Add(referrerFilterATHeaderKey, referrerFilterATHeaderValue)
		}
		s.referrerCache.Set(referrerKey{dig: d.Digest, artifactType: filterAT}, rl)
		// paginate the output
		nextPage, offset, err := rl.Paginate(s.conf.API.Referrer.Limit)
		if err != nil {
			s.log.Info("failed splitting referrer list", "err", err, "repo", repoStr, "arg", arg, "digest", d.Digest.String())
		}
		if len(nextPage) == 0 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// set the requested page output and next link
		if offset > 0 {
			next := r.URL
			q := next.Query()
			q.Set("cache", d.Digest.String())
			q.Set("offset", fmt.Sprintf("%d", offset))
			next.RawQuery = q.Encode()
			w.Header().Add("Link", fmt.Sprintf("<%s>; rel=next", next.String()))
		}
		// write the requested page
		w.Header().Add("content-type", types.MediaTypeOCI1ManifestList)
		w.Header().Add("content-length", fmt.Sprintf("%d", len(nextPage)))
		w.WriteHeader(http.StatusOK)
		//#nosec G705 this looks like a false positive
		_, err = w.Write(nextPage)
		if err != nil {
			s.log.Info("failed to write referrers response", "err", err, "repo", repoStr, "arg", arg)
		}
	}
}
