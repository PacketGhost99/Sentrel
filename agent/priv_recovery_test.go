package agent

import (
	"testing"
	"time"
)

func TestCheckRecoveryWindow(t *testing.T) {
	now := time.Now()
	if err := checkRecoveryWindow(nil, now); err == nil {
		t.Fatal("nil marker allowed rollback")
	}
	if err := checkRecoveryWindow(&updateMarker{}, now); err == nil {
		t.Fatal("empty marker allowed rollback")
	}
	if err := checkRecoveryWindow(&updateMarker{At: now.Add(-time.Minute).Unix()}, now); err != nil {
		t.Fatalf("recent marker refused: %v", err)
	}
	if err := checkRecoveryWindow(&updateMarker{At: now.Add(-recoveryWindow - time.Minute).Unix()}, now); err == nil {
		t.Fatal("stale marker allowed rollback")
	}
	if err := checkRecoveryWindow(&updateMarker{At: now.Add(time.Hour).Unix()}, now); err == nil {
		t.Fatal("future-dated marker allowed rollback")
	}
}
