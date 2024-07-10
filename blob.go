package olareg

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	// imports required for go-digest
	_ "crypto/sha256"
	_ "crypto/sha512"

	"github.com/opencontainers/go-digest"

	"github.com/olareg/olareg/internal/store"
	"github.com/olareg/olareg/types"
)

func (s *Server) blobGet(repoStr, arg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d, err := digest.Parse(arg)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoDigestInvalid("digest cannot be parsed"))
			return
		}
		repo, err := s.store.RepoGet(r.Context(), repoStr)
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
		repo.Done()
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
		repo, err := s.store.RepoGet(r.Context(), repoStr)
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
		repo.Done()
		if err != nil {
			s.log.Debug("failed to delete blob", "err", err, "repo", repoStr, "digest", d.String())
			if errors.Is(err, types.ErrNotFound) {
				w.WriteHeader(http.StatusNotFound)
				_ = types.ErrRespJSON(w, types.ErrInfoBlobUnknown("blob was not found"))
			} else {
				w.WriteHeader(http.StatusInternalServerError)
			}
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

type blobUploadState struct {
	Offset int64 `json:"offset"`
}

func (s *Server) blobUploadDelete(repoStr, sessionID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, err := s.store.RepoGet(r.Context(), repoStr)
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
		bc, err := repo.BlobSession(sessionID)
		repo.Done()
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoBlobUploadUnknown("upload session not found"))
			s.log.Error("upload session not found", "repo", repoStr, "sessionID", sessionID)
			return
		}
		err = bc.Cancel()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Info("failed to cancel upload", "err", err, "repo", repoStr, "sessionID", sessionID)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

func (s *Server) blobUploadGet(repoStr, sessionID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, err := s.store.RepoGet(r.Context(), repoStr)
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
		bc, err := repo.BlobSession(sessionID)
		repo.Done()
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoBlobUploadUnknown("upload session not found"))
			s.log.Error("upload session not found", "repo", repoStr, "sessionID", sessionID)
			return
		}
		curEnd := bc.Size()
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
		bOpts := []store.BlobOpt{}
		// check for mount=digest&from=repo, consider allowing anonymous blob mounts
		mountStr := r.URL.Query().Get("mount")
		fromStr := r.URL.Query().Get("from")
		if mountStr != "" && fromStr != "" {
			if err := s.blobUploadMount(fromStr, repoStr, mountStr, w, r); err == nil {
				return
			}
		}
		// check for digest and algorithm parameters
		// TODO(bmitch): the digest-algorithm field is EXPERIMENTAL and needs to be adopted by OCI
		algoStr := r.URL.Query().Get("digest-algorithm")
		if algoStr != "" {
			algo := digest.Algorithm(algoStr)
			if !algo.Available() {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoDigestInvalid("unsupported digest algorithm"))
				s.log.Error("invalid digest algorithm", "algo", algoStr, "repo", repoStr)
				return
			}
			bOpts = append(bOpts, store.BlobWithAlgorithm(algo))
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
			bOpts = append(bOpts, store.BlobWithDigest(d))
		}
		// start a new upload session with the backend storage and track as current upload
		repo, err := s.store.RepoGet(r.Context(), repoStr)
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
		bc, sessionID, err := repo.BlobCreate(bOpts...)
		repo.Done()
		if err != nil {
			if errors.Is(err, types.ErrBlobExists) {
				// blob exists, indicate it was created and return the location to get
				loc, err := url.JoinPath("/v2", repoStr, "blobs", d.String())
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					s.log.Error("failed to build location header", "repo", repoStr, "err", err)
					return
				}
				w.Header().Set("location", loc)
				w.WriteHeader(http.StatusCreated)
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Error("create blob", "err", err)
			return
		}
		// handle monolithic upload in the POST
		if dStr != "" {
			_, err = io.Copy(bc, r.Body)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				s.log.Info("failed to copy blob content", "repo", repoStr, "digest", dStr, "err", err)
				return
			}
			err = bc.Verify(d)
			if err != nil {
				_ = bc.Cancel()
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoBlobUploadInvalid("digest mismatch"))
				s.log.Debug("failed to verify blob digest", "repo", repoStr, "digest", d.String(), "err", err)
				return
			}
			err = bc.Close()
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				s.log.Info("failed to close blob", "repo", repoStr, "err", err)
				return
			}
			loc, err := url.JoinPath("/v2", repoStr, "blobs", d.String())
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				s.log.Error("failed to build location header", "repo", repoStr, "err", err)
				return
			}
			w.Header().Set("location", loc)
			w.WriteHeader(http.StatusCreated)
		}
		// generate the response
		stateJSON, err := json.Marshal(blobUploadState{Offset: 0})
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Error("failed to marshal new state", "err", err)
			_ = bc.Cancel()
			return
		}
		state := base64.RawURLEncoding.EncodeToString(stateJSON)
		// write the location (session id and state) and accepted status
		loc := url.URL{
			Path: r.URL.Path,
		}
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
	repoTgt, err := s.store.RepoGet(r.Context(), repoTgtStr)
	if err != nil {
		return err
	}
	bc, _, err := repoTgt.BlobCreate(store.BlobWithDigest(dig))
	repoTgt.Done()
	if err != nil {
		if errors.Is(err, types.ErrBlobExists) {
			// blob exists, indicate it was created and return the location to get
			loc, err := url.JoinPath("/v2", repoTgtStr, "blobs", dig.String())
			if err != nil {
				s.log.Error("failed to build location header", "repo", repoTgtStr, "err", err)
				return err
			}
			w.Header().Set("location", loc)
			w.WriteHeader(http.StatusCreated)
			return nil
		}
		return err
	}
	repoSrc, err := s.store.RepoGet(r.Context(), repoSrcStr)
	if err != nil {
		return errors.Join(err, bc.Cancel())
	}
	rdr, err := repoSrc.BlobGet(dig)
	repoSrc.Done()
	if err != nil {
		return errors.Join(err, bc.Cancel())
	}
	// copy content from source repo
	_, err = io.Copy(bc, rdr)
	if err != nil {
		return errors.Join(err, bc.Cancel(), rdr.Close())
	}
	err = errors.Join(rdr.Close(), bc.Close())
	if err != nil {
		return err
	}
	// write the success status and return nil
	loc, err := url.JoinPath("/v2", repoTgtStr, "blobs", dig.String())
	if err != nil {
		return err
	}
	w.Header().Set("location", loc)
	w.WriteHeader(http.StatusCreated)
	return nil
}

