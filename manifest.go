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
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	digest "github.com/sudo-bmitch/oci-digest"

	"github.com/olareg/olareg/types"
)

func (s *Server) manifestDelete(repoStr, arg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if *s.conf.Storage.ReadOnly {
			w.WriteHeader(http.StatusForbidden)
			_ = types.ErrRespJSON(w, types.ErrInfoDenied("repository is read-only"))
			return
		}
		repo, err := s.backend.RepoGet(repoStr)
		if err != nil {
			if errors.Is(err, types.ErrRepoNotAllowed) {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoNameInvalid("repository name is not allowed"))
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Info("failed to get repo", "err", err, "repo", repoStr, "arg", arg)
			return
		}
		if types.RefTagRE.MatchString(arg) {
			err = repo.TagDelete(arg)
			if err != nil && !errors.Is(err, types.ErrNotFound) {
				w.WriteHeader(http.StatusInternalServerError)
				s.log.Debug("failed to delete tag", "err", err, "repo", repoStr, "arg", arg)
				return
			}
		} else {
			dig, err := digest.Parse(arg)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoDigestInvalid("tag or digest invalid"))
				s.log.Debug("failed to parse tag or digest", "repo", repoStr, "arg", arg, "err", err)
				return
			}
			err = repo.ManifestDelete(dig)
			if err != nil && !errors.Is(err, types.ErrNotFound) {
				w.WriteHeader(http.StatusInternalServerError)
				s.log.Debug("failed to delete manifest", "err", err, "repo", repoStr, "arg", arg)
				return
			}
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

func (s *Server) manifestGet(repoStr, arg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, err := s.backend.RepoGet(repoStr)
		if err != nil {
			if errors.Is(err, types.ErrRepoNotAllowed) {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoNameInvalid("repository name is not allowed"))
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Info("failed to get repo", "err", err, "repo", repoStr, "arg", arg)
			return
		}
		var dig digest.Digest
		if types.RefTagRE.MatchString(arg) {
			dig, err = repo.TagGet(arg)
			if errors.Is(err, types.ErrNotFound) {
				w.WriteHeader(http.StatusNotFound)
				_ = types.ErrRespJSON(w, types.ErrInfoManifestUnknown("tag was not found in repository"))
				return
			} else if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				s.log.Debug("failed to get tag", "err", err, "repo", repoStr, "arg", arg)
				return
			}
		} else {
			dig, err = digest.Parse(arg)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoDigestInvalid("tag or digest invalid"))
				s.log.Debug("failed to parse tag or digest", "repo", repoStr, "arg", arg, "err", err)
				return
			}
		}
		desc, rdr, err := repo.ManifestGet(dig)
		acceptList := r.Header.Values("Accept")
		// if desc does not match requested accept header, but we are pulling an index by tag, try to find a matching descriptor
		if err == nil && len(acceptList) > 0 && !types.MediaTypeAccepts(desc.MediaType, acceptList) &&
			types.MediaTypeIndex(desc.MediaType) && types.RefTagRE.MatchString(arg) {
			i := types.Index{}
			err = json.NewDecoder(rdr).Decode(&i)
			_ = rdr.Close()
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				s.log.Info("failed to parse index searching for a media type match", "err", err, "repo", repoStr, "arg", arg)
				return
			}
			for _, d := range i.Manifests {
				if types.MediaTypeAccepts(d.MediaType, acceptList) {
					// use first match if found
					desc, rdr, err = repo.ManifestGet(d.Digest)
					break
				}
			}
		}
		if errors.Is(err, types.ErrNotFound) {
			w.WriteHeader(http.StatusNotFound)
			_ = types.ErrRespJSON(w, types.ErrInfoManifestUnknown("tag or digest was not found in repository"))
			return
		} else if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Info("failed to retrieve manifest", "err", err, "repo", repoStr, "arg", arg)
			return
		}
		defer rdr.Close()
		if !types.MediaTypeAccepts(desc.MediaType, acceptList) {
			w.WriteHeader(http.StatusNotFound)
			_ = types.ErrRespJSON(w, types.ErrInfoManifestUnknown("requested media type not found, available media type is "+desc.MediaType))
			return
		}
		w.Header().Add("Content-Type", desc.MediaType)
		w.Header().Add(types.HeaderDockerDigest, desc.Digest.String())
		// use ServeContent to handle range requests
		http.ServeContent(w, r, "", time.Time{}, rdr)
	}
}

