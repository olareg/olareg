package olareg

import (
	"encoding/json"
	"net/http"
	"path"
	"regexp"
	"strings"

	"github.com/olareg/olareg/internal/slog"
	"github.com/olareg/olareg/internal/store"
)

var (
	rePathPart = regexp.MustCompile(`^[a-z0-9]+(?:(?:\.|_|__|-+)[a-z0-9]+)*$`)
)

// New runs an http handler
func New(conf Config) http.Handler {
	s := &server{
		conf: conf,
		log:  conf.Log,
	}
	if s.log == nil {
		s.log = slog.Null{}
	}
	switch conf.StoreType {
	// case StoreMem:
	// 	s.store = store.NewMem()
	case StoreDir:
		if s.conf.RootDir == "" {
			s.conf.RootDir = "."
		}
		s.store = store.NewDir(s.conf.RootDir, store.WithDirLog(s.log))
	}
	return s
}

type server struct {
	conf  Config
	store store.Store
	log   slog.Logger
	// TODO: add context?
	// TODO: implement memory store, disk cache, GC handling, etc for the non-disk and non-config data
}

func (s *server) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	// parse request path, cleaning traversal attacks, and stripping leading and trailing slash
	pathEl := strings.Split(strings.Trim(path.Clean("/"+req.URL.Path), "/"), "/")
	validAPI := false
	repoStr := ""
	action := ""
	arg := ""
	if len(pathEl) >= 4 && pathEl[0] == "v2" {
		validAPI = true
		repoStr = strings.Join(pathEl[1:len(pathEl)-2], "/")
		action = pathEl[len(pathEl)-2]
		arg = pathEl[len(pathEl)-1]
		for _, part := range pathEl[1 : len(pathEl)-2] {
			if !rePathPart.MatchString(part) {
				validAPI = false
				break
			}
		}
	}
	resp.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	switch {
	case len(pathEl) == 1 && pathEl[0] == "v2" && (req.Method == http.MethodGet || req.Method == http.MethodHead):
		// handle v2 ping
		s.v2Ping(resp, req)
		return
	case validAPI && action == "tags" && arg == "list" && (req.Method == http.MethodGet || req.Method == http.MethodHead):
		// handle tag listing
		s.tagList(repoStr).ServeHTTP(resp, req)
		return
	case validAPI && action == "blobs" && (req.Method == http.MethodGet || req.Method == http.MethodHead):
		// handle blob get
		s.blobGet(repoStr, arg).ServeHTTP(resp, req)
		return
	case validAPI && action == "manifests" && (req.Method == http.MethodGet || req.Method == http.MethodHead):
		// handle manifest get
		s.manifestGet(repoStr, arg).ServeHTTP(resp, req)
		return
	default:
		resp.WriteHeader(http.StatusNotFound)
		// TODO: remove response, this is for debugging/development
		errResp := struct {
			Valid  bool
			Method string
			Action string
			Arg    string
			Path   []string
		}{
			Valid:  validAPI,
			Method: req.Method,
			Action: action,
			Arg:    arg,
			Path:   pathEl,
		}
		_ = json.NewEncoder(resp).Encode(errResp)
	}
	// TODO: include a status/health endpoint?
	// TODO: add handler wrappers: auth, rate limit, etc
}

func (s *server) v2Ping(resp http.ResponseWriter, req *http.Request) {
	resp.Header().Add("Content-Type", "application/json")
	resp.Header().Add("Content-Length", "2")
	resp.WriteHeader(200)
	if req.Method != http.MethodHead {
		_, _ = resp.Write([]byte("{}"))
	}
}
