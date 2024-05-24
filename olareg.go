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
	"sync"
	"time"

	"github.com/olareg/olareg/config"
	"github.com/olareg/olareg/internal/cache"
	"github.com/olareg/olareg/internal/slog"
	"github.com/olareg/olareg/internal/store"
)

var (
	pathPart = `[a-z0-9]+(?:(?:\.|_|__|-+)[a-z0-9]+)*`
	rePath   = regexp.MustCompile(`^` + pathPart + `(?:\/` + pathPart + `)*$`)
)

// New returns a Server.
// Ensure the resource is cleaned up with either [Server.Close] or [Server.Shutdown].
func New(conf config.Config) *Server {
	conf.SetDefaults()
	s := &Server{
		conf: conf,
		log:  conf.Log,
		referrerCache: cache.New[referrerKey, referrerResponses](cache.Opts[referrerKey, referrerResponses]{
			Age:   conf.API.Referrer.PageCacheExpire,
			Count: conf.API.Referrer.PageCacheLimit,
		}),
	}
	if s.log == nil {
		s.log = slog.Null{}
	}
	switch s.conf.Storage.StoreType {
	case config.StoreMem:
		s.store = store.NewMem(s.conf, store.WithLog(s.log))
	case config.StoreDir:
		s.store = store.NewDir(s.conf, store.WithLog(s.log))
	}
	return s
}

type Server struct {
	mu            sync.Mutex
	conf          config.Config
	store         store.Store
	log           slog.Logger
	httpServer    *http.Server
	referrerCache *cache.Cache[referrerKey, referrerResponses]
}

// Close is used to release the backend store resources.
func (s *Server) Close() error {
	if s.store == nil {
		return fmt.Errorf("backend store was already closed")
	}
	err := s.store.Close()
	s.store = nil
	return err
}

// Run starts a listener and serves requests.
// It only returns after a call to [Server.Shutdown].
func (s *Server) Run(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Unlock()
	var err error
	if s.conf.HTTP.CertFile != "" && s.conf.HTTP.KeyFile != "" {
		err = hs.ListenAndServeTLS(s.conf.HTTP.CertFile, s.conf.HTTP.KeyFile)
	} else {
		err = hs.ListenAndServe()
	}
	s.mu.Lock()
	// graceful exit should not error
	if err != nil && errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

// Shutdown is used to stop the http listener and close the backend store.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.httpServer == nil {
		return fmt.Errorf("server is not running")
	}
	err := s.httpServer.Shutdown(ctx)
	s.httpServer = nil
	if err != nil {
		return err
	}
	if s.store != nil {
		err = s.store.Close()
		s.store = nil
	}
	return err
}

// ServeHTTP handles requests to the OCI registry.
func (s *Server) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	if s.store == nil {
		s.log.Error("ServeHTTP called without a backend store")
		resp.WriteHeader(http.StatusInternalServerError)
		return
	}
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
		} else if req.Method == http.MethodPut && *s.conf.API.PushEnabled {
			s.manifestPut(matches[0], matches[1]).ServeHTTP(resp, req)
			return
		} else if req.Method == http.MethodDelete && *s.conf.API.DeleteEnabled {
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
		} else if req.Method == http.MethodDelete && *s.conf.API.DeleteEnabled && *s.conf.API.Blob.DeleteEnabled {
			// handle blob delete
			s.blobDelete(matches[0], matches[1]).ServeHTTP(resp, req)
			return
		} else if matches[1] == "uploads" && req.Method == http.MethodPost && *s.conf.API.PushEnabled {
			// handle blob post
			s.blobUploadPost(matches[0]).ServeHTTP(resp, req)
			return
		} else {
			// other methods are not allowed (delete is done by GC)
			resp.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
	} else if matches, ok := matchV2(pathEl, "...", "referrers", "*"); ok && *s.conf.API.Referrer.Enabled {
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
	} else if matches, ok := matchV2(pathEl, "...", "blobs", "uploads", "*"); ok && *s.conf.API.PushEnabled {
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
		} else if req.Method == http.MethodDelete {
			s.blobUploadDelete(matches[0], matches[1]).ServeHTTP(resp, req)
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
