package olareg

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/olareg/olareg/types"
)

func (s *server) manifestGet(repoStr, arg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo := s.store.RepoGet(repoStr)
		index, err := repo.IndexGet()
		if err != nil {
			// TODO: handle different errors (perm denied, not found, internal server error)
			w.WriteHeader(http.StatusNotFound)
			_ = types.ErrRespJSON(w, types.ErrInfoNameUnknown("repository does not exist"))
			return
		}
		// get descriptor for arg from index
		desc, err := index.GetDesc(arg)
		if err != nil || desc.Digest.String() == "" {
			s.log.Info("failed to get descriptor", "err", err, "repo", repoStr, "arg", arg)
			w.WriteHeader(http.StatusNotFound)
			_ = types.ErrRespJSON(w, types.ErrInfoManifestUnknown("tag or digest was not found in repository"))
			return
		}
		// if desc does not match requested accept header
		acceptList := r.Header.Values("Accept")
		if !types.MediaTypeAccepts(desc.MediaType, acceptList) {
			// if accept header is defined, desc is an index, and arg is a tag
			if len(acceptList) > 0 && types.MediaTypeIndex(desc.MediaType) && types.RefTagRE.MatchString(arg) {
				// parse the index to find a matching media type
				i := types.Index{}
				rdr, err := repo.BlobGet(desc.Digest)
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				defer rdr.Close()
				err = json.NewDecoder(rdr).Decode(&i)
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				found := false
				for _, d := range i.Manifests {
					if types.MediaTypeAccepts(d.MediaType, acceptList) {
						// use first match if found
						desc = d
						found = true
						break
					}
				}
				if !found {
					w.WriteHeader(http.StatusNotFound)
					_ = types.ErrRespJSON(w, types.ErrInfoManifestUnknown("requested media type not found, available media type is "+desc.MediaType))
					return
				}
			} else {
				w.WriteHeader(http.StatusNotFound)
				_ = types.ErrRespJSON(w, types.ErrInfoManifestUnknown("requested media type not found, available media type is "+desc.MediaType))
				return
			}
		}
		rdr, err := repo.BlobGet(desc.Digest)
		if err != nil {
			s.log.Info("failed to open manifest blob", "err", err)
			if os.IsNotExist(err) {
				w.WriteHeader(http.StatusNotFound)
				_ = types.ErrRespJSON(w, types.ErrInfoManifestBlobUnknown("requested manifest was not found in blob store"))
			} else {
				w.WriteHeader(http.StatusInternalServerError)
			}
			return
		}
		defer rdr.Close()
		w.Header().Add("Content-Type", desc.MediaType)
		w.Header().Add("Docker-Content-Digest", desc.Digest.String())
		// use ServeContent to handle range requests
		http.ServeContent(w, r, "", time.Time{}, rdr)
	}
}
