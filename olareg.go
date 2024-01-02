package olareg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/olareg/olareg/config"
	"github.com/olareg/olareg/internal/slog"
	"github.com/olareg/olareg/internal/store"
)

const (
	manifestLimitDefault = 8388608 // 8MiB
)

var (
	pathPart = `[a-z0-9]+(?:(?:\.|_|__|-+)[a-z0-9]+)*`
	rePath   = regexp.MustCompile(`^` + pathPart + `(?:\/` + pathPart + `)*$`)
)

// New runs an http handler
func New(conf config.Config) *Server {
	s := &Server{
		conf: conf,
		log:  conf.Log,
	}
	if s.log == nil {
		s.log = slog.Null{}
	}
	if s.conf.API.Manifest.Limit <= 0 {
		s.conf.API.Manifest.Limit = manifestLimitDefault
	}
	switch conf.Storage.StoreType {
	case config.StoreMem:
		s.store = store.NewMem(s.conf, store.WithLog(s.log))
	case config.StoreDir:
		if s.conf.Storage.RootDir == "" {
			s.conf.Storage.RootDir = "."
		}
		s.store = store.NewDir(s.conf, store.WithLog(s.log))
	}
	return s
}

type Server struct {
	conf       config.Config
	store      store.Store
	log        slog.Logger
	httpServer *http.Server
	// TODO: implement disk cache, GC handling, etc for the non-disk and non-config data
}

func (s *Server) Run(ctx context.Context) error {
	if s.httpServer != nil {
		return fmt.Errorf("server is already running, run shutdown first")
	}
	s.log.Info("launching server", "addr", s.conf.HTTP.Addr)
	hs := &http.Server{
		Addr:              s.conf.HTTP.Addr,
		ReadHeaderTimeout: 5 * time.Second,
		Handler:           s,
	}
	s.httpServer = hs
	if ctx != nil {
		hs.BaseContext = func(l net.Listener) context.Context { return ctx }
	}
	var err error
	if s.conf.HTTP.CertFile != "" && s.conf.HTTP.KeyFile != "" {
		err = hs.ListenAndServeTLS(s.conf.HTTP.CertFile, s.conf.HTTP.KeyFile)
	} else {
		err = hs.ListenAndServe()
	}
	// graceful exit should not error
	if err != nil && errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer == nil {
		return fmt.Errorf("server is not running")
	}
	err := s.httpServer.Shutdown(ctx)
	s.httpServer = nil
	return err
}

func (s *Server) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	// parse request path, cleaning traversal attacks, and stripping leading and trailing slash
	pathEl := strings.Split(strings.Trim(path.Clean("/"+req.URL.Path), "/"), "/")
	resp.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	if _, ok := matchV2(pathEl); ok && (req.Method == http.MethodGet || req.Method == http.MethodHead) {
		// handle v2 ping
		s.v2Ping(resp, req)
		return
	} else if matches, ok := matchV2(pathEl, "...", "manifests", "*"); ok {
		// handle manifest
		if req.Method == http.MethodGet || req.Method == http.MethodHead {
			s.manifestGet(matches[0], matches[1]).ServeHTTP(resp, req)
			return
		} else if req.Method == http.MethodPut && boolDefault(s.conf.API.PushEnabled, true) {
			s.manifestPut(matches[0], matches[1]).ServeHTTP(resp, req)
			return
		} else if req.Method == http.MethodDelete && boolDefault(s.conf.API.DeleteEnabled, true) {
			s.manifestDelete(matches[0], matches[1]).ServeHTTP(resp, req)
			return
		} else {
			resp.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
	} else if matches, ok := matchV2(pathEl, "...", "blobs", "*"); ok {
		if req.Method == http.MethodGet || req.Method == http.MethodHead {
			// handle blob get
			s.blobGet(matches[0], matches[1]).ServeHTTP(resp, req)
			return
		} else if req.Method == http.MethodDelete && boolDefault(s.conf.API.DeleteEnabled, true) && boolDefault(s.conf.API.Blob.DeleteEnabled, false) {
			// handle blob delete
			s.blobDelete(matches[0], matches[1]).ServeHTTP(resp, req)
			return
		} else if matches[1] == "uploads" && req.Method == http.MethodPost && boolDefault(s.conf.API.PushEnabled, true) {
			// handle blob post
			s.blobUploadPost(matches[0]).ServeHTTP(resp, req)
			return
		} else {
			// other methods are not allowed (delete is done by GC)
			resp.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
	} else if matches, ok := matchV2(pathEl, "...", "referrers", "*"); ok && boolDefault(s.conf.API.Referrer.Enabled, true) {
		if req.Method == http.MethodGet || req.Method == http.MethodHead {
			// handle referrer get
			s.referrerGet(matches[0], matches[1]).ServeHTTP(resp, req)
			return
		} else {
			// other methods are not allowed
			resp.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
	} else if matches, ok := matchV2(pathEl, "...", "tags", "list"); ok && (req.Method == http.MethodGet || req.Method == http.MethodHead) {
		// handle tag listing
		s.tagList(matches[0]).ServeHTTP(resp, req)
		return
	} else if matches, ok := matchV2(pathEl, "...", "blobs", "uploads", "*"); ok && boolDefault(s.conf.API.PushEnabled, true) {
		// handle blob upload methods
		if req.Method == http.MethodPatch {
			s.blobUploadPatch(matches[0], matches[1]).ServeHTTP(resp, req)
			return
		} else if req.Method == http.MethodPut {
			s.blobUploadPut(matches[0], matches[1]).ServeHTTP(resp, req)
			return
		} else if req.Method == http.MethodGet {
			s.blobUploadGet(matches[0], matches[1]).ServeHTTP(resp, req)
			return
		} else {
			resp.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
	} else {
		resp.WriteHeader(http.StatusNotFound)
		// TODO: remove response, this is for debugging/development
		errResp := struct {
			Method string
			Path   []string
		}{
			Method: req.Method,
			Path:   pathEl,
		}
		_ = json.NewEncoder(resp).Encode(errResp)
	}
	// TODO: include a status/health endpoint?
	// TODO: add handler wrappers: auth, rate limit, etc
}

func matchV2(pathEl []string, params ...string) ([]string, bool) {
	matches := make([]string, 0, len(pathEl)-1)
	if pathEl[0] != "v2" || len(pathEl) < len(params)+1 {
		return nil, false
	}
	i := 1
	repoStr := ""
	for j, p := range params {
		switch p {
		case "...": // repo value
			// from current pos to num of path elements - remaining params
			repoStr = strings.Join(pathEl[i:len(pathEl)-(len(params)-j-1)], "/")
			// offset remaining path - remaining params - 1 (handle the later ++)
			i = len(pathEl) - (len(params) - j - 1) - 1
			matches = append(matches, repoStr)
		case "*": // match any single arg
			matches = append(matches, pathEl[i])
		default:
			if pathEl[i] != p {
				return nil, false
			}
		}
		i++
	}
	if len(pathEl) != i {
		// not all path fields were matched
		return nil, false
	}
	if repoStr != "" && !rePath.MatchString(repoStr) {
		return nil, false
	}
	return matches, true
}

func (s *Server) v2Ping(resp http.ResponseWriter, req *http.Request) {
	resp.Header().Add("Content-Type", "application/json")
	resp.Header().Add("Content-Length", "2")
	resp.WriteHeader(200)
	if req.Method != http.MethodHead {
		_, _ = resp.Write([]byte("{}"))
	}
}

func boolDefault(b *bool, def bool) bool {
	if b != nil {
		return *b
	}
	return def
}
