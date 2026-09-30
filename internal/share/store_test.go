package share

import (
	"errors"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/session"
)

func TestCreateResolveRevoke(t *testing.T) {
	s := NewStore()
	var revoked []string
	s.OnRevoke = func(sessionID, linkID string) { revoked = append(revoked, sessionID+"/"+linkID) }
	link, tok, err := s.Create("sess", session.RoleView, "qa", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) != 43 {
		t.Fatalf("token length %d", len(tok))
	}
	got, err := s.Resolve(tok)
	if err != nil || got.ID != link.ID || got.Role != session.RoleView {
		t.Fatalf("resolve: %+v %v", got, err)
	}
	if _, err := s.Resolve("nope"); !errors.Is(err, ErrUnknownToken) {
		t.Fatalf("unknown: %v", err)
	}
	if !s.Revoke("sess", link.ID) {
		t.Fatal("revoke failed")
	}
	if s.Revoke("other", link.ID) {
		t.Fatal("revoke must check session ownership")
	}
	if _, err := s.Resolve(tok); !errors.Is(err, ErrRevoked) {
		t.Fatalf("after revoke: %v", err)
	}
	if len(revoked) != 1 || revoked[0] != "sess/"+link.ID {
		t.Fatalf("hook %v", revoked)
	}
	s.Revoke("sess", link.ID)
	if len(revoked) != 1 {
		t.Fatal("hook must fire once")
	}
	if list := s.ListBySession("sess"); len(list) != 1 || !list[0].Revoked {
		t.Fatalf("list %+v", list)
	}
}

func TestExpiry(t *testing.T) {
	s := NewStore()
	now := time.Now()
	s.now = func() time.Time { return now }
	_, tok, _ := s.Create("sess", session.RoleControl, "", time.Minute)
	if _, err := s.Resolve(tok); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := s.Resolve(tok); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired: %v", err)
	}
}

func TestDeleteSessionAndRole(t *testing.T) {
	s := NewStore()
	if _, _, err := s.Create("sess", "admin", "", 0); !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("role: %v", err)
	}
	_, tok, _ := s.Create("sess", session.RoleView, "", 0)
	s.DeleteSession("sess")
	if _, err := s.Resolve(tok); !errors.Is(err, ErrUnknownToken) {
		t.Fatalf("after delete: %v", err)
	}
}

func TestEqualConstantTime(t *testing.T) {
	if !Equal("abc", "abc") || Equal("abc", "abd") || Equal("", "x") {
		t.Fatal("Equal misbehaves")
	}
}

// A run link belongs to its run alone: listed, revoked and capped by the run,
// never by a session, with its own revoke hook.
func TestRunLinks(t *testing.T) {
	s := NewStore()
	var sessionRevokes, runRevokes []string
	s.OnRevoke = func(sessionID, linkID string) { sessionRevokes = append(sessionRevokes, sessionID+"/"+linkID) }
	s.OnRevokeRun = func(runID, linkID string) { runRevokes = append(runRevokes, runID+"/"+linkID) }
	if _, _, err := s.CreateRunLink("run", "admin", "", 0); !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("role: %v", err)
	}
	link, tok, err := s.CreateRunLink("run", session.RoleView, "standup", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if link.RunID != "run" || link.SessionID != "" || link.Label != "standup" || link.ExpiresAt == nil {
		t.Fatalf("link %+v", link)
	}
	got, err := s.Resolve(tok)
	if err != nil || got.ID != link.ID || got.RunID != "run" || got.SessionID != "" {
		t.Fatalf("resolve: %+v %v", got, err)
	}
	if l := s.ListByRun("run"); len(l) != 1 || l[0].ID != link.ID {
		t.Fatalf("list by run %+v", l)
	}
	if l := s.ListBySession(""); len(l) != 0 {
		t.Fatalf("a run link is listed by session: %+v", l)
	}
	if s.Revoke("", link.ID) || s.Revoke("run", link.ID) {
		t.Fatal("a run link is revoked as a session's")
	}
	if s.RevokeRun("other", link.ID) {
		t.Fatal("revoke must check run ownership")
	}
	sl, _, _ := s.Create("sess", session.RoleView, "", 0)
	if s.RevokeRun("", sl.ID) || s.RevokeRun("sess", sl.ID) {
		t.Fatal("a session link is revoked as a run's")
	}
	if !s.RevokeRun("run", link.ID) {
		t.Fatal("revoke run failed")
	}
	if _, err := s.Resolve(tok); !errors.Is(err, ErrRevoked) {
		t.Fatalf("after revoke: %v", err)
	}
	s.RevokeRun("run", link.ID)
	if len(runRevokes) != 1 || runRevokes[0] != "run/"+link.ID || len(sessionRevokes) != 0 {
		t.Fatalf("hooks: run %v session %v", runRevokes, sessionRevokes)
	}
	if l := s.ListByRun("run"); len(l) != 1 || !l[0].Revoked {
		t.Fatalf("list after revoke %+v", l)
	}
	// Deleting a session leaves the run links alone.
	s.DeleteSession("")
	if _, ok := s.Get(link.ID); !ok {
		t.Fatal("a session's delete dropped a run link")
	}
}

// The per-scope cap applies to each run as it does to each session.
func TestRunLinksAreCappedPerRun(t *testing.T) {
	s := NewStore()
	for i := range MaxLinksPerSession {
		if _, _, err := s.CreateRunLink("run", session.RoleView, "", 0); err != nil {
			t.Fatalf("link %d: %v", i, err)
		}
	}
	if _, _, err := s.CreateRunLink("run", session.RoleView, "", 0); !errors.Is(err, ErrTooManyLinks) {
		t.Fatalf("past the cap: %v", err)
	}
	if _, _, err := s.CreateRunLink("other", session.RoleView, "", 0); err != nil {
		t.Fatalf("another run: %v", err)
	}
	if _, _, err := s.Create("run", session.RoleView, "", 0); err != nil {
		t.Fatalf("a session named like the run: %v", err)
	}
}
