//go:build go1.18
// +build go1.18

package cache

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type testKey struct {
	i int
	s string
}

func TestCache(t *testing.T) {
	t.Parallel()
	testData := []string{
		"a", "b", "c", "d", "e", "f", "g", "h", "i", "j",
		"k", "l", "m", "n", "o", "p", "q", "r", "s", "t",
	}
	countMax := 10
	countMin := int(float64(countMax) * 0.9)
	countDel := 2
	countSave := 1
	pruneCutoff := len(testData) + countSave - countMax
	saveCutoff := len(testData) + countSave - countMin
	checkCutoff := len(testData) - (countMax / 2)
	saveKey := "a"
	pruned := map[string]bool{}
	var prunedMu sync.Mutex
	errBlocked := fmt.Errorf("prune blocked")
	pruneFn := func(_ int, v string) error {
		if v == saveKey {
			return errBlocked
		}
		prunedMu.Lock()
		pruned[v] = true
		prunedMu.Unlock()
		return nil
	}
	timeout := time.Millisecond * 250
	timeoutHalf := timeout / 2
	timePrune := timeout + (timeout / 5)
	c := New[int, string](Opts[int, string]{
		Age:     timeout,
		Count:   countMax,
		PruneFn: pruneFn,
	})
	// add entries beyond limit
	for i, v := range testData {
		c.Set(i, v)
	}
	// short delay to ensure prune has run
	prunedMu.Lock()
	if len(pruned) < pruneCutoff {
		prunedMu.Unlock()
		time.Sleep(timeoutHalf)
		prunedMu.Lock()
	}
	prunedMu.Unlock()
	// delete last 2 entries
	for i := len(testData) - countDel; i < len(testData); i++ {
		c.Delete(i)
	}
	if c.IsEmpty() {
		t.Errorf("cache is empty after adding entries")
	}
	// get entries, verify some deleted
	for i, v := range testData {
		getVal, err := c.Get(i)
		prunedMu.Lock()
		if v == saveKey || (i >= saveCutoff && i < len(testData)-countDel) {
			if pruned[v] {
				t.Errorf("value found on pruned map: %s", v)
			}
			if err != nil {
				t.Errorf("value not found: %s", v)
			} else if getVal != v {
				t.Errorf("value mismatch: %d, expect %s, received %s", i, v, getVal)
			}
		} else if i < pruneCutoff || i >= len(testData)-countDel {
			if !pruned[v] {
				t.Errorf("value not found on pruned map: %s", v)
			}
			if err == nil {
				t.Errorf("value found that should have been pruned: %s", v)
			}
		}
		prunedMu.Unlock()
	}
	entries, err := c.List()
	if err != nil {
		t.Errorf("failed listing entries: %v", err)
	} else if len(entries) < countMin-countDel || len(entries) > countMax {
		t.Errorf("unexpected length of entries: %d not between %d and %d", len(entries), countMin-countDel, countMax)
	}
	// track used time
	start := time.Now()
	// delay to before minAge and check some entries
	time.Sleep(timeoutHalf)
	for i, v := range testData {
		if i >= saveCutoff && i < checkCutoff {
			getVal, err := c.Get(i)
			if err != nil {
				t.Errorf("value not found: %s", v)
			} else if getVal != v {
				t.Errorf("value mismatch: %d, expect %s, received %s", i, v, getVal)
			}
		}
	}
	// delay to after maxAge from 1st used time
	time.Sleep(timePrune - time.Since(start))
	for i, v := range testData {
		getVal, err := c.Get(i)
		if v == saveKey || (i >= saveCutoff && i < checkCutoff) {
			if err != nil {
				t.Errorf("value not found: [%d]%s", i, v)
			} else if getVal != v {
				t.Errorf("value mismatch: %d, expect %s, received %s", i, v, getVal)
			}
		} else {
			if err == nil {
				t.Errorf("value not pruned: [%d]%s", i, v)
			}
		}
	}
	// delay to after maxAge for all entries
	time.Sleep(timePrune + timePrune/5)
	for i, v := range testData {
		_, err := c.Get(i)
		if v == saveKey {
			if err != nil {
				t.Errorf("value was pruned: [%d]%s", i, v)
			}
		} else if err == nil {
			t.Errorf("value not pruned: [%d]%s", i, v)
		}
	}
	if c.IsEmpty() {
		t.Errorf("cache is empty when \"a\" should not be pruned")
	}
	// set and delete a key
	c.Set(42, "x")
	c.Delete(42)
	// delete non-existent key
	c.Delete(42)
	// delete all entries with a prune function
	err = c.DeleteAll()
	if !errors.Is(err, errBlocked) {
		t.Errorf("DeleteAll: expected %v, received %v", errBlocked, err)
	}

	// set and get a struct based key
	c2 := New[testKey, string](Opts[testKey, string]{
		Age:   timeout,
		Count: countMax,
	})
	c2.Set(testKey{i: 42, s: "test"}, "value")
	v, err := c2.Get(testKey{i: 42, s: "test"})
	if err != nil {
		t.Errorf("failed to get key: %v", err)
	}
	if v != "value" {
		t.Errorf("value mismatch, expected value, received %s", v)
	}
	if c2.IsEmpty() {
		t.Errorf("cache is empty before deleting entries")
	}
	c2.DeleteAll()
	if len(c2.entries) > 0 {
		t.Errorf("entries remain after DeleteAll")
	}
	if !c2.IsEmpty() {
		t.Errorf("cache is not empty after deleting entries")
	}
}
