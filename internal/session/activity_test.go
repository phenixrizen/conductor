package session

import (
	"fmt"
	"testing"
)

func TestActivityRingKeepsNewestOldestFirst(t *testing.T) {
	var r activityRing
	for i := 0; i < MaxActivity+25; i++ {
		r.Add(ActivityEntry{Type: "link", Message: fmt.Sprintf("m%d", i)})
	}
	snap := r.Snapshot()
	if len(snap) != MaxActivity {
		t.Fatalf("len %d, want %d", len(snap), MaxActivity)
	}
	if snap[0].Message != "m25" || snap[len(snap)-1].Message != fmt.Sprintf("m%d", MaxActivity+24) {
		t.Fatalf("order: first %q last %q", snap[0].Message, snap[len(snap)-1].Message)
	}
	if r.Add(ActivityEntry{Type: "link"}); r.Snapshot()[0].Message != "m26" {
		t.Fatal("ring did not drop the oldest entry")
	}
}

func TestActivityRingStampsTime(t *testing.T) {
	var r activityRing
	r.Add(ActivityEntry{Type: "join", ByName: "Priya"})
	if r.Snapshot()[0].At.IsZero() {
		t.Fatal("At must be stamped when missing")
	}
}
