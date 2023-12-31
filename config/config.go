// Package config contains data types for configuration of olareg.
package config

import (
	"fmt"
	"strings"

	"github.com/olareg/olareg/internal/slog"
)

const (
	manifestLimitDefault = 8388608 // 8MiB
)

type Store int

const (
	StoreUndef  Store = iota // undefined backend storage is the invalid zero value
	StoreMem                 // StoreMem only uses memory for an ephemeral registry
	StoreDir                 // StoreDir tracks each repository with a separate blob store
	StoreShared              // StoreShared tracks the blobs in a single store for less disk usage
)

type Config struct {
	HTTP    ConfigHTTP
	Storage ConfigStorage
	API     ConfigAPI
	Log     slog.Logger
	// TODO: GC policy, delete untagged? timeouts for partial blobs?
	// TODO: proxy settings, pull only, or push+pull cache
	// TODO: memory option to load from disk
	// TODO: auth options (basic, bearer)
}

type ConfigHTTP struct {
	Addr     string // address and port to listen on, e.g. ":5000"
	CertFile string // public certificate for https server (leave blank for http)
	KeyFile  string // private key for https server (leave blank for http)
}

type ConfigStorage struct {
	StoreType Store
	RootDir   string
	ReadOnly  *bool
}

type ConfigAPI struct {
	PushEnabled   *bool
	DeleteEnabled *bool
	Manifest      ConfigAPIManifest
	Blob          ConfigAPIBlob
	Referrer      ConfigAPIReferrer
}

type ConfigAPIManifest struct {
	Limit int64
}

type ConfigAPIBlob struct {
	DeleteEnabled *bool
}

type ConfigAPIReferrer struct {
	Enabled *bool
}

func (c *Config) SetDefaults() {
	t := true
	f := false
	if c.API.DeleteEnabled == nil {
		c.API.DeleteEnabled = &t
	}
	if c.API.PushEnabled == nil {
		c.API.PushEnabled = &t
	}
	if c.API.Blob.DeleteEnabled == nil {
		c.API.Blob.DeleteEnabled = &f
	}
	if c.API.Referrer.Enabled == nil {
		c.API.Referrer.Enabled = &t
	}
	if c.Storage.ReadOnly == nil {
		c.Storage.ReadOnly = &f
	}
	if c.API.Manifest.Limit <= 0 {
		c.API.Manifest.Limit = manifestLimitDefault
	}
	switch c.Storage.StoreType {
	case StoreDir:
		if c.Storage.RootDir == "" {
			c.Storage.RootDir = "."
		}
	}
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
