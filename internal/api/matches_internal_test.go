package api

import (
	"testing"
	"time"
)

// M13 (the rule editor's matches box): two requests that read the counters concurrently may note
// their readings in any order; the older reading noted last must not make the counter seem to grow
// again on the next read.
func TestMatchTrackerIgnoresAReadingOlderThanTheNewest(t *testing.T) {
	var m matchTracker
	t0 := time.Unix(1000, 0)
	m.observe("r", 10, 1, t0)
	grew := m.observe("r", 12, 1, t0.Add(2*time.Second))
	if grew == nil || !grew.Equal(t0.Add(2*time.Second)) {
		t.Fatalf("the counter grew: %v", grew)
	}
	// the older reading (taken at t0+1s, 11 packets) is noted last
	if got := m.observe("r", 11, 1, t0.Add(time.Second)); got == nil || !got.Equal(*grew) {
		t.Errorf("an older reading moved the last match: %v", got)
	}
	// the next read finds the counter unchanged: no new match
	if got := m.observe("r", 12, 1, t0.Add(5*time.Second)); got == nil || !got.Equal(*grew) {
		t.Errorf("a counter that did not grow has a new last match: %v", got)
	}
	// an older reading of another epoch does not start the counter over either
	if got := m.observe("r", 0, 0, t0.Add(3*time.Second)); got == nil || !got.Equal(*grew) {
		t.Errorf("an older reading of an earlier epoch: %v", got)
	}
	if got := m.observe("r", 12, 1, t0.Add(6*time.Second)); got == nil || !got.Equal(*grew) {
		t.Errorf("after an older reading of an earlier epoch: %v", got)
	}
}

// Within an epoch a lower reading (in the same instant) keeps the highest one.
func TestMatchTrackerKeepsTheHighestReadingOfAnEpoch(t *testing.T) {
	var m matchTracker
	t0 := time.Unix(1000, 0)
	m.observe("r", 10, 1, t0)
	m.observe("r", 8, 1, t0.Add(time.Second))
	if got := m.observe("r", 10, 1, t0.Add(2*time.Second)); got != nil {
		t.Errorf("a counter back at its highest reading was seen growing: %v", got)
	}
}

// The counters of rules and overlays that are gone are forgotten; an older reading prunes nothing.
func TestMatchTrackerForgetsTheCountersThatAreGone(t *testing.T) {
	var m matchTracker
	t0 := time.Unix(1000, 0)
	m.observe("a", 1, 1, t0)
	m.observe("b", 1, 1, t0)
	m.prune(func(n string) bool { return n == "a" }, t0.Add(2*time.Second))
	if m.len() != 1 {
		t.Fatalf("the counter of a removed rule is kept: %d entries", m.len())
	}
	m.observe("c", 1, 1, t0.Add(3*time.Second))
	m.prune(func(n string) bool { return n == "a" }, t0.Add(time.Second))
	if m.len() != 2 {
		t.Errorf("an older reading pruned a newer counter: %d entries", m.len())
	}
}
