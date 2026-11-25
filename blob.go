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
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	digest "github.com/sudo-bmitch/oci-digest"

	"github.com/olareg/olareg/internal/backend"
	"github.com/olareg/olareg/types"
)

func (s *Server) blobDelete(repoStr, arg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if *s.conf.Storage.ReadOnly {
			w.WriteHeader(http.StatusForbidden)
			_ = types.ErrRespJSON(w, types.ErrInfoDenied("repository is read-only"))
			return
		}
		d, err := digest.Parse(arg)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoDigestInvalid("digest cannot be parsed"))
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
		err = repo.BlobDelete(d)
		if err != nil {
			s.log.Debug("failed to delete blob", "err", err, "repo", repoStr, "digest", d.String())
			if errors.Is(err, types.ErrNotFound) {
				w.WriteHeader(http.StatusNotFound)
				_ = types.ErrRespJSON(w, types.ErrInfoBlobUnknown("blob was not found"))
			} else if errors.Is(err, types.ErrForbidden) {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoDenied(err.Error()))
			} else {
				w.WriteHeader(http.StatusInternalServerError)
				s.log.Debug("failed to delete blob", "err", err, "repo", repoStr, "arg", arg)
			}
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

func (s *Server) blobGet(repoStr, arg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d, err := digest.Parse(arg)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoDigestInvalid("digest cannot be parsed"))
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
		rdr, err := repo.BlobGet(d)
		if err != nil {
			if r.Method != http.MethodHead {
				s.log.Debug("failed to open blob", "err", err, "repo", repoStr, "digest", d.String())
			}
			if errors.Is(err, types.ErrNotFound) {
				w.WriteHeader(http.StatusNotFound)
				_ = types.ErrRespJSON(w, types.ErrInfoBlobUnknown("blob was not found"))
			} else {
				w.WriteHeader(http.StatusInternalServerError)
			}
			return
		}
		defer rdr.Close()
		w.Header().Add("Content-Type", "application/octet-stream")
		w.Header().Add(types.HeaderDockerDigest, d.String())
		// use ServeContent to handle range requests
		http.ServeContent(w, r, "", time.Time{}, rdr)
	}
}

type blobUploadState struct {
	Offset int64 `json:"offset"`
}

