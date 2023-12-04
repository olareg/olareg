// Package store is used to interface with different types of storage (memory, disk)
package store

import "net/http"

type Store interface {
	RepoGet(repoStr string) Repo
}

type Repo interface {
	BlobGet(arg string) http.HandlerFunc
	ManifestGet(arg string) http.HandlerFunc
	TagList() http.HandlerFunc
}
