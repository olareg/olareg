package store

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	// imports required for go-digest
	_ "crypto/sha256"
	_ "crypto/sha512"

	"github.com/opencontainers/go-digest"

	"github.com/olareg/olareg/internal/slog"
	"github.com/olareg/olareg/types"
)

const (
	freqCheck    = time.Second
	indexFile    = "index.json"
	layoutFile   = "oci-layout"
	annotRefName = "org.opencontainers.image.ref.name"
)

var (
	reTag = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}$`)
)

type dir struct {
	mu    sync.Mutex
	root  string
	repos map[string]*dirRepo // TODO: switch to storing these in a cache that expires from memory
	log   slog.Logger
}

type dirRepo struct {
	timeCheck time.Time
	timeMod   time.Time
	mu        sync.Mutex
	name      string
	path      string
	exists    bool
	index     types.Index
	tags      map[string]types.Descriptor
	manifests map[digest.Digest]types.Descriptor
	log       slog.Logger
}

// OptDir includes options for the directory store.
type OptDir func(*dir)

// NewDir returns a directory store.
func NewDir(root string, opts ...OptDir) Store {
	d := &dir{
		root:  root,
		repos: map[string]*dirRepo{},
	}
	for _, opt := range opts {
		opt(d)
	}
	if d.log == nil {
		d.log = slog.Null{}
	}
	return d
}

// WithDirLog includes a logger on the directory store.
func WithDirLog(log slog.Logger) OptDir {
	return func(d *dir) {
		d.log = log
	}
}

// TODO: include options for memory caching, allowed methods.

func (d *dir) RepoGet(repoStr string) Repo {
	d.mu.Lock()
	defer d.mu.Unlock()
	if dr, ok := d.repos[repoStr]; ok {
		return dr
	}
	dr := dirRepo{
		path:      filepath.Join(d.root, repoStr),
		name:      repoStr,
		manifests: map[digest.Digest]types.Descriptor{},
		tags:      map[string]types.Descriptor{},
		log:       d.log,
	}
	d.repos[repoStr] = &dr
	statDir, err := os.Stat(dr.path)
	if err == nil && statDir.IsDir() {
		statIndex, errIndex := os.Stat(filepath.Join(dr.path, indexFile))
		statLayout, errLayout := os.Stat(filepath.Join(dr.path, layoutFile))
		// TODO: validate content of index and layout
		if errIndex == nil && errLayout == nil && !statIndex.IsDir() && !statLayout.IsDir() {
			dr.exists = true
		}
	}
	return &dr
}

// BlobGet returns a requested blob by digest.
func (dr *dirRepo) BlobGet(arg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !dr.exists {
			w.WriteHeader(http.StatusNotFound)
			_ = types.ErrRespJSON(w, types.ErrInfoNameUnknown("repository does not exist"))
			return
		}
		d, err := digest.Parse(arg)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = types.ErrRespJSON(w, types.ErrInfoDigestInvalid("digest cannot be parsed"))
			return
		}
		fh, err := os.Open(filepath.Join(dr.path, "blobs", d.Algorithm().String(), d.Encoded()))
		if err != nil {
			dr.log.Info("failed to open blob", "err", err)
			if os.IsNotExist(err) {
				w.WriteHeader(http.StatusNotFound)
				_ = types.ErrRespJSON(w, types.ErrInfoBlobUnknown("blob was not found"))
			} else {
				w.WriteHeader(http.StatusInternalServerError)
			}
			return
		}
		defer fh.Close()
		w.Header().Add("Content-Type", "application/octet-stream")
		w.Header().Add("Docker-Content-Digest", d.String())
		// use ServeContent to handle range requests
		http.ServeContent(w, r, "", time.Time{}, fh)
	}
}

// ManifestGet returns a requested manifest by tag or digest.
func (dr *dirRepo) ManifestGet(arg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := dr.repoLoad(false)
		if err != nil || !dr.exists {
			w.WriteHeader(http.StatusNotFound)
			_ = types.ErrRespJSON(w, types.ErrInfoNameUnknown("repository does not exist"))
			return
		}
		// get the descriptor
		desc, err := dr.descGet(arg)
		if err != nil || desc.Digest.String() == "" {
			dr.log.Info("failed to get descriptor", "err", err)
			w.WriteHeader(http.StatusNotFound)
			_ = types.ErrRespJSON(w, types.ErrInfoManifestUnknown("tag or digest was not found in repository"))
			return
		}
		// if desc does not match requested accept header
		acceptList := r.Header.Values("Accept")
		if !types.MediaTypeAccepts(desc.MediaType, acceptList) {
			// if accept header is defined, desc is an index, and arg is a tag
			if len(acceptList) > 0 && types.MediaTypeIndex(desc.MediaType) && reTag.MatchString(arg) {
				// parse the index to find a matching media type
				i := types.Index{}
				fh, err := os.Open(filepath.Join(dr.path, "blobs", desc.Digest.Algorithm().String(), desc.Digest.Encoded()))
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				defer fh.Close()
				err = json.NewDecoder(fh).Decode(&i)
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
		fh, err := os.Open(filepath.Join(dr.path, "blobs", desc.Digest.Algorithm().String(), desc.Digest.Encoded()))
		if err != nil {
			dr.log.Info("failed to open manifest blob", "err", err)
			if os.IsNotExist(err) {
				w.WriteHeader(http.StatusNotFound)
				_ = types.ErrRespJSON(w, types.ErrInfoManifestBlobUnknown("requested manifest was not found in blob store"))
			} else {
				w.WriteHeader(http.StatusInternalServerError)
			}
			return
		}
		defer fh.Close()
		w.Header().Add("Content-Type", desc.MediaType)
		w.Header().Add("Docker-Content-Digest", desc.Digest.String())
		// use ServeContent to handle range requests
		http.ServeContent(w, r, "", time.Time{}, fh)
	}
}

// TagList returns the list of tags.
func (dr *dirRepo) TagList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := dr.repoLoad(false)
		if err != nil || !dr.exists {
			w.WriteHeader(http.StatusNotFound)
			_ = types.ErrRespJSON(w, types.ErrInfoNameUnknown("repository does not exist"))
			return
		}
		last := r.URL.Query().Get("last")
		w.Header().Add("Content-Type", "application/json")
		tl := types.TagList{
			Name: dr.name,
			Tags: []string{},
		}
		for t := range dr.tags {
			if strings.Compare(last, t) < 0 {
				tl.Tags = append(tl.Tags, t)
			}
		}
		sort.Strings(tl.Tags)
		n := r.URL.Query().Get("n")
		if n != "" {
			if nInt, err := strconv.Atoi(n); err == nil && len(tl.Tags) > nInt {
				tl.Tags = tl.Tags[:nInt]
				// add next header for pagination
				next := r.URL
				q := next.Query()
				q.Set("last", tl.Tags[len(tl.Tags)-1])
				next.RawQuery = q.Encode()
				w.Header().Add("Link", fmt.Sprintf("%s; rel=next", next.String()))
			}
		}
		tlJSON, err := json.Marshal(tl)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			dr.log.Warn("failed to marshal tag list", "err", err)
			return
		}
		w.Header().Add("Content-Length", fmt.Sprintf("%d", len(tlJSON)))
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, err = w.Write(tlJSON)
			if err != nil {
				dr.log.Warn("failed to marshal tag list", "err", err)
			}
		}
	}
}

func (dr *dirRepo) descGet(arg string) (types.Descriptor, error) {
	dr.mu.Lock()
	defer dr.mu.Unlock()
	var dZero types.Descriptor
	if reTag.MatchString(arg) {
		// search for tag
		if dr.tags == nil {
			return dZero, types.ErrNotFound
		}
		if d, ok := dr.tags[arg]; ok {
			return d, nil
		}
		dr.log.Info("failed to get descriptor by tag", "arg", arg)
	} else {
		// else, attempt to parse digest
		dig, err := digest.Parse(arg)
		if err != nil {
			return dZero, err
		}
		if d, ok := dr.manifests[dig]; ok {
			return d, nil
		}
		dr.log.Info("failed to get descriptor by digest", "arg", arg)
	}
	return dZero, types.ErrNotFound
}

func (dr *dirRepo) repoLoad(force bool) error {
	dr.mu.Lock()
	defer dr.mu.Unlock()
	if !force && time.Since(dr.timeCheck) < freqCheck {
		return nil
	}
	// TODO: verify oci-layout existence and content
	fh, err := os.Open(filepath.Join(dr.path, indexFile))
	if err != nil {
		return err
	}
	defer fh.Close()
	stat, err := fh.Stat()
	if err != nil {
		return err
	}
	dr.timeCheck = time.Now()
	if dr.timeMod == stat.ModTime() {
		return nil
	}
	err = json.NewDecoder(fh).Decode(&dr.index)
	if err != nil {
		return err
	}
	dr.manifests = map[digest.Digest]types.Descriptor{}
	dr.tags = map[string]types.Descriptor{}
	dr.exists = true
	for _, d := range dr.index.Manifests {
		if _, ok := dr.manifests[d.Digest]; !ok {
			dr.manifests[d.Digest] = types.Descriptor{
				MediaType: d.MediaType,
				Digest:    d.Digest,
				Size:      d.Size,
			}
		}
		if d.Annotations != nil && d.Annotations[annotRefName] != "" {
			dr.tags[d.Annotations[annotRefName]] = types.Descriptor{
				MediaType: d.MediaType,
				Digest:    d.Digest,
				Size:      d.Size,
			}
		}
		if types.MediaTypeIndex(d.MediaType) {
			err = dr.repoLoadIndex(d)
			if err != nil {
				return err // TODO: ignore, log, or gather multiple errors?
			}
		}
	}
	dr.timeMod = stat.ModTime()
	return nil
}

func (dr *dirRepo) repoLoadIndex(d types.Descriptor) error {
	fh, err := os.Open(filepath.Join(dr.path, "blobs", d.Digest.Algorithm().String(), d.Digest.Encoded()))
	if err != nil {
		return err
	}
	i := types.Index{}
	err = json.NewDecoder(fh).Decode(&i)
	_ = fh.Close() // close here rather than defer, to avoid open fh during recursion
	if err != nil {
		return err
	}
	for _, di := range i.Manifests {
		dCur := types.Descriptor{
			MediaType: di.MediaType,
			Digest:    di.Digest,
			Size:      di.Size,
		}
		dr.log.Debug("loading descriptor", "digest", dCur.Digest.String(), "parent", d.Digest.String())
		// if _, ok := dr.manifests[dCur.Digest]; ok {
		// 	continue // don't reprocess digests
		// }
		dr.manifests[dCur.Digest] = dCur
		switch {
		case types.MediaTypeIndex(dCur.MediaType):
			// TODO: gather multiple errors and process as much as possible?
			err = dr.repoLoadIndex(dCur)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
