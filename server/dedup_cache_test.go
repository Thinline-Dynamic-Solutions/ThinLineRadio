package main

import (
	"testing"
	"time"
)

func TestCheckAndMarkReceivedAtDoesNotSlideWindow(t *testing.T) {
	dc := NewDedupCache(30000)
	defer dc.Stop()

	if dc.CheckAndMarkReceivedAt(1, 2, 2.0) {
		t.Fatal("first call should not be a duplicate")
	}

	dc.mutex.Lock()
	dc.entries["ra:1:2"].SeenAt = time.Now().Add(-900 * time.Millisecond)
	dc.mutex.Unlock()

	if !dc.CheckAndMarkReceivedAt(1, 2, 2.0) {
		t.Fatal("second similar call within 1s should be a duplicate")
	}

	dc.mutex.Lock()
	dc.entries["ra:1:2"].SeenAt = time.Now().Add(-1100 * time.Millisecond)
	dc.mutex.Unlock()

	if dc.CheckAndMarkReceivedAt(1, 2, 2.0) {
		t.Fatal("call after the 1s window should not stay blocked by a sliding SeenAt")
	}
}

func TestCheckAndMarkReceivedAtRequiresSimilarDuration(t *testing.T) {
	dc := NewDedupCache(30000)
	defer dc.Stop()

	if dc.CheckAndMarkReceivedAt(1, 2, 0.5) {
		t.Fatal("first call should not be a duplicate")
	}
	if dc.CheckAndMarkReceivedAt(1, 2, 8.0) {
		t.Fatal("different-length call on a busy talkgroup should not be treated as a duplicate")
	}
}
