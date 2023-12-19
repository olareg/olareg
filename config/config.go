// Package config contains data types for configuration of olareg.
package config

import (
	"fmt"
	"strings"

	"github.com/olareg/olareg/internal/slog"
)

type Store int

const (
	StoreMem    Store = iota // StoreMem only uses memory for an ephemeral registry
	StoreDir                 // StoreDir tracks each repository with a separate blob store
	StoreShared              // StoreShared tracks the blobs in a single store for less disk usage
)

type Config struct {
	StoreType     Store
	RootDir       string
	Log           slog.Logger
	ManifestLimit int64
	API           ConfigAPI
	// TODO: TLS and listener options? not needed here if only providing handler
	// TODO: GC policy, delete untagged? timeouts for partial blobs?
	// TODO: proxy settings, pull only, or push+pull cache
	// TODO: memory option to load from disk
	// TODO: auth options (basic, bearer)
	// TODO: allowed actions: get/head, put, delete, catalog
}

type ConfigAPI struct {
	BlobDelete ConfigAPIBlobDelete
	Referrer   ConfigAPIReferrer
}

type ConfigAPIBlobDelete struct {
	Enabled *bool
}

type ConfigAPIReferrer struct {
	Enabled *bool
}

func (s Store) MarshalText() ([]byte, error) {
	var ret string
	switch s {
	case StoreMem:
		ret = "mem"
	case StoreDir:
		ret = "dir"
	case StoreShared:
		ret = "shared"
	}
	if ret == "" {
		return []byte{}, fmt.Errorf("unknown store value %d", int(s))
	}
	return []byte(ret), nil
}

func (s *Store) UnmarshalText(b []byte) error {
	switch strings.ToLower(string(b)) {
	default:
		return fmt.Errorf("unknown store value \"%s\"", b)
	case "mem":
		*s = StoreMem
	case "dir":
		*s = StoreDir
	case "shared":
		*s = StoreShared
	}
	return nil
}
