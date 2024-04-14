// Package cache is used to store values with limits.
// Items are automatically pruned when too many entries are stored, or values become stale.
package cache

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/olareg/olareg/types"
)

type Cache[k comparable, v any] struct {
	mu       sync.Mutex
	minAge   time.Duration
	maxAge   time.Duration
	minCount int
	maxCount int
	timer    *time.Timer
	entries  map[k]*Entry[v]
	pruneFn  func(k, v)
}

type Entry[v any] struct {
	used  time.Time
	value v
}

type sortKeys[k comparable] struct {
	keys   []k
	lessFn func(a, b k) bool
}

type conf[k comparable, v any] struct {
	minAge   time.Duration
	maxCount int
	pruneFn  func(k, v)
}

type CacheOpts[k comparable, v any] func(*conf[k, v])

func WithAge[k comparable, v any](age time.Duration) CacheOpts[k, v] {
	return func(c *conf[k, v]) {
		c.minAge = age
	}
}

func WithCount[k comparable, v any](count int) CacheOpts[k, v] {
	return func(c *conf[k, v]) {
		c.maxCount = count
	}
}

func WithPrune[k comparable, v any](fn func(k, v)) CacheOpts[k, v] {
	return func(c *conf[k, v]) {
		c.pruneFn = fn
	}
}

// New returns a new cache.
func New[k comparable, v any](opts ...CacheOpts[k, v]) *Cache[k, v] {
	c := conf[k, v]{}
	for _, opt := range opts {
		opt(&c)
	}
	maxAge := c.minAge + (c.minAge / 10)
	minCount := 0
	if c.maxCount > 0 {
		minCount = int(float64(c.maxCount) * 0.9)
	}
	return &Cache[k, v]{
		minAge:   c.minAge,
		maxAge:   maxAge,
		minCount: minCount,
		maxCount: c.maxCount,
		pruneFn:  c.pruneFn,
		entries:  map[k]*Entry[v]{},
	}
}

// Delete removes an entry from the cache.
func (c *Cache[k, v]) Delete(key k) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pruneFn != nil && c.entries[key] != nil {
		v := c.entries[key].value
		c.mu.Unlock()
		c.pruneFn(key, v)
		c.mu.Lock()
	}
	delete(c.entries, key)
	if len(c.entries) == 0 && c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
}

// DeleteAll removes all entries in the cache.
func (c *Cache[k, v]) DeleteAll() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.entries {
		if c.pruneFn != nil {
			v := c.entries[key].value
			c.mu.Unlock()
			c.pruneFn(key, v)
			c.mu.Lock()
		}
		delete(c.entries, key)
	}
	if len(c.entries) == 0 && c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
}

// Get retrieves an entry from the cache.
func (c *Cache[k, v]) Get(key k) (v, error) {
	if c == nil {
		var val v
		return val, types.ErrNotFound
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[key]; ok {
		if c.minAge > 0 && e.used.Add(c.minAge).Before(time.Now()) {
			// entry expired
			go c.prune()
		} else {
			c.entries[key].used = time.Now()
			return e.value, nil
		}
	}
	var val v
	return val, types.ErrNotFound
}

// IsEmpty returns true if the cache is empty.
func (c *Cache[k, v]) IsEmpty() bool {
	if c == nil {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries) == 0
}

// List returns a list of keys in the cache.
func (c *Cache[k, v]) List() ([]k, error) {
	if c == nil {
		return []k{}, fmt.Errorf("cache is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	keys := make([]k, 0, len(c.entries))
	for key := range c.entries {
		keys = append(keys, key)
	}
	return keys, nil
}

// Set adds an entry to the cache.
func (c *Cache[k, v]) Set(key k, val v) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = &Entry[v]{
		used:  time.Now(),
		value: val,
	}
	if c.maxCount > 0 && len(c.entries) > c.maxCount {
		c.pruneLocked()
	} else if c.timer == nil && c.maxAge > 0 {
		// prune resets the timer, so this is only needed if the prune wasn't triggered
		c.timer = time.AfterFunc(c.maxAge, c.prune)
	}
}

func (c *Cache[k, v]) prune() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked()
}

func (c *Cache[k, v]) pruneLocked() {
	// sort key list by last used date
	keyList := make([]k, 0, len(c.entries))
	for key := range c.entries {
		keyList = append(keyList, key)
	}
	sk := sortKeys[k]{
		keys: keyList,
		lessFn: func(a, b k) bool {
			return c.entries[a].used.Before(c.entries[b].used)
		},
	}
	sort.Sort(&sk)
	// prune entries
	now := time.Now()
	cutoff := now.Add(c.minAge * -1)
	nextTime := now
	delCount := 0
	if c.minCount > 0 {
		delCount = len(keyList) - c.minCount
	}
	for i, key := range keyList {
		if i < delCount || (c.minAge > 0 && c.entries[key].used.Before(cutoff)) {
			if c.pruneFn != nil {
				c.pruneFn(key, c.entries[key].value)
			}
			delete(c.entries, key)
		} else {
			nextTime = c.entries[key].used
			break
		}
	}
	// set next timer
	if len(c.entries) > 0 && c.maxAge > 0 {
		dur := nextTime.Sub(now) + c.maxAge
		if c.timer == nil {
			// this shouldn't be possible
			c.timer = time.AfterFunc(dur, c.prune)
		} else {
			c.timer.Reset(dur)
		}
	} else if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
}

func (sk *sortKeys[k]) Len() int {
	return len(sk.keys)
}

func (sk *sortKeys[k]) Less(i, j int) bool {
	return sk.lessFn(sk.keys[i], sk.keys[j])
}

func (sk *sortKeys[k]) Swap(i, j int) {
	sk.keys[i], sk.keys[j] = sk.keys[j], sk.keys[i]
}