func (s *Server) manifestPut(repoStr, arg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if *s.conf.Storage.ReadOnly {
			w.WriteHeader(http.StatusForbidden)
			_ = types.ErrRespJSON(w, types.ErrInfoDenied("repository is read-only"))
			return
		}
		var dig digest.Digest
		repo, err := s.backend.RepoGet(repoStr)
		if err != nil {
			if errors.Is(err, types.ErrRepoNotAllowed) {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoNameInvalid("repository name is not allowed"))
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Info("failed to get repo", "err", err, "repo", repoStr, "arg", arg)
			return
		}
		// parse/validate headers
		mt := r.Header.Get("content-type")
		mt, _, _ = strings.Cut(mt, ";")
		mt = strings.TrimSpace(strings.ToLower(mt))
		if r.ContentLength > s.conf.API.Manifest.Limit {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			_ = types.ErrRespJSON(w, types.ErrInfoManifestInvalid(fmt.Sprintf("manifest too large, limited to %d bytes", s.conf.API.Manifest.Limit)))
			return
		}
		// parse params
		// read and validate the tags
		tags := r.URL.Query()["tag"]
		for _, tag := range tags {
			if !types.RefTagRE.MatchString(tag) {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoUnsupported("invalid tag"))
				s.log.Debug("invalid tag argument", "repo", repoStr, "tag", tag, "tags", tags)
				return
			}
		}
		// parse arg
		pushByDig := false
		if types.RefTagRE.MatchString(arg) {
			if len(tags) > 0 {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoUnsupported("tag query parameter requires a digest"))
				s.log.Debug("attempted to push with a tag query parameter without a digest", "repo", repoStr, "arg", arg, "tags", tags, "err", err)
				return
			}
			tags = []string{arg}
		} else {
			dig, err = digest.Parse(arg)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoDigestInvalid("tag or digest invalid"))
				s.log.Debug("failed to parse tag or digest", "repo", repoStr, "arg", arg, "err", err)
				return
			}
			pushByDig = true
		}
		// read manifest
		rLimit := io.LimitReader(r.Body, s.conf.API.Manifest.Limit)
		mRaw, err := io.ReadAll(rLimit)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Info("failed to read manifest", "repo", repoStr, "arg", arg, "err", err)
			return
		}
		// push manifest to the backend
		desc, procTags, subjDig, err := repo.ManifestPut(mRaw, mt, dig, tags)
		if err != nil {
			if errors.Is(err, types.ErrManifestInvalid) {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoManifestInvalid(err.Error()))
				s.log.Debug("failed to push manifest (invalid)", "repo", repoStr, "arg", arg, "err", err)
				return
			} else if errors.Is(err, types.ErrDigestInvalid) {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoDigestInvalid(err.Error()))
				s.log.Debug("failed to push manifest (bad digest)", "repo", repoStr, "arg", arg, "err", err)
				return
			} else if errors.Is(err, types.ErrManifestBlobUnknown) {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoManifestBlobUnknown(err.Error()))
				s.log.Debug("failed to push manifest (blob unknown)", "repo", repoStr, "arg", arg, "err", err)
				return
			} else {
				w.WriteHeader(http.StatusInternalServerError)
				s.log.Info("failed to push manifest", "repo", repoStr, "arg", arg, "err", err)
				return
			}
		}
		// report processed tags when pushing by digest
		if pushByDig {
			for _, tag := range procTags {
				w.Header().Add("OCI-Tag", tag)
			}
		}
		// set subject header if referrers were updated
		if !subjDig.IsZero() {
			w.Header().Set("OCI-Subject", subjDig.String())
		}
		// set the location header
		//#nosec G125 all variables in the path have been sanitized
		loc, err := url.JoinPath("/v2", repoStr, "manifests", desc.Digest.String())
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Error("failed to build location header for manifest put", "err", err, "repo", repoStr, "arg", arg)
			return
		}
		w.Header().Set("location", loc)
		w.Header().Add(types.HeaderDockerDigest, desc.Digest.String())
		w.WriteHeader(http.StatusCreated)
	}
}
