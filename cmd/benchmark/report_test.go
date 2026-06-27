package main

import "testing"

func TestMinMaxInt64Empty(t *testing.T) {
	if got := minInt64(nil); got != 0 {
		t.Errorf("minInt64(nil) = %d, want 0", got)
	}
	if got := maxInt64(nil); got != 0 {
		t.Errorf("maxInt64(nil) = %d, want 0", got)
	}
}
