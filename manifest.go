package olareg

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	// imports required for go-digest
	_ "crypto/sha256"
	_ "crypto/sha512"

	"github.com/opencontainers/go-digest"

	"github.com/olareg/olareg/internal/store"
	"github.com/olareg/olareg/types"
)

func (s *Server) manifestDelete(repoStr, arg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if *s.conf.Storage.ReadOnly {
			w.WriteHeader(http.StatusForbidden)
			_ = types.ErrRespJSON(w, types.ErrInfoDenied("repository is read-only"))
			return
		}
		repo, err := s.store.RepoGet(repoStr)
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
		defer repo.Done()
		index, err := repo.IndexGet()
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			_ = types.ErrRespJSON(w, types.ErrInfoNameUnknown("repository does not exist"))
			return
		}
		// get descriptor for arg from index
		desc, err := index.GetDesc(arg)
		if err != nil || desc.Digest.String() == "" {
			s.log.Debug("failed to get descriptor", "err", err, "repo", repoStr, "arg", arg)
			w.WriteHeader(http.StatusNotFound)
			_ = types.ErrRespJSON(w, types.ErrInfoManifestUnknown("tag or digest was not found in repository"))
			return
		}
		// if referrers is enabled, get the manifest to check for a subject
		if *s.conf.API.Referrer.Enabled {
			// wrap in a func to allow a return from errors without breaking the actual delete
			err = func() error {
				rdr, err := repo.BlobGet(desc.Digest)
				if err != nil {
					return err
				}
				raw, err := io.ReadAll(rdr)
				_ = rdr.Close()
				if err != nil {
					return err
				}
				subject, refDesc, err := types.ManifestReferrerDescriptor(raw, desc)
				if err != nil {
					if errors.Is(types.ErrNotFound, err) {
						return nil
					}
					return err
				}
				err = s.referrerDelete(repo, subject.Digest, refDesc)
				if err != nil {
					return err
				}
				return nil
			}()
			if err != nil {
				s.log.Info("failed to delete referrer", "repo", repoStr, "arg", arg, "err", err)
			}
		}
		// delete the digest or tag
		err = repo.IndexRemove(desc)
		if err != nil {
			s.log.Debug("failed to remove manifest", "repo", repoStr, "arg", arg, "err", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

func (s *Server) manifestGet(repoStr, arg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, err := s.store.RepoGet(repoStr)
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
		defer repo.Done()
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
			if r.Method != http.MethodHead {
				s.log.Debug("failed to get descriptor", "err", err, "repo", repoStr, "arg", arg)
			}
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

func (s *Server) manifestPut(repoStr, arg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if *s.conf.Storage.ReadOnly {
			w.WriteHeader(http.StatusForbidden)
			_ = types.ErrRespJSON(w, types.ErrInfoDenied("repository is read-only"))
			return
		}
		tag := ""
		var dExpect digest.Digest
		addOpts := []types.IndexOpt{}
		repo, err := s.store.RepoGet(repoStr)
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
		defer repo.Done()
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
		if r.ContentLength > s.conf.API.Manifest.Limit {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			_ = types.ErrRespJSON(w, types.ErrInfoManifestInvalid(fmt.Sprintf("manifest too large, limited to %d bytes", s.conf.API.Manifest.Limit)))
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
		rLimit := io.LimitReader(r.Body, s.conf.API.Manifest.Limit)
		mRaw, err := io.ReadAll(rLimit)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Info("failed to read manifest", "repo", repoStr, "arg", arg, "err", err)
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
		// if mt == "", detect media type
		if mt == "" {
			mt = types.MediaTypeDetect(mRaw)
		}
		// parse and validate image or index contents
		var subject digest.Digest
		var referrer *types.Descriptor
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
			if m.Subject != nil && m.Subject.Digest != "" && *s.conf.API.Referrer.Enabled {
				subject = m.Subject.Digest
				referrer = &types.Descriptor{
					MediaType:    mt,
					ArtifactType: m.ArtifactType,
					Size:         int64(len(mRaw)),
					Digest:       d,
					Annotations:  m.Annotations,
				}
				if m.ArtifactType == "" {
					referrer.ArtifactType = m.Config.MediaType
				}
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
			if m.Subject != nil && m.Subject.Digest != "" && *s.conf.API.Referrer.Enabled {
				subject = m.Subject.Digest
				referrer = &types.Descriptor{
					MediaType:    mt,
					ArtifactType: m.ArtifactType,
					Size:         int64(len(mRaw)),
					Digest:       d,
					Annotations:  m.Annotations,
				}
			}
		default:
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoManifestInvalid("unsupported media type: "+mt))
			s.log.Debug("unsupported media type", "repo", repoStr, "arg", arg, "mediaType", mt)
			return
		}
		// push to blob store
		bc, err := repo.BlobCreate(store.BlobWithDigest(d))
		if err != nil && !errors.Is(err, types.ErrBlobExists) {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Info("failed to create blob", "repo", repoStr, "arg", arg, "err", err)
			return
		} else if err == nil {
			_, err = bc.Write(mRaw)
			if err != nil {
				_ = bc.Close()
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
		err = repo.IndexInsert(desc, addOpts...)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Info("failed to create index entry", "repo", repoStr, "arg", arg, "err", err)
			return
		}
		// push/update referrer if detected
		if subject != "" {
			err = s.referrerAdd(repo, subject, *referrer)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				s.log.Info("failed to add referrer", "repo", repoStr, "arg", arg, "err", err)
				return
			}
			w.Header().Set("OCI-Subject", subject.String())
		}
		// set the location header
		loc, err := url.JoinPath("/v2", repoStr, "manifests", d.String())
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.Error("failed to build location header for manifest put", "err", err, "repo", repoStr, "arg", arg)
			return
		}
		w.Header().Set("location", loc)
		w.Header().Add("Docker-Content-Digest", d.String())
		w.WriteHeader(http.StatusCreated)
	}
}

func (s *Server) manifestVerifyImage(repo store.Repo, m types.Manifest) []types.ErrorInfo {
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

func (s *Server) manifestVerifyIndex(repo store.Repo, m types.Index) []types.ErrorInfo {
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
