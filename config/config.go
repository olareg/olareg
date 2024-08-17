// Package config contains data types for configuration of olareg.
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/olareg/olareg/internal/slog"
)

const (
	manifestLimitDefault       = 1024 * 1024 * 8
	referrerCacheExpireDefault = time.Minute * 5
	referrerCacheLimitDefault  = 1000
	referrersLimitDefault      = 1024 * 1024 * 4
	repoUploadMaxDefault       = 1000
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
	// TODO: proxy settings, pull only, or push+pull cache
	// TODO: auth options (basic, bearer)
}

type ConfigHTTP struct {
	Addr     string // address and port to listen on, e.g. ":5000"
	CertFile string // public certificate for https server (leave blank for http)
	KeyFile  string // private key for https server (leave blank for http)
}

type ConfigStorage struct {
	StoreType Store
	RootDir   string   // root directory for filesystem backed storage
	ReadOnly  *bool    // read only disables all writes to the backing filesystem
	GC        ConfigGC // garbage collection policy
}

type ConfigGC struct {
	Frequency         time.Duration // frequency to run garbage collection, disable gc with a negative value
	GracePeriod       time.Duration // time to preserve recently pushed manifests and blobs, disable with a negative value
	RepoUploadMax     int           // limit on number of concurrent uploads to a repository, unlimited with a negative value
	Untagged          *bool         // delete untagged manifests
	EmptyRepo         *bool         // delete empty repo
	ReferrersDangling *bool         // delete referrers when manifest does not exist
	ReferrersWithSubj *bool         // delete referrers response when subject manifest is deleted
}

type ConfigAPI struct {
	PushEnabled   *bool // enable push to repository, enabled by default
	DeleteEnabled *bool // enable deletion, disabled by default
	Manifest      ConfigAPIManifest
	Blob          ConfigAPIBlob
	Referrer      ConfigAPIReferrer
	Warnings      []string
	RateLimit     int // number of requests per second from a given IP address
}

type ConfigAPIManifest struct {
	Limit int64 // max size of a manifest, default is 8MB (manifestLimitDefault)
}

type ConfigAPIBlob struct {
	DeleteEnabled *bool // enable blob deletion, disabled by default, ConfigAPI.DeleteEnabled must also be true
}

type ConfigAPIReferrer struct {
	Enabled         *bool         // enable referrer API, enabled by default
	PageCacheExpire time.Duration // time to save pages for a paged response
	PageCacheLimit  int           // max number of paged responses to keep in memory
	Limit           int64         // max size of a referrers response (OCI recommends 4MiB)
}

func (c *Config) SetDefaults() {
	c.API.DeleteEnabled = boolDefault(c.API.DeleteEnabled, false)
	c.API.PushEnabled = boolDefault(c.API.PushEnabled, true)
	c.API.Blob.DeleteEnabled = boolDefault(c.API.Blob.DeleteEnabled, false)
	c.API.Referrer.Enabled = boolDefault(c.API.Referrer.Enabled, true)
	if c.API.Manifest.Limit <= 0 {
		c.API.Manifest.Limit = manifestLimitDefault
	}
	if c.API.Referrer.PageCacheExpire == 0 {
		c.API.Referrer.PageCacheExpire = referrerCacheExpireDefault
	}
	if c.API.Referrer.PageCacheLimit == 0 {
		c.API.Referrer.PageCacheLimit = referrerCacheLimitDefault
	}
	if c.API.Referrer.Limit == 0 {
		c.API.Referrer.Limit = referrersLimitDefault
	}

	c.Storage.ReadOnly = boolDefault(c.Storage.ReadOnly, false)
	switch c.Storage.StoreType {
	case StoreDir:
		if c.Storage.RootDir == "" {
			c.Storage.RootDir = "."
		}
	}
	if c.Storage.GC.Frequency == 0 {
		c.Storage.GC.Frequency = time.Minute * 15
	}
	if c.Storage.GC.GracePeriod == 0 {
		c.Storage.GC.GracePeriod = time.Hour
	}
	if c.Storage.GC.RepoUploadMax == 0 {
		c.Storage.GC.RepoUploadMax = repoUploadMaxDefault
	}
	c.Storage.GC.Untagged = boolDefault(c.Storage.GC.Untagged, false)
	c.Storage.GC.EmptyRepo = boolDefault(c.Storage.GC.EmptyRepo, true)
	c.Storage.GC.ReferrersDangling = boolDefault(c.Storage.GC.ReferrersDangling, false)
	c.Storage.GC.ReferrersWithSubj = boolDefault(c.Storage.GC.ReferrersWithSubj, true)
}

func boolDefault(cur *bool, def bool) *bool {
	if cur != nil {
		return cur
	}
	return &def
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
