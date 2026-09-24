package session

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubDriver struct{ info Info }

func (d *stubDriver) Info() Info                 { return d.info }
func (d *stubDriver) Stop(context.Context) error { return nil }
func (d *stubDriver) DisconnectLink(string)      {}
func stub(id string, created time.Time) *stubDriver {
	return &stubDriver{Info{ID: id, CreatedAt: created, Status: StatusRunning}}
}

func TestRegistryCapacityListAndGC(t *testing.T) {
	r := NewRegistry(2)
	now := time.Now()
	a, b := stub("a", now.Add(-2*time.Minute)), stub("b", now.Add(-time.Minute))
	if err := r.Add(a); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(b); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(stub("c", now)); !errors.Is(err, ErrTooManySessions) {
		t.Fatalf("expected capacity error, got %v", err)
	}
	if list := r.List(); list[0].ID != "b" || list[1].ID != "a" {
		t.Fatalf("order %v", list)
	}
	ended := now.Add(-20 * time.Minute)
	a.info.Status = StatusExited
	a.info.EndedAt = &ended
	var removed []string
	r.OnRemove = func(id string) { removed = append(removed, id) }
	if got := r.GC(10*time.Minute, now); len(got) != 1 || got[0] != "a" || len(removed) != 1 {
		t.Fatalf("gc %v removed %v", got, removed)
	}
	if err := r.Add(stub("c", now)); err != nil {
		t.Fatalf("capacity after gc: %v", err)
	}
	if _, ok := r.Get("a"); ok {
		t.Fatal("a should be gone")
	}
}
