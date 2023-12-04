package store

import "net/http"

type mem struct {
}

type memRepo struct {
}

// NewMem returns a memory store.
// TODO: include options for limiting memory usage, allowed methods, seed data.
func NewMem() Store {
	return &mem{}
}

func (m *mem) RepoGet(repoStr string) Repo {
	// TODO: lookup repo
	return &memRepo{}
}

func (mr *memRepo) BlobGet(arg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// TODO: validate arg, check for existence, and write or error
	}
}

func (mr *memRepo) ManifestGet(arg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// TODO: validate arg, check for existence, and write or error
	}
}

func (mr *memRepo) TagList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// TODO: validate arg, check for existence, and write or error
	}
}
