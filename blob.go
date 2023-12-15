package olareg

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	// imports required for go-digest
	_ "crypto/sha256"
	_ "crypto/sha512"

	"github.com/opencontainers/go-digest"

	"github.com/olareg/olareg/internal/store"
	"github.com/olareg/olareg/types"
)

func (s *server) blobGet(repoStr, arg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo := s.store.RepoGet(repoStr)
		d, err := digest.Parse(arg)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoDigestInvalid("digest cannot be parsed"))
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
		w.Header().Add("Docker-Content-Digest", d.String())
		// use ServeContent to handle range requests
		http.ServeContent(w, r, "", time.Time{}, rdr)
	}
}

// TODO: replace sessions with a cache that expires entries (with cancel) after some time and automatically cancels on shutdown.
// TODO: cache can also limit concurrent sessions.
var (
	blobUploadSessions = map[string]store.BlobCreator{}
	blobUploadMu       sync.Mutex
)

type blobUploadState struct {
	Offset int64 `json:"offset"`
}

func (s *server) blobUploadPost(repoStr string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// start a new upload session with the backend storage and track as current upload
		repo := s.store.RepoGet(repoStr)
		// TODO: check for mount=digest&from=repo, consider allowing anonymous blob mounts
		bOpts := []store.BlobOpt{}
		dStr := r.URL.Query().Get("digest")
		if dStr != "" {
			d, err := digest.Parse(dStr)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoDigestInvalid("invalid digest format"))
				s.log.Error("invalid digest", "err", err, "repo", repoStr)
				return
			}
			bOpts = append(bOpts, store.BlobWithDigest(d))
		}
		bc, err := repo.BlobCreate(bOpts...)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Error("create blob", "err", err)
			return
		}
		// TODO: handle monolithic upload when dStr is defined

		// generate a session to track blob upload between requests
		sb := make([]byte, 16)
		_, err = rand.Read(sb)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Error("failed to generate random session id", "err", err)
			bc.Cancel()
			return
		}
		sessionID := base64.RawURLEncoding.EncodeToString(sb)
		stateJSON, err := json.Marshal(blobUploadState{Offset: 0})
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Error("failed to marshal new state", "err", err)
			bc.Cancel()
			return
		}
		state := base64.RawURLEncoding.EncodeToString(stateJSON)
		blobUploadMu.Lock()
		defer blobUploadMu.Unlock()
		if _, ok := blobUploadSessions[repoStr+":"+sessionID]; ok {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Error("session id collision", "err", err)
			bc.Cancel()
			return
		}
		blobUploadSessions[repoStr+":"+sessionID] = bc
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

func (s *server) blobUploadPut(repoStr, sessionID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// repo is not needed since upload session tracks upload to repo
		// repo := s.store.RepoGet(repoStr)
		d, err := digest.Parse(r.URL.Query().Get("digest"))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoDigestInvalid("invalid or missing digest"))
			s.log.Error("invalid or missing digest", "err", err, "repo", repoStr, "sessionID", sessionID, "digest", r.URL.Query().Get("digest"))
			return
		}
		stateStr := r.URL.Query().Get("state")
		state := blobUploadState{}
		stateB, err := base64.RawURLEncoding.DecodeString(stateStr)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoBlobUploadInvalid("invalid state"))
			s.log.Error("invalid state", "err", err, "repo", repoStr, "sessionID", sessionID, "state", r.URL.Query().Get("state"))
			return
		}
		err = json.Unmarshal(stateB, &state)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoBlobUploadInvalid("invalid state"))
			s.log.Error("invalid state", "err", err, "repo", repoStr, "sessionID", sessionID, "state", r.URL.Query().Get("state"))
			return
		}
		// TODO: check for range header
		blobUploadMu.Lock()
		bc, ok := blobUploadSessions[repoStr+":"+sessionID]
		blobUploadMu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoBlobUploadUnknown("upload session not found"))
			s.log.Error("upload session not found", "repo", repoStr, "sessionID", sessionID)
			return
		}
		_, err = io.Copy(bc, r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Error("failed to write blob", "err", err, "repo", repoStr, "sessionID", sessionID)
			return
		}
		// verify the digest and close or cancel
		err = bc.Verify(d)
		if err != nil {
			bc.Cancel()
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoBlobUploadInvalid("invalid digest, expected: "+bc.Digest().String()))
			s.log.Error("invalid digest", "err", err, "repo", repoStr, "sessionID", sessionID, "expected", bc.Digest().String(), "received", d.String())
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
		// upon success, delete the blob session
		blobUploadMu.Lock()
		delete(blobUploadSessions, repoStr+":"+sessionID)
		blobUploadMu.Unlock()
	}
}