func (s *Server) blobUploadDelete(repoStr, sessionID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, err := s.backend.RepoGet(repoStr)
		if err != nil {
			if errors.Is(err, types.ErrRepoNotAllowed) {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoNameInvalid("repository name is not allowed"))
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Info("failed to get repo", "err", err, "repo", repoStr, "sessionID", sessionID)
			return
		}
		err = repo.UploadCancel(sessionID)
		if err != nil {
			if errors.Is(err, types.ErrNotFound) {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoBlobUploadUnknown("upload session not found"))
				s.log.Debug("upload session not found", "repo", repoStr, "sessionID", sessionID)
			} else {
				w.WriteHeader(http.StatusInternalServerError)
				s.log.Info("failed to cancel upload", "err", err, "repo", repoStr, "sessionID", sessionID)
				return
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) blobUploadGet(repoStr, sessionID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, err := s.backend.RepoGet(repoStr)
		if err != nil {
			if errors.Is(err, types.ErrRepoNotAllowed) {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoNameInvalid("repository name is not allowed"))
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Info("failed to get repo", "err", err, "repo", repoStr, "sessionID", sessionID)
			return
		}
		status, err := repo.UploadStatus(sessionID)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoBlobUploadUnknown("failed to get upload session"))
			s.log.Info("upload status failed", "repo", repoStr, "sessionID", sessionID, "error", err)
			return
		}
		stateJSON, err := json.Marshal(blobUploadState{Offset: status.Size})
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Error("failed to marshal new state", "err", err)
			return
		}
		state := base64.RawURLEncoding.EncodeToString(stateJSON)
		loc := url.URL{
			Path: r.URL.Path,
		}
		locQ := url.Values{}
		locQ.Set("state", state)
		loc.RawQuery = locQ.Encode()
		w.Header().Add("Location", loc.String())
		w.Header().Add("Range", fmt.Sprintf("0-%d", status.Size-1))
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) blobUploadPost(repoStr string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if *s.conf.Storage.ReadOnly {
			w.WriteHeader(http.StatusForbidden)
			_ = types.ErrRespJSON(w, types.ErrInfoDenied("repository is read-only"))
			return
		}
		bOpts := []backend.BlobOpt{}
		// check for mount=digest&from=repo, consider allowing anonymous blob mounts
		mountStr := r.URL.Query().Get("mount")
		fromStr := r.URL.Query().Get("from")
		if mountStr != "" && fromStr != "" {
			if err := s.blobUploadMount(fromStr, repoStr, mountStr, w, r); err == nil {
				return
			}
		}
		// check for digest and algorithm parameters
		algoStr := r.URL.Query().Get("digest-algorithm")
		if algoStr != "" {
			algo, err := digest.AlgorithmLookup(algoStr)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoDigestInvalid("unsupported digest algorithm"))
				s.log.Error("invalid digest algorithm", "algo", algoStr, "repo", repoStr)
				return
			}
			bOpts = append(bOpts, backend.BlobWithAlgorithm(algo))
		}
		dStr := r.URL.Query().Get("digest")
		var d digest.Digest
		var err error
		if dStr != "" || mountStr != "" {
			if dStr != "" {
				d, err = digest.Parse(dStr)
			} else {
				d, err = digest.Parse(mountStr)
			}
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoDigestInvalid("invalid digest format"))
				s.log.Error("invalid digest", "err", err, "repo", repoStr)
				return
			}
			bOpts = append(bOpts, backend.BlobWithDigest(d))
		}
		// start a new upload session with the backend storage and track as current upload
		repo, err := s.backend.RepoGet(repoStr)
		if err != nil {
			if errors.Is(err, types.ErrRepoNotAllowed) {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoNameInvalid("repository name is not allowed"))
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Info("failed to get repo", "err", err, "repo", repoStr)
			return
		}
		// create a new blob in the store
		sessionID, err := repo.UploadCreate(bOpts...)
		if err != nil {
			if errors.Is(err, types.ErrBlobExists) {
				// blob exists, indicate it was created and return the location to get
				//#nosec G125 all variables in the path have been sanitized
				loc, err := url.JoinPath("/v2", repoStr, "blobs", d.String())
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					s.log.Error("failed to build location header", "repo", repoStr, "err", err)
					return
				}
				w.Header().Set("location", loc)
				w.Header().Add(types.HeaderDockerDigest, d.String())
				w.WriteHeader(http.StatusCreated)
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Error("create blob", "err", err)
			return
		}
		// handle monolithic upload in the POST
		if dStr != "" {
			err = repo.UploadReadFrom(sessionID, r.Body, 0)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				s.log.Info("failed to copy blob content", "repo", repoStr, "digest", dStr, "err", err)
				return
			}
			err = repo.UploadSave(sessionID, d)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoBlobUploadInvalid(err.Error()))
				s.log.Debug("failed to save blob", "repo", repoStr, "err", err)
				return
			}
			//#nosec G125 all variables in the path have been sanitized
			loc, err := url.JoinPath("/v2", repoStr, "blobs", d.String())
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				s.log.Error("failed to build location header", "repo", repoStr, "err", err)
				return
			}
			w.Header().Set("location", loc)
			w.Header().Add(types.HeaderDockerDigest, d.String())
			w.WriteHeader(http.StatusCreated)
			return
		}
		// generate the response
		stateJSON, err := json.Marshal(blobUploadState{Offset: 0})
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Error("failed to marshal new state", "err", err)
			_ = repo.UploadCancel(sessionID)
			return
		}
		state := base64.RawURLEncoding.EncodeToString(stateJSON)
		// write the location (session id and state) and accepted status
		loc := url.URL{
			Path: r.URL.Path,
		}
		//#nosec G125 all variables in the path have been sanitized
		loc = *loc.JoinPath(sessionID)
		locQ := url.Values{}
		locQ.Set("state", state)
		loc.RawQuery = locQ.Encode()
		w.Header().Add("Location", loc.String())
		w.WriteHeader(http.StatusAccepted)
	}
}

