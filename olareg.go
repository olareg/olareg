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

const (
	manifestLimitDefault = 8388608 // 8MiB
)

var (
	pathPart = `[a-z0-9]+(?:(?:\.|_|__|-+)[a-z0-9]+)*`
	rePath   = regexp.MustCompile(`^` + pathPart + `(?:\/` + pathPart + `)*$`)
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
	if s.conf.ManifestLimit <= 0 {
		s.conf.ManifestLimit = manifestLimitDefault
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
	resp.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	if _, ok := matchV2(pathEl); ok && (req.Method == http.MethodGet || req.Method == http.MethodHead) {
		// handle v2 ping
		s.v2Ping(resp, req)
		return
	} else if matches, ok := matchV2(pathEl, "...", "tags", "list"); ok && (req.Method == http.MethodGet || req.Method == http.MethodHead) {
		// handle tag listing
		s.tagList(matches[0]).ServeHTTP(resp, req)
		return
	} else if matches, ok := matchV2(pathEl, "...", "blobs", "*"); ok && (req.Method == http.MethodGet || req.Method == http.MethodHead) {
		// handle blob get
		s.blobGet(matches[0], matches[1]).ServeHTTP(resp, req)
		return
	} else if matches, ok := matchV2(pathEl, "...", "blobs", "uploads"); ok && req.Method == http.MethodPost {
		// handle blob post
		s.blobUploadPost(matches[0]).ServeHTTP(resp, req)
		return
	} else if matches, ok := matchV2(pathEl, "...", "blobs", "uploads", "*"); ok && req.Method == http.MethodPut {
		// handle blob put
		s.blobUploadPut(matches[0], matches[1]).ServeHTTP(resp, req)
		return
	} else if matches, ok := matchV2(pathEl, "...", "manifests", "*"); ok && (req.Method == http.MethodGet || req.Method == http.MethodHead) {
		// handle manifest get
		s.manifestGet(matches[0], matches[1]).ServeHTTP(resp, req)
		return
	} else if matches, ok := matchV2(pathEl, "...", "manifests", "*"); ok && req.Method == http.MethodPut {
		// handle manifest put
		s.manifestPut(matches[0], matches[1]).ServeHTTP(resp, req)
		return
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
			i += ((len(pathEl) - i) - (len(params) - j - 1) - 1)
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

func (s *server) v2Ping(resp http.ResponseWriter, req *http.Request) {
	resp.Header().Add("Content-Type", "application/json")
	resp.Header().Add("Content-Length", "2")
	resp.WriteHeader(200)
	if req.Method != http.MethodHead {
		_, _ = resp.Write([]byte("{}"))
	}
}
