package main

import "testing"

func TestPagerAlertDedup_TryClaim(t *testing.T) {
	d := NewPagerAlertDedup()

	if !d.TryClaim(1, 100) {
		t.Fatal("first claim should succeed")
	}
	if d.TryClaim(1, 100) {
		t.Fatal("second claim for same user+call should fail")
	}
	if !d.TryClaim(1, 101) {
		t.Fatal("different call should succeed")
	}
	if !d.TryClaim(2, 100) {
		t.Fatal("different user should succeed")
	}
}
