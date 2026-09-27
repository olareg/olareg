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
	"sort"
	"strconv"
	"strings"

	digest "github.com/sudo-bmitch/oci-digest"

	"github.com/olareg/olareg/types"
)

type tagHistoryKey struct {
	dig           digest.Digest
	before, since string
}

// tagHistory is EXPERIMENTAL, pending the merge of https://github.com/opencontainers/distribution-spec/pull/606
func (s *Server) tagHistory(repoStr, arg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limitStr := r.URL.Query().Get("n")
		limit, err := strconv.Atoi(limitStr)
		if limitStr == "" || err != nil {
			limit = -1
		}
		filterBefore := r.URL.Query().Get("before")
		filterSince := r.URL.Query().Get("since")
		cacheDig := r.URL.Query().Get("cache")
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		if offset < 0 {
			offset = 0
		}
		if !types.RefTagRE.MatchString(arg) {
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoUnsupported("requested tag is not valid"))
			return
		}
		// most errors should return an empty index
		i := types.Index{
			SchemaVersion: 2,
			MediaType:     types.MediaTypeOCI1ManifestList,
			Manifests:     []types.Descriptor{},
		}
		if limit == 0 {
			w.Header().Add("content-type", types.MediaTypeOCI1ManifestList)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(i)
			return
		}
		repo, err := s.backend.RepoGet(repoStr)
		if err != nil {
			w.Header().Add("content-type", types.MediaTypeOCI1ManifestList)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(i)
			if !errors.Is(err, types.ErrRepoNotAllowed) {
				s.log.Info("failed to get repo", "repo", repoStr, "arg", arg, "err", err)
			}
			return
		}
		if cacheDig != "" && offset != 0 {
			dig, err := digest.Parse(cacheDig)
			if err != nil {
				s.log.Info("paged tag history request for invalid cache digest", "cache", cacheDig, "repo", repoStr, "offset", offset)
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoUnsupported("requested digest is not valid"))
				return
			}
			if cacheResp, err := s.tagHistoryCache.Get(tagHistoryKey{dig: dig, before: filterBefore, since: filterSince}); err == nil && offset < len(cacheResp.Manifests) {
				cacheResp.Manifests = cacheResp.Manifests[offset:]
				if limit > 0 && limit < len(cacheResp.Manifests) {
					cacheResp.Manifests = cacheResp.Manifests[:limit]
				}
				nextPage, addOffset, err := cacheResp.Paginate(s.conf.API.TagHistory.Limit)
				if err != nil {
					s.log.Info("failed splitting tag history", "err", err, "repo", repoStr, "arg", arg, "digest", dig.String())
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
				w.Header().Add("content-type", types.MediaTypeOCI1ManifestList)
				w.Header().Add("content-length", fmt.Sprintf("%d", len(nextPage)))
				w.WriteHeader(http.StatusOK)
				_, err = w.Write(nextPage) //#nosec G705
				if err != nil {
					s.log.Info("failed to write tag history response", "err", err, "repo", repoStr, "arg", arg)
				}
				return
			}
			// cache search for paged data failed, regenerate from current state, only use page counter if digest matches
		}
		d, i, err := repo.TagHistory(arg)
		if err != nil {
			w.Header().Add("content-type", types.MediaTypeOCI1ManifestList)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(i)
			if !errors.Is(err, types.ErrNotFound) {
				s.log.Info("failed to get the tag history", "repo", repoStr, "arg", arg, "err", err)
			}
			return
		}
		// apply filters and cache the result
		if filterBefore != "" || filterSince != "" {
			i.Manifests = slices.DeleteFunc(i.Manifests, func(cur types.Descriptor) bool {
				val, ok := cur.AnnotationVal(types.AnnotTagTimestamp)
				return ok && ((filterBefore != "" && val >= filterBefore) || (filterSince != "" && val <= filterSince))
			})
		}
		s.tagHistoryCache.Set(tagHistoryKey{dig: d.Digest, before: filterBefore, since: filterSince}, i)
		// apply pagination
		if limit > 0 && limit < len(i.Manifests) {
			i.Manifests = i.Manifests[:limit]
		}
		nextPage, offset, err := i.Paginate(s.conf.API.TagHistory.Limit)
		if err != nil {
			s.log.Info("failed splitting tag history", "err", err, "repo", repoStr, "arg", arg, "digest", d.Digest.String())
		}
		if len(nextPage) == 0 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
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
			s.log.Info("failed to write tag history", "err", err, "repo", repoStr, "arg", arg)
		}
	}
}

func (s *Server) tagList(repoStr string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, err := s.backend.RepoGet(repoStr)
		if err != nil {
			if errors.Is(err, types.ErrRepoNotAllowed) {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoNameInvalid("repository name is not allowed"))
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Info("failed to get repo", "repo", repoStr, "err", err)
			return
		}
		tags, err := repo.TagList()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Info("failed to list tags", "repo", repoStr, "err", err)
			return
		}
		last := r.URL.Query().Get("last")
		w.Header().Add("Content-Type", "application/json")
		tl := types.TagList{
			Name: repoStr,
			Tags: []string{},
		}
		for t := range tags {
			if strings.Compare(last, t) < 0 {
				tl.Tags = append(tl.Tags, t)
			}
		}
		sort.Strings(tl.Tags)
		n := r.URL.Query().Get("n")
		if n != "" {
			if nInt, err := strconv.Atoi(n); err == nil && len(tl.Tags) > nInt {
				tl.Tags = tl.Tags[:nInt]
				// add next header for pagination
				next := r.URL
				q := next.Query()
				q.Set("last", tl.Tags[len(tl.Tags)-1])
				next.RawQuery = q.Encode()
				w.Header().Add("Link", fmt.Sprintf("<%s>; rel=next", next.String()))
			}
		}
		tlJSON, err := json.Marshal(tl)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Warn("failed to marshal tag list", "err", err)
			return
		}
		w.Header().Add("Content-Length", fmt.Sprintf("%d", len(tlJSON)))
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, err = w.Write(tlJSON)
			if err != nil {
				s.log.Warn("failed to marshal tag list", "err", err)
			}
		}
	}
}