func (s *Server) blobUploadPatch(repoStr, sessionID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, err := s.store.RepoGet(r.Context(), repoStr)
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
		bc, err := repo.BlobSession(sessionID)
		repo.Done()
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoBlobUploadUnknown("upload session not found"))
			s.log.Error("upload session not found", "repo", repoStr, "sessionID", sessionID)
			return
		}
		// check range if provided
		cr := r.Header.Get("content-range")
		if !blobValidRange(cr, bc.Size()) {
			w.Header().Set("range", fmt.Sprintf("0-%d", bc.Size()-1))
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			_ = types.ErrRespJSON(w, types.ErrInfoSizeInvalid(fmt.Sprintf("range is not valid, current range is 0-%d", bc.Size()-1)))
			s.log.Debug("blob patch content range invalid", "repo", repoStr, "sessionID", sessionID, "range", cr, "curEnd", bc.Size())
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
		if stateIn.Offset != bc.Size() {
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoBlobUploadInvalid("invalid state"))
			s.log.Error("invalid state size", "repo", repoStr, "sessionID", sessionID, "state", r.URL.Query().Get("state"), "sizeState", stateIn.Offset, "sizeCur", bc.Size())
			return
		}
		// write bytes to blob
		_, err = io.Copy(bc, r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Error("failed to write blob", "err", err, "repo", repoStr, "sessionID", sessionID)
			return
		}
		curEnd := bc.Size()
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
		repo, err := s.store.RepoGet(r.Context(), repoStr)
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
		bc, err := repo.BlobSession(sessionID)
		repo.Done()
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoBlobUploadUnknown("upload session not found"))
			s.log.Error("upload session not found", "repo", repoStr, "sessionID", sessionID)
			return
		}
		// check range if provided
		cr := r.Header.Get("content-range")
		if !blobValidRange(cr, bc.Size()) {
			w.Header().Set("range", fmt.Sprintf("0-%d", bc.Size()-1))
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			_ = types.ErrRespJSON(w, types.ErrInfoSizeInvalid(fmt.Sprintf("range is not valid, current range is 0-%d", bc.Size()-1)))
			s.log.Debug("blob put content range invalid", "repo", repoStr, "sessionID", sessionID, "range", cr, "curEnd", bc.Size())
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
		if stateIn.Offset != bc.Size() {
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoBlobUploadInvalid("invalid state"))
			s.log.Error("invalid state size", "repo", repoStr, "sessionID", sessionID, "state", r.URL.Query().Get("state"), "sizeState", stateIn.Offset, "sizeCur", bc.Size())
			return
		}
		// copy blob content
		_, err = io.Copy(bc, r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Error("failed to write blob", "err", err, "repo", repoStr, "sessionID", sessionID)
			return
		}
		// verify the digest and close or cancel
		err = bc.Verify(d)
		if err != nil {
			s.log.Error("invalid digest", "err", err, "repo", repoStr, "sessionID", sessionID, "expected", bc.Digest().String(), "received", d.String(), "size", bc.Size())
			if err = bc.Cancel(); err != nil {
				s.log.Error("canceling upload", "err", err, "repo", repoStr, "sessionID", sessionID, "expected", bc.Digest().String(), "received", d.String(), "size", bc.Size())
			}
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoBlobUploadInvalid("invalid digest, expected: "+bc.Digest().String()))
			return
		}
		err = bc.Close()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Error("failed to close blob upload", "err", err, "repo", repoStr, "sessionID", sessionID)
			return
		}
		loc, err := url.JoinPath("/v2", repoStr, "blobs", d.String())
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Error("failed to build location header", "err", err, "repo", repoStr, "sessionID", sessionID)
			return
		}
		w.Header().Set("location", loc)
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
