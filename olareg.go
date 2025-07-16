package olareg

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/olareg/olareg/config"
	"github.com/olareg/olareg/internal/cache"
	"github.com/olareg/olareg/internal/httplog"
	"github.com/olareg/olareg/internal/sloghandle"
	"github.com/olareg/olareg/internal/store"
)

var (
	pathPart = `[a-z0-9]+(?:(?:\.|_|__|-+)[a-z0-9]+)*`
	rePath   = regexp.MustCompile(`^` + pathPart + `(?:\/` + pathPart + `)*$`)
	LogTrace = slog.LevelDebug - 4
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
	if conf.API.RateLimit > 0 {
		s.rateLimit = cache.New(cache.Opts[string, *rateLimitEntry]{
			Age: time.Second * 10,
		})
	}
	if s.log == nil {
		s.log = slog.New(sloghandle.Discard)
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
	log           *slog.Logger
	httpServer    *http.Server
	referrerCache *cache.Cache[referrerKey, referrerResponses]
	rateLimit     *cache.Cache[string, *rateLimitEntry]
}

type rateLimitEntry struct {
	first time.Time
	count int
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
		Handler:           httplog.New(s, s.log, LogTrace),
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
	for _, msg := range s.conf.API.Warnings {
		resp.Header().Add("Warning", "299 - \""+msg+"\"")
	}
	if s.conf.API.RateLimit > 0 {
		ip := req.Header.Get("X-Forwarded-For")
		if ip != "" {
			ip, _, _ = strings.Cut(ip, ", ")
		} else {
			ip = req.RemoteAddr
			portSep := strings.LastIndex(ip, ":")
			if portSep > 0 {
				ip = ip[:portSep]
			}
		}
		s.mu.Lock()
		now := time.Now()
		limit, err := s.rateLimit.Get(ip)
		count := 1
		if err != nil || limit == nil {
			// not found, make a new entry
			limit = &rateLimitEntry{
				first: now,
				count: count,
			}
			s.rateLimit.Set(ip, limit)
		} else {
			if now.Sub(limit.first) > time.Second {
				limit.count = count
				limit.first = now
			} else {
				limit.count++
				count = limit.count
			}
		}
		s.rateLimit.Set(ip, limit)
		s.mu.Unlock()
		if count > s.conf.API.RateLimit {
			// block, retry after 1 second
			resp.Header().Add("Retry-After", "1")
			resp.WriteHeader(http.StatusTooManyRequests)
			return
		}
	}
	var handler http.Handler
	repo := ""
	access := config.AuthUnknown
	if _, ok := matchV2(pathEl); ok && (req.Method == http.MethodGet || req.Method == http.MethodHead) {
		// handle v2 ping
		access = config.AuthRead
		handler = s.v2Ping()
	} else if matches, ok := matchV2(pathEl, "...", "manifests", "*"); ok {
		// handle manifest
		if req.Method == http.MethodGet || req.Method == http.MethodHead {
			repo = matches[0]
			access = config.AuthRead
			handler = s.manifestGet(matches[0], matches[1])
		} else if req.Method == http.MethodPut && *s.conf.API.PushEnabled {
			repo = matches[0]
			access = config.AuthWrite
			handler = s.manifestPut(matches[0], matches[1])
		} else if req.Method == http.MethodDelete && *s.conf.API.DeleteEnabled {
			repo = matches[0]
			access = config.AuthDelete
			handler = s.manifestDelete(matches[0], matches[1])
		} else {
			handler = methodNotAllowed()
		}
	} else if matches, ok := matchV2(pathEl, "...", "blobs", "*"); ok {
		if req.Method == http.MethodGet || req.Method == http.MethodHead {
			// handle blob get
			repo = matches[0]
			access = config.AuthRead
			handler = s.blobGet(matches[0], matches[1])
		} else if req.Method == http.MethodDelete && *s.conf.API.DeleteEnabled && *s.conf.API.Blob.DeleteEnabled {
			// handle blob delete
			repo = matches[0]
			access = config.AuthDelete
			handler = s.blobDelete(matches[0], matches[1])
		} else if matches[1] == "uploads" && req.Method == http.MethodPost && *s.conf.API.PushEnabled {
			// handle blob post
			repo = matches[0]
			access = config.AuthWrite
			handler = s.blobUploadPost(matches[0])
		} else {
			// other methods are not allowed (delete is done by GC)
			handler = methodNotAllowed()
		}
	} else if matches, ok := matchV2(pathEl, "...", "referrers", "*"); ok && *s.conf.API.Referrer.Enabled {
		if req.Method == http.MethodGet || req.Method == http.MethodHead {
			// handle referrer get
			repo = matches[0]
			access = config.AuthRead
			handler = s.referrerGet(matches[0], matches[1])
		} else {
			// other methods are not allowed
			handler = methodNotAllowed()
		}
	} else if matches, ok := matchV2(pathEl, "...", "tags", "list"); ok && (req.Method == http.MethodGet || req.Method == http.MethodHead) {
		// handle tag listing
		repo = matches[0]
		access = config.AuthRead
		handler = s.tagList(matches[0])
	} else if matches, ok := matchV2(pathEl, "...", "blobs", "uploads", "*"); ok && *s.conf.API.PushEnabled {
		// handle blob upload methods
		if req.Method == http.MethodPatch {
			repo = matches[0]
			access = config.AuthRead
			handler = s.blobUploadPatch(matches[0], matches[1])
		} else if req.Method == http.MethodPut {
			repo = matches[0]
			access = config.AuthRead
			handler = s.blobUploadPut(matches[0], matches[1])
		} else if req.Method == http.MethodGet {
			repo = matches[0]
			access = config.AuthRead
			handler = s.blobUploadGet(matches[0], matches[1])
		} else if req.Method == http.MethodDelete {
			repo = matches[0]
			access = config.AuthRead
			handler = s.blobUploadDelete(matches[0], matches[1])
		} else {
			handler = methodNotAllowed()
		}
	} else if len(pathEl) == 1 && pathEl[0] == "token" && s.conf.Auth.Token != nil {
		// internal token server
		s.conf.Auth.Token.ServeHTTP(resp, req)
		return
	}
	if handler == nil {
		resp.WriteHeader(http.StatusNotFound)
		s.log.Debug("unknown request", "method", req.Method, "path", pathEl)
		return
	}
	// wrap with auth handler if defined
	if s.conf.Auth.Handler != nil {
		handler = s.conf.Auth.Handler(repo, access, handler)
	}
	// TODO: include a status/health endpoint?
	// TODO: add handler wrappers: rate limit, logging, etc
	handler.ServeHTTP(resp, req)
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

func methodNotAllowed() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) v2Ping() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		w.Header().Add("Content-Length", "2")
		w.WriteHeader(200)
		if r.Method != http.MethodHead {
			_, _ = w.Write([]byte("{}"))
		}
	}
}
