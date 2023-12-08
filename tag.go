package olareg

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/olareg/olareg/types"
)

func (s *server) tagList(repoStr string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo := s.store.RepoGet(repoStr)
		index, err := repo.IndexGet()
		if err != nil {
			// TODO: handle different errors (perm denied, not found, internal server error)
			w.WriteHeader(http.StatusNotFound)
			_ = types.ErrRespJSON(w, types.ErrInfoNameUnknown("repository does not exist"))
			return
		}
		last := r.URL.Query().Get("last")
		w.Header().Add("Content-Type", "application/json")
		tl := types.TagList{
			Name: repoStr,
			Tags: []string{},
		}
		for _, d := range index.Manifests {
			if d.Annotations != nil && d.Annotations[types.AnnotRefName] != "" && strings.Compare(last, d.Annotations[types.AnnotRefName]) < 0 {
				tl.Tags = append(tl.Tags, d.Annotations[types.AnnotRefName])
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
			s.log.Warn("failed to marshal tag list", "err", err)
			return
		}
		w.Header().Add("Content-Length", fmt.Sprintf("%d", len(tlJSON)))
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, err = w.Write(tlJSON)
			if err != nil {
				s.log.Warn("failed to marshal tag list", "err", err)
			}
		}
	}
}