// blobUploadMount is a handler for blob mount attempts.
// Any errors return the error without writing to the response.
// This allows the registry to fall back to a standard blob push.
// If the return is nil, the location header and created status are first be written to the response.
func (s *Server) blobUploadMount(repoSrcStr, repoTgtStr, digStr string, w http.ResponseWriter, r *http.Request) error {
	dig, err := digest.Parse(digStr)
	if err != nil {
		return err
	}
	repoTgt, err := s.backend.RepoGet(repoTgtStr)
	if err != nil {
		return err
	}
	repoSrc, err := s.backend.RepoGet(repoSrcStr)
	if err != nil {
		return err
	}
	rdr, err := repoSrc.BlobGet(dig)
	if err != nil {
		return err
	}
	defer rdr.Close()
	_, err = repoTgt.UploadCreate(backend.BlobWithDigest(dig), backend.BlobWithReader(rdr))
	if err != nil {
		if errors.Is(err, types.ErrBlobExists) {
			// blob exists, indicate it was created and return the location to get
			//#nosec G125 all variables in the path have been sanitized
			loc, err := url.JoinPath("/v2", repoTgtStr, "blobs", dig.String())
			if err != nil {
				s.log.Error("failed to build location header", "repo", repoTgtStr, "err", err)
				return err
			}
			w.Header().Set("location", loc)
			w.Header().Add(types.HeaderDockerDigest, dig.String())
			w.WriteHeader(http.StatusCreated)
			return nil
		}
		return err
	}
	// write the success status and return nil
	//#nosec G125 all variables in the path have been sanitized
	loc, err := url.JoinPath("/v2", repoTgtStr, "blobs", dig.String())
	if err != nil {
		return err
	}
	w.Header().Set("location", loc)
	w.Header().Add(types.HeaderDockerDigest, dig.String())
	w.WriteHeader(http.StatusCreated)
	return nil
}

func (s *Server) blobUploadPatch(repoStr, sessionID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, err := s.backend.RepoGet(repoStr)
		if err != nil {
			if errors.Is(err, types.ErrRepoNotAllowed) {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoNameInvalid("repository name is not allowed"))
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Info("failed to get repo", "err", err, "repo", repoStr, "sessionID", sessionID)
			return
		}
		us, err := repo.UploadStatus(sessionID)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoBlobUploadUnknown("upload session not found"))
			s.log.Error("upload session not found", "repo", repoStr, "sessionID", sessionID)
			return
		}
		// check range if provided
		cr := r.Header.Get("content-range")
		if !blobValidRange(cr, us.Size) {
			w.Header().Set("range", fmt.Sprintf("0-%d", us.Size-1))
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			_ = types.ErrRespJSON(w, types.ErrInfoSizeInvalid(fmt.Sprintf("range is not valid, current range is 0-%d", us.Size-1)))
			s.log.Debug("blob patch content range invalid", "repo", repoStr, "sessionID", sessionID, "range", cr, "curEnd", us.Size)
			return
		}
		// check state variable
		stateStr := r.URL.Query().Get("state")
		stateIn := blobUploadState{}
		stateB, err := base64.RawURLEncoding.DecodeString(stateStr)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoBlobUploadInvalid("invalid state"))
			s.log.Error("invalid state", "err", err, "repo", repoStr, "sessionID", sessionID, "state", r.URL.Query().Get("state"))
			return
		}
		err = json.Unmarshal(stateB, &stateIn)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoBlobUploadInvalid("invalid state"))
			s.log.Error("invalid state", "err", err, "repo", repoStr, "sessionID", sessionID, "state", r.URL.Query().Get("state"))
			return
		}
		if stateIn.Offset != us.Size {
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoBlobUploadInvalid("invalid state"))
			s.log.Error("invalid state size", "repo", repoStr, "sessionID", sessionID, "state", r.URL.Query().Get("state"), "sizeState", stateIn.Offset, "sizeCur", us.Size)
			return
		}
		// write bytes to blob
		err = repo.UploadReadFrom(sessionID, r.Body, us.Size)
		if err != nil {
			if errors.Is(err, types.ErrBlobUploadInvalid) {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoBlobUploadInvalid(fmt.Sprintf("invalid upload: %v", err)))
			} else {
				w.WriteHeader(http.StatusInternalServerError)
			}
			s.log.Error("failed to write blob", "err", err, "repo", repoStr, "sessionID", sessionID)
			return
		}
		usEnd, err := repo.UploadStatus(sessionID)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoBlobUploadUnknown("upload session not found"))
			s.log.Error("upload session not found", "repo", repoStr, "sessionID", sessionID)
			return
		}
		curEnd := usEnd.Size
		stateJSON, err := json.Marshal(blobUploadState{Offset: curEnd})
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Error("failed to marshal new state", "err", err)
			return
		}
		state := base64.RawURLEncoding.EncodeToString(stateJSON)
		loc := url.URL{
			Path: r.URL.Path,
		}
		locQ := url.Values{}
		locQ.Set("state", state)
		loc.RawQuery = locQ.Encode()
		w.Header().Add("Location", loc.String())
		w.Header().Add("Range", fmt.Sprintf("0-%d", curEnd-1))
		w.WriteHeader(http.StatusAccepted)
	}
}

