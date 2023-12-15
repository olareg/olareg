package olareg

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/opencontainers/go-digest"

	"github.com/olareg/olareg/internal/store"
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

func (s *server) manifestPut(repoStr, arg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tag := ""
		var dExpect digest.Digest
		addOpts := []types.IndexOpt{}
		repo := s.store.RepoGet(repoStr)
		// parse/validate headers
		mt := r.Header.Get("content-type")
		mt, _, _ = strings.Cut(mt, ";")
		mt = strings.TrimSpace(strings.ToLower(mt))
		switch mt {
		case types.MediaTypeDocker2Manifest, types.MediaTypeDocker2ManifestList,
			types.MediaTypeOCI1Manifest, types.MediaTypeOCI1ManifestList:
			// valid types, noop
		case "":
			// detect media type later if unset
		default:
			// fail fast
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoManifestInvalid("unsupported media type: "+mt))
			s.log.Debug("unsupported media type", "repo", repoStr, "arg", arg, "mediaType", mt)
			return
		}
		if r.ContentLength > s.conf.ManifestLimit {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			_ = types.ErrRespJSON(w, types.ErrInfoManifestInvalid(fmt.Sprintf("manifest too large, limited to %d bytes", s.conf.ManifestLimit)))
			return
		}
		// parse arg
		if types.RefTagRE.MatchString(arg) {
			tag = arg
		} else {
			var err error
			dExpect, err = digest.Parse(arg)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoDigestInvalid("tag or digest invalid"))
				s.log.Debug("failed to parse tag or digest", "repo", repoStr, "arg", arg, "err", err)
				return
			}
		}
		// read manifest
		rLimit := io.LimitReader(r.Body, s.conf.ManifestLimit)
		mRaw, err := io.ReadAll(rLimit)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Info("failed to read manifest", "repo", repoStr, "arg", arg, "err", err)
			return
		}
		// if mt == "", detect media type
		if mt == "" {
			mt = types.MediaTypeDetect(mRaw)
		}
		// parse and validate image or index contents
		switch mt {
		case types.MediaTypeOCI1Manifest, types.MediaTypeDocker2Manifest:
			m := types.Manifest{}
			err = json.Unmarshal(mRaw, &m)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoManifestInvalid("manifest could not be parsed"))
				s.log.Debug("failed to parse image manifest", "repo", repoStr, "arg", arg, "mediaType", mt, "err", err)
				return
			}
			// validate image blobs exist
			eList := s.manifestVerifyImage(repo, m)
			if eList != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, eList...)
				s.log.Debug("manifest blobs missing", "repo", repoStr, "arg", arg, "mediaType", mt, "errList", eList)
				return
			}
		case types.MediaTypeOCI1ManifestList, types.MediaTypeDocker2ManifestList:
			m := types.Index{}
			err = json.Unmarshal(mRaw, &m)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, types.ErrInfoManifestInvalid("manifest could not be parsed"))
				s.log.Debug("failed to parse image manifest", "repo", repoStr, "arg", arg, "mediaType", mt, "err", err)
				return
			}
			addOpts = append(addOpts, types.IndexWithChildren(m.Manifests))
			// validate manifests exist
			eList := s.manifestVerifyIndex(repo, m)
			if eList != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = types.ErrRespJSON(w, eList...)
				s.log.Debug("child manifests missing", "repo", repoStr, "arg", arg, "mediaType", mt, "errList", eList)
				return
			}
		default:
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoManifestInvalid("unsupported media type: "+mt))
			s.log.Debug("unsupported media type", "repo", repoStr, "arg", arg, "mediaType", mt)
			return
		}
		// verify / set digest
		dAlgo := digest.Canonical
		if dExpect != "" {
			dAlgo = dExpect.Algorithm()
		}
		d := dAlgo.FromBytes(mRaw)
		if dExpect != "" && d != dExpect {
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoDigestInvalid("digest mismatch, expected "+d.String()))
			s.log.Debug("content digest did not match request", "repo", repoStr, "arg", arg, "expect", d.String())
			return
		}
		// push to blob store
		bc, err := repo.BlobCreate(store.BlobWithDigest(d))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Info("failed to create blob", "repo", repoStr, "arg", arg, "err", err)
			return
		}
		_, err = bc.Write(mRaw)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Info("failed to write blob", "repo", repoStr, "arg", arg, "err", err)
			return
		}
		err = bc.Close()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Info("failed to close blob", "repo", repoStr, "arg", arg, "err", err)
			return
		}
		// add entry to index
		desc := types.Descriptor{
			MediaType: mt,
			Size:      int64(len(mRaw)),
			Digest:    d,
		}
		if tag != "" {
			desc.Annotations = map[string]string{
				types.AnnotRefName: tag,
			}
		}
		err = repo.IndexAdd(desc, addOpts...)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Info("failed to create index entry", "repo", repoStr, "arg", arg, "err", err)
			return
		}
		// set the location header
		loc, err := url.JoinPath("/v2", repoStr, "manifests", d.String())
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Error("failed to build location header for manifest put", "err", err, "repo", repoStr, "arg", arg)
			return
		}
		w.Header().Set("location", loc)
		w.WriteHeader(http.StatusCreated)
	}
}

func (s *server) manifestVerifyImage(repo store.Repo, m types.Manifest) []types.ErrorInfo {
	// TODO: allow validation to be disabled
	es := []types.ErrorInfo{}
	r, err := repo.BlobGet(m.Config.Digest)
	if err != nil {
		es = append(es, types.ErrInfoManifestBlobUnknown("config not found: "+m.Config.Digest.String()))
	} else {
		_ = r.Close()
	}
	for _, d := range m.Layers {
		r, err := repo.BlobGet(d.Digest)
		if err != nil {
			es = append(es, types.ErrInfoManifestBlobUnknown("layer not found: "+d.Digest.String()))
		} else {
			_ = r.Close()
		}
	}
	if len(es) > 0 {
		return es
	}
	return nil
}

func (s *server) manifestVerifyIndex(repo store.Repo, m types.Index) []types.ErrorInfo {
	// TODO: allow validation to be disabled
	es := []types.ErrorInfo{}
	for _, d := range m.Manifests {
		// TODO: allow sparse manifests
		r, err := repo.BlobGet(d.Digest)
		if err != nil {
			es = append(es, types.ErrInfoManifestBlobUnknown("manifest not found: "+d.Digest.String()))
		} else {
			_ = r.Close()
		}
	}
	if len(es) > 0 {
		return es
	}
	return nil
}
