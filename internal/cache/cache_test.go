//go:build go1.18
// +build go1.18

package cache

import (
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
	pruned := map[string]bool{}
	pruneFn := func(_ int, v string) {
		pruned[v] = true
	}
	timeout := time.Millisecond * 250
	timeoutHalf := timeout / 2
	timePrune := timeout + (timeout / 10)
	c := New[int, string](WithAge[int, string](timeout), WithCount[int, string](countMax), WithPrune[int, string](pruneFn))
	// add entries beyond limit
	for i, v := range testData {
		c.Set(i, v)
	}
	// delete last 2 entries
	for i := len(testData) - countDel; i < len(testData); i++ {
		c.Delete(i)
	}
	if c.IsEmpty() {
		t.Errorf("cache is empty after adding entries")
	}
	// get entries, verify some deleted
	pruneCutoff := len(testData) - countMax
	saveCutoff := len(testData) - countMin
	checkCutoff := len(testData) - (countMax / 2)
	for i, v := range testData {
		getVal, err := c.Get(i)
		if i < pruneCutoff || i >= len(testData)-countDel {
			if !pruned[v] {
				t.Errorf("value not found on pruned map: %s", v)
			}
			if err == nil {
				t.Errorf("value found that should have been pruned: %d", i)
			}
		} else if i >= saveCutoff && i < len(testData)-countDel {
			if pruned[v] {
				t.Errorf("value found on pruned map: %s", v)
			}
			if err != nil {
				t.Errorf("value not found: %d", i)
			} else if getVal != v {
				t.Errorf("value mismatch: %d, expect %s, received %s", i, v, getVal)
			}
		}
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
				t.Errorf("value not found: %d", i)
			} else if getVal != v {
				t.Errorf("value mismatch: %d, expect %s, received %s", i, v, getVal)
			}
		}
	}
	// delay to after maxAge from 1st used time
	time.Sleep(timePrune - time.Since(start))
	for i, v := range testData {
		getVal, err := c.Get(i)
		if i >= saveCutoff && i < checkCutoff {
			if err != nil {
				t.Errorf("value not found: %d", i)
			} else if getVal != v {
				t.Errorf("value mismatch: %d, expect %s, received %s", i, v, getVal)
			}
		} else {
			if err == nil {
				t.Errorf("value not pruned: %d", i)
			}
		}
	}
	// delay to after maxAge for all entries
	time.Sleep(timePrune + timePrune/5)
	for i := range testData {
		_, err := c.Get(i)
		if err == nil {
			t.Errorf("value not pruned: %d", i)
		}
	}
	if !c.IsEmpty() {
		t.Errorf("cache is not empty after prune time")
	}
	// set and delete a key
	c.Set(42, "x")
	c.Delete(42)
	// delete non-existent key
	c.Delete(42)

	// set and get a struct based key
	c2 := New[testKey, string](WithAge[testKey, string](timeout), WithCount[testKey, string](countMax))
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
	if !c.IsEmpty() {
		t.Errorf("cache is not empty after deleting entries")
	}
}
