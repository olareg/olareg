// Package cache is used to store values with limits.
// Items are automatically pruned when too many entries are stored, or values become stale.
package cache

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/olareg/olareg/types"
)

type Cache[k comparable, v any] struct {
	mu          sync.Mutex
	minAge      time.Duration
	maxAge      time.Duration
	minCount    int
	maxCount    int
	timer       *time.Timer
	entries     map[k]*Entry[v]
	pruneFn     func(k, v) error
	prunePreFn  func(k, v)
	prunePostFn func(k, v)
}

type Entry[v any] struct {
	used  time.Time
	value v
}

type sortKeys[k comparable] struct {
	keys   []k
	lessFn func(a, b k) bool
}

type Opts[k comparable, v any] struct {
	Age         time.Duration    // when entries become eligible for pruning
	Count       int              // size that triggers pruning
	PruneFn     func(k, v) error // function to run when pruning entries and manually deleting
	PrunePreFn  func(k, v)       // function to run before asynchronous pruning (e.g. mutex lock)
	PrunePostFn func(k, v)       // function to run after asynchronous pruning (e.g. mutex unlock)
}

// New returns a new cache.
func New[k comparable, v any](opt Opts[k, v]) *Cache[k, v] {
	maxAge := opt.Age + (opt.Age / 10)
	minCount := 0
	if opt.Count > 0 {
		minCount = int(float64(opt.Count) * 0.9)
	}
	return &Cache[k, v]{
		minAge:      opt.Age,
		maxAge:      maxAge,
		minCount:    minCount,
		maxCount:    opt.Count,
		pruneFn:     opt.PruneFn,
		prunePreFn:  opt.PrunePreFn,
		prunePostFn: opt.PrunePostFn,
		entries:     map[k]*Entry[v]{},
	}
}

// Delete removes an entry from the cache.
func (c *Cache[k, v]) Delete(key k) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pruneFn != nil && c.entries[key] != nil {
		v := c.entries[key].value
		c.mu.Unlock()
		err := c.pruneFn(key, v)
		c.mu.Lock()
		if err != nil {
			return err
		}
	}
	delete(c.entries, key)
	if len(c.entries) == 0 && c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	return nil
}

// DeleteAll removes all entries in the cache.
func (c *Cache[k, v]) DeleteAll() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	errs := make([]error, 0, len(c.entries))
	for key := range c.entries {
		if c.pruneFn != nil {
			v := c.entries[key].value
			c.mu.Unlock()
			err := c.pruneFn(key, v)
			c.mu.Lock()
			if err != nil {
				errs = append(errs, err)
				continue
			}
		}
		delete(c.entries, key)
	}
	if len(c.entries) == 0 && c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
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
		c.entries[key].used = time.Now()
		return e.value, nil
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
	if c.timer == nil && c.maxAge > 0 {
		// prune resets the timer, so this is only needed if the prune wasn't triggered
		c.timer = time.AfterFunc(c.maxAge, c.pruneAge)
	}
	if c.maxCount > 0 && len(c.entries) > c.maxCount {
		go c.pruneCount()
	}
}

// pruneAge deletes entries over the age limit.
func (c *Cache[k, v]) pruneAge() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.minAge <= 0 {
		return
	}
	now := time.Now()
	cutoff := now.Add(c.minAge * -1)
	oldest := now
	for key := range c.entries {
		if c.entries[key].used.Before(cutoff) {
			if c.pruneFn != nil {
				if c.prunePreFn != nil {
					c.prunePreFn(key, c.entries[key].value)
				}
				err := c.pruneFn(key, c.entries[key].value)
				if c.prunePostFn != nil {
					c.prunePostFn(key, c.entries[key].value)
				}
				if err != nil {
					c.entries[key].used = now
					continue
				}
			}
			delete(c.entries, key)
		} else if c.entries[key].used.Before(oldest) {
			oldest = c.entries[key].used
		}
	}
	// set/clear next timer
	if len(c.entries) > 0 {
		dur := c.maxAge - now.Sub(oldest)
		if dur <= 0 {
			// this shouldn't be possible
			dur = time.Millisecond
		}
		if c.timer == nil {
			// this shouldn't be possible
			c.timer = time.AfterFunc(dur, c.pruneAge)
		} else {
			c.timer.Reset(dur)
		}
	} else if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
}

// pruneCount deletes entries beyond the count limit.
func (c *Cache[k, v]) pruneCount() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.minCount <= 0 || len(c.entries) <= c.minCount {
		return
	}
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
	delLen := len(keyList) - c.minCount
	delCount := 0
	for _, key := range keyList {
		if c.pruneFn != nil {
			if c.prunePreFn != nil {
				c.prunePreFn(key, c.entries[key].value)
			}
			err := c.pruneFn(key, c.entries[key].value)
			if c.prunePostFn != nil {
				c.prunePostFn(key, c.entries[key].value)
			}
			if err != nil {
				c.entries[key].used = time.Now()
				continue
			}
		}
		delete(c.entries, key)
		delCount++
		if delCount >= delLen {
			break
		}
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