func (s *Server) blobUploadPut(repoStr, sessionID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, err := s.backend.RepoGet(repoStr)
		if err != nil {
			if errors.Is(err, types.ErrRepoNotAllowed) {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoNameInvalid("repository name is not allowed"))
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Info("failed to get repo", "err", err, "repo", repoStr, "sessionID", sessionID)
			return
		}
		us, err := repo.UploadStatus(sessionID)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoBlobUploadUnknown("upload session not found"))
			s.log.Error("upload session not found", "repo", repoStr, "sessionID", sessionID)
			return
		}
		// check range if provided
		cr := r.Header.Get("content-range")
		if !blobValidRange(cr, us.Size) {
			w.Header().Set("range", fmt.Sprintf("0-%d", us.Size-1))
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			_ = types.ErrRespJSON(w, types.ErrInfoSizeInvalid(fmt.Sprintf("range is not valid, current range is 0-%d", us.Size-1)))
			s.log.Debug("blob put content range invalid", "repo", repoStr, "sessionID", sessionID, "range", cr, "curEnd", us.Size)
			return
		}
		// parse digest
		d, err := digest.Parse(r.URL.Query().Get("digest"))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoDigestInvalid("invalid or missing digest"))
			s.log.Error("invalid or missing digest", "err", err, "repo", repoStr, "sessionID", sessionID, "digest", r.URL.Query().Get("digest"))
			return
		}
		// check state
		stateStr := r.URL.Query().Get("state")
		stateIn := blobUploadState{}
		stateB, err := base64.RawURLEncoding.DecodeString(stateStr)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoBlobUploadInvalid("invalid state"))
			s.log.Error("invalid state", "err", err, "repo", repoStr, "sessionID", sessionID, "state", r.URL.Query().Get("state"))
			return
		}
		err = json.Unmarshal(stateB, &stateIn)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoBlobUploadInvalid("invalid state"))
			s.log.Error("invalid state", "err", err, "repo", repoStr, "sessionID", sessionID, "state", r.URL.Query().Get("state"))
			return
		}
		if stateIn.Offset != us.Size {
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoBlobUploadInvalid("invalid state"))
			s.log.Error("invalid state size", "repo", repoStr, "sessionID", sessionID, "state", r.URL.Query().Get("state"), "sizeState", stateIn.Offset, "sizeCur", us.Size)
			return
		}
		// copy blob content
		err = repo.UploadReadFrom(sessionID, r.Body, us.Size)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Error("failed to write blob", "err", err, "repo", repoStr, "sessionID", sessionID)
			return
		}
		// save the result
		err = repo.UploadSave(sessionID, d)
		if err != nil {
			if errors.Is(err, types.ErrDigestInvalid) {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoDigestInvalid("invalid digest: "+err.Error()))
				s.log.Debug("failed to save blob (invalid digest)", "err", err, "repo", repoStr, "sessionID", sessionID, "digest", d.String(), "size", us.Size)
			} else {
				s.log.Error("failed to save blob", "err", err, "repo", repoStr, "sessionID", sessionID, "digest", d.String(), "size", us.Size)
				w.WriteHeader(http.StatusInternalServerError)
			}
			return
		}
		//#nosec G125 all variables in the path have been sanitized
		loc, err := url.JoinPath("/v2", repoStr, "blobs", d.String())
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Error("failed to build location header", "err", err, "repo", repoStr, "sessionID", sessionID)
			return
		}
		w.Header().Set("location", loc)
		w.Header().Add(types.HeaderDockerDigest, d.String())
		w.WriteHeader(http.StatusCreated)
	}
}

func blobValidRange(cr string, curSize int64) bool {
	if cr == "" {
		return true // no range, streaming patch allowed
	}
	i := strings.Index(cr, "-")
	if i < 1 {
		return false // invalid range header
	}
	crStart, err := strconv.ParseInt(cr[:i], 10, 64)
	if err != nil {
		return false
	}
	if crStart != curSize {
		return false
	}
	return true
}
