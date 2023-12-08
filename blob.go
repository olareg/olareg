package olareg

import (
	"errors"
	"net/http"
	"time"

	// imports required for go-digest
	_ "crypto/sha256"
	_ "crypto/sha512"

	"github.com/opencontainers/go-digest"

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
			s.log.Info("failed to open blob", "err", err, "repo", repoStr, "digest", d.String())
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
