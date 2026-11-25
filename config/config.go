// Copyright the olareg contributors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package config contains data types for configuration of olareg.
package config

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/olareg/olareg/internal/sloghandle"
)

const (
	authDescription      = "olareg registry"
	cacheExpireDefault   = time.Minute * 5
	cacheLimitDefault    = 1000
	manifestLimitDefault = 1024 * 1024 * 8
	responseLimitDefault = 1024 * 1024 * 4
	repoUploadMaxDefault = 1000
	tagHistoryMinDefault = 50
	tagHistoryAgeDefault = time.Hour * 24 * 30
)

type Store = string // preserving for backwards compatibility

const (
	StoreUndef = ""    // undefined backend storage is the invalid zero value
	StoreMem   = "mem" // StoreMem only uses memory for an ephemeral registry
	StoreDir   = "dir" // StoreDir tracks each repository with a separate blob store
)

type Config struct {
	HTTP    ConfigHTTP
	Storage ConfigStorage
	API     ConfigAPI
	Log     *slog.Logger
	Auth    ConfigAuth
	// TODO: proxy settings, pull only, or push+pull cache
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
	TagHistory    ConfigAPITagHistory
	Warnings      []string
	RateLimit     int // number of requests per second from a given IP address
}

type ConfigAPIManifest struct {
	Limit       int64 // max size of a manifest, default is 8MB (manifestLimitDefault)
	SparseImage *bool // allow sparse images (missing layers/config)
	SparseIndex *bool // allow spare index (missing platforms)
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

type ConfigAPITagHistory struct {
	Enabled         *bool         // enable the tag history API, enabled by default
	PruneAge        time.Duration // time after which history entries exceeding PruneMin may be pruned
	PruneMin        int           // minimum number of history entries to retain
	PageCacheExpire time.Duration // time to save pages for a paged response
	PageCacheLimit  int           // max number of paged responses to keep in memory
	Limit           int64         // max size of a tag history response (OCI recommends 4MiB)
}

type ConfigAuth struct {
	Handler AuthHandler  // Handler is used to create a new [http.Handler] to authenticate request
	Token   http.Handler // Token is a [http.Handler] that receives requests to the /token route
}

func (c *Config) SetDefaults() {
	c.API.DeleteEnabled = boolDefault(c.API.DeleteEnabled, false)
	c.API.PushEnabled = boolDefault(c.API.PushEnabled, true)
	c.API.Blob.DeleteEnabled = boolDefault(c.API.Blob.DeleteEnabled, false)
	c.API.Referrer.Enabled = boolDefault(c.API.Referrer.Enabled, true)
	if c.API.Manifest.Limit <= 0 {
		c.API.Manifest.Limit = manifestLimitDefault
	}
	c.API.Manifest.SparseImage = boolDefault(c.API.Manifest.SparseImage, false)
	c.API.Manifest.SparseIndex = boolDefault(c.API.Manifest.SparseIndex, false)
	if c.API.Referrer.PageCacheExpire == 0 {
		c.API.Referrer.PageCacheExpire = cacheExpireDefault
	}
	if c.API.Referrer.PageCacheLimit == 0 {
		c.API.Referrer.PageCacheLimit = cacheLimitDefault
	}
	if c.API.Referrer.Limit == 0 {
		c.API.Referrer.Limit = responseLimitDefault
	}
	c.API.TagHistory.Enabled = boolDefault(c.API.TagHistory.Enabled, true)
	if c.API.TagHistory.PruneMin == 0 {
		c.API.TagHistory.PruneMin = tagHistoryMinDefault
	}
	if c.API.TagHistory.PruneAge == 0 {
		c.API.TagHistory.PruneAge = tagHistoryAgeDefault
	}
	if c.API.TagHistory.PageCacheExpire == 0 {
		c.API.TagHistory.PageCacheExpire = cacheExpireDefault
	}
	if c.API.TagHistory.PageCacheLimit == 0 {
		c.API.TagHistory.PageCacheLimit = cacheLimitDefault
	}
	if c.API.TagHistory.Limit == 0 {
		c.API.TagHistory.Limit = responseLimitDefault
	}
	if c.Log == nil {
		c.Log = slog.New(sloghandle.Discard)
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
