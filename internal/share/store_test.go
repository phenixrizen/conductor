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
