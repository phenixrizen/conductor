package share

import (
	"errors"
	"slices"
	"sync"
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
	if !found(s.Revoke("sess", link.ID)) {
		t.Fatal("revoke failed")
	}
	if found(s.Revoke("other", link.ID)) {
		t.Fatal("revoke must check session ownership")
	}
	if _, err := s.Resolve(tok); !errors.Is(err, ErrRevoked) {
		t.Fatalf("after revoke: %v", err)
	}
	if len(revoked) != 1 || revoked[0] != "sess/"+link.ID {
		t.Fatalf("hook %v", revoked)
	}
	// A second revoke tells the hook again: whoever is still attached
	// through the link is closed then too.
	s.Revoke("sess", link.ID)
	if len(revoked) != 2 || revoked[1] != "sess/"+link.ID {
		t.Fatalf("hook after a second revoke %v", revoked)
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
	s.DeleteSession("sess", true)
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
	if found(s.Revoke("", link.ID)) || found(s.Revoke("run", link.ID)) {
		t.Fatal("a run link is revoked as a session's")
	}
	if found(s.RevokeRun("other", link.ID)) {
		t.Fatal("revoke must check run ownership")
	}
	sl, _, _ := s.Create("sess", session.RoleView, "", 0)
	if found(s.RevokeRun("", sl.ID)) || found(s.RevokeRun("sess", sl.ID)) {
		t.Fatal("a session link is revoked as a run's")
	}
	if !found(s.RevokeRun("run", link.ID)) {
		t.Fatal("revoke run failed")
	}
	if _, err := s.Resolve(tok); !errors.Is(err, ErrRevoked) {
		t.Fatalf("after revoke: %v", err)
	}
	s.RevokeRun("run", link.ID)
	if len(runRevokes) != 2 || runRevokes[0] != "run/"+link.ID || runRevokes[1] != "run/"+link.ID || len(sessionRevokes) != 0 {
		t.Fatalf("hooks: run %v session %v", runRevokes, sessionRevokes)
	}
	if l := s.ListByRun("run"); len(l) != 1 || !l[0].Revoked {
		t.Fatalf("list after revoke %+v", l)
	}
	// Deleting a session leaves the run links alone.
	s.DeleteSession("", true)
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

// DeleteRun forgets every link of a run and returns the IDs of those not
// revoked, whose viewers may still be attached; other runs' links and
// sessions' links stay.
func TestDeleteRun(t *testing.T) {
	s := NewStore()
	live, liveTok, _ := s.CreateRunLink("run", session.RoleView, "", 0)
	revoked, revokedTok, _ := s.CreateRunLink("run", session.RoleControl, "", 0)
	s.RevokeRun("run", revoked.ID)
	other, otherTok, _ := s.CreateRunLink("other", session.RoleView, "", 0)
	sl, sessTok, _ := s.Create("run", session.RoleView, "", 0)

	if ids := s.DeleteRun("run"); !slices.Equal(ids, []string{live.ID}) {
		t.Fatalf("deleted %v, want [%s]", ids, live.ID)
	}
	for _, tok := range []string{liveTok, revokedTok} {
		if _, err := s.Resolve(tok); !errors.Is(err, ErrUnknownToken) {
			t.Fatalf("after delete: %v", err)
		}
	}
	if _, ok := s.Get(live.ID); ok {
		t.Fatal("the link is still found by ID")
	}
	if l := s.ListByRun("run"); len(l) != 0 {
		t.Fatalf("still listed %+v", l)
	}
	if got, err := s.Resolve(otherTok); err != nil || got.ID != other.ID {
		t.Fatalf("another run's link: %+v %v", got, err)
	}
	if got, err := s.Resolve(sessTok); err != nil || got.ID != sl.ID {
		t.Fatalf("a session's link: %+v %v", got, err)
	}
	if ids := s.DeleteRun("run"); len(ids) != 0 {
		t.Fatalf("deleted again %v", ids)
	}
}

// Resolve reads a link under the store's lock, so it may run while the link
// is revoked (go test -race).
func TestResolveWhileRevoking(t *testing.T) {
	s := NewStore()
	link, tok, _ := s.CreateRunLink("run", session.RoleView, "", 0)
	sl, sessTok, _ := s.Create("sess", session.RoleView, "", 0)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 200 {
				_, _ = s.Resolve(tok)
				_, _ = s.Resolve(sessTok)
			}
		})
	}
	wg.Go(func() {
		for range 200 {
			s.RevokeRun("run", link.ID)
			s.Revoke("sess", sl.ID)
		}
	})
	wg.Wait()
	if _, err := s.Resolve(tok); !errors.Is(err, ErrRevoked) {
		t.Fatalf("after revoke: %v", err)
	}
}

// found is the first result of a revoke: whether the link was the caller's.
func found(ok, _ bool) bool { return ok }

// A revoke says whether it revoked the link: a second one finds it revoked
// already (and tells the hook again, TestCreateResolveRevoke).
func TestRevokeSaysWhetherItRevoked(t *testing.T) {
	s := NewStore()
	link, _, _ := s.CreateRunLink("run", session.RoleView, "", 0)
	if ok, revoked := s.RevokeRun("run", link.ID); !ok || !revoked {
		t.Fatalf("first: %v %v", ok, revoked)
	}
	if ok, revoked := s.RevokeRun("run", link.ID); !ok || revoked {
		t.Fatalf("second: %v %v", ok, revoked)
	}
	sl, _, _ := s.Create("sess", session.RoleView, "", 0)
	if ok, revoked := s.Revoke("sess", sl.ID); !ok || !revoked {
		t.Fatalf("session first: %v %v", ok, revoked)
	}
	if ok, revoked := s.Revoke("sess", sl.ID); !ok || revoked {
		t.Fatalf("session second: %v %v", ok, revoked)
	}
}

// A switchyard's group links: indexed by owner and run, kept current by
// their owner alone, revoked by it alone, and found by the sessions they name.
func TestGroupLinks(t *testing.T) {
	s := NewStore()
	var revokedRun string
	s.OnRevokeRun = func(runID, linkID string) { revokedRun = runID }
	g := Group{Name: "api", Members: []GroupMember{{Name: "lead", SessionID: "s1", AgentID: "claude", Status: "running"}}}
	l, tok, err := s.CreateGroupLink("api-1", "owner-a", g, session.RoleView, "", 0)
	if err != nil || l.Group == nil || !l.Group.Names("s1") || l.Owner != "owner-a" {
		t.Fatalf("create: %+v %v", l, err)
	}
	if got, err := s.Resolve(tok); err != nil || got.RunID != "api-1" || !got.Group.Names("s1") {
		t.Fatalf("resolve: %+v %v", got, err)
	}
	g.Members = append(g.Members, GroupMember{Name: "core", SessionID: "s2", AgentID: "claude", Status: "running"})
	if changed, _ := s.SetGroup("owner-b", "api-1", g); len(changed) != 0 {
		t.Fatalf("another owner changed %v", changed)
	}
	if changed, left := s.SetGroup("owner-a", "api-1", g); len(changed) != 1 || changed[0] != l.ID || len(left) != 0 {
		t.Fatalf("its owner changed %v, left %v", changed, left)
	}
	if ids := s.ListNaming("s2"); len(ids) != 1 || ids[0].ID != l.ID {
		t.Fatalf("naming s2: %v", ids)
	}
	if found, _ := s.RevokeGroup("owner-b", l.ID); found {
		t.Fatal("another owner revoked it")
	}
	if found, revoked := s.RevokeGroup("owner-a", l.ID); !found || !revoked || revokedRun != "api-1" {
		t.Fatalf("revoke: %v %v %q", found, revoked, revokedRun)
	}
	if _, err := s.Resolve(tok); err != ErrRevoked {
		t.Fatalf("after revoke: %v", err)
	}
}

// Live answers a link by id while it still opens what it was made for, and
// says why once it does not: revoked, expired, or forgotten.
func TestLive(t *testing.T) {
	s := NewStore()
	now := time.Now()
	s.now = func() time.Time { return now }
	sl, _, _ := s.Create("sess", session.RoleControl, "", time.Minute)
	rl, _, _ := s.CreateRunLink("run", session.RoleView, "", 0)
	if got, err := s.Live(sl.ID); err != nil || got.ID != sl.ID || got.Role != session.RoleControl {
		t.Fatalf("live: %+v %v", got, err)
	}
	if got, err := s.Live(rl.ID); err != nil || got.RunID != "run" {
		t.Fatalf("live run link: %+v %v", got, err)
	}
	if _, err := s.Live("nope"); !errors.Is(err, ErrUnknownLink) {
		t.Fatalf("unknown: %v", err)
	}
	now = now.Add(time.Minute)
	if _, err := s.Live(sl.ID); !errors.Is(err, ErrExpired) {
		t.Fatalf("at its expiry: %v", err)
	}
	s.RevokeRun("run", rl.ID)
	if _, err := s.Live(rl.ID); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revoked: %v", err)
	}
	s.DeleteRun("run")
	if _, err := s.Live(rl.ID); !errors.Is(err, ErrUnknownLink) {
		t.Fatalf("forgotten: %v", err)
	}
}

// Drop returns the link as it stood when it went, its group's sessions
// then, and nothing for a link it does not hold.
func TestDropReturnsTheLinkAsItWent(t *testing.T) {
	s := NewStore()
	g := Group{Name: "api", Members: []GroupMember{{Name: "lead", SessionID: "s1"}}}
	l, _, _ := s.CreateGroupLink("api-1", "owner", g, session.RoleView, "", 0)
	g.Members = append(g.Members, GroupMember{Name: "core", SessionID: "s2"})
	s.SetGroup("owner", "api-1", g)
	gone, ok := s.Drop(l.ID)
	if !ok || gone.ID != l.ID || !gone.Group.Names("s1") || !gone.Group.Names("s2") {
		t.Fatalf("dropped %+v %v", gone, ok)
	}
	if _, err := s.Live(l.ID); !errors.Is(err, ErrUnknownLink) {
		t.Fatalf("after the drop: %v", err)
	}
	if _, ok := s.Drop(l.ID); ok {
		t.Fatal("dropped twice")
	}
}

// ExpireDue tells the revoke hooks of each link that expired, once, and
// leaves alone the links not expired yet, those without an expiry and those
// revoked before.
func TestExpireDue(t *testing.T) {
	s := NewStore()
	var sessions, runs []string
	s.OnRevoke = func(sessionID, linkID string) { sessions = append(sessions, sessionID+"/"+linkID) }
	s.OnRevokeRun = func(runID, linkID string) { runs = append(runs, runID+"/"+linkID) }
	start := time.Now()
	soon, _, _ := s.Create("sess", session.RoleView, "", time.Minute)
	later, _, _ := s.Create("sess", session.RoleView, "", time.Hour)
	s.Create("sess", session.RoleView, "", 0)
	revoked, _, _ := s.Create("sess", session.RoleView, "", time.Minute)
	s.Revoke("sess", revoked.ID)
	sessions = nil
	run, _, _ := s.CreateRunLink("run", session.RoleControl, "", time.Minute)

	if n := s.ExpireDue(start); n != 0 || len(sessions) != 0 || len(runs) != 0 {
		t.Fatalf("before any expiry: %d %v %v", n, sessions, runs)
	}
	if n := s.ExpireDue(start.Add(2 * time.Minute)); n != 2 {
		t.Fatalf("told %d, want 2", n)
	}
	if !slices.Equal(sessions, []string{"sess/" + soon.ID}) || !slices.Equal(runs, []string{"run/" + run.ID}) {
		t.Fatalf("hooks: sessions %v runs %v", sessions, runs)
	}
	if n := s.ExpireDue(start.Add(3 * time.Minute)); n != 0 {
		t.Fatalf("told again: %d", n)
	}
	if n := s.ExpireDue(start.Add(2 * time.Hour)); n != 1 || sessions[len(sessions)-1] != "sess/"+later.ID {
		t.Fatalf("the later one: %d %v", n, sessions)
	}
}

// SetGroup says which links it changed and which sessions they no longer
// name, so the viewers attached to those through them can be closed; the
// links name the new group's sessions alone from then on.
func TestSetGroupSaysWhichSessionsLeft(t *testing.T) {
	s := NewStore()
	g := Group{Name: "api", Members: []GroupMember{{Name: "lead", SessionID: "s1"}, {Name: "core", SessionID: "s2"}, {Name: "docs"}}}
	a, _, _ := s.CreateGroupLink("api-1", "owner", g, session.RoleView, "", 0)
	b, _, _ := s.CreateGroupLink("api-1", "owner", g, session.RoleControl, "", 0)
	gone, _, _ := s.CreateGroupLink("api-1", "owner", g, session.RoleControl, "", 0)
	s.RevokeGroup("owner", gone.ID)

	g2 := Group{Name: "api", Members: []GroupMember{{Name: "lead", SessionID: "s1"}, {Name: "core"}, {Name: "docs", SessionID: "s3"}}}
	changed, left := s.SetGroup("owner", "api-1", g2)
	want := []string{a.ID, b.ID}
	slices.Sort(want)
	if !slices.Equal(changed, want) || !slices.Equal(left, []string{"s2"}) {
		t.Fatalf("changed %v left %v", changed, left)
	}
	for _, id := range want {
		l, err := s.Live(id)
		if err != nil || l.Group.Names("s2") || !l.Group.Names("s3") {
			t.Fatalf("link %s after the update: %+v %v", id, l, err)
		}
	}
	if changed, left := s.SetGroup("owner", "api-1", g2); len(changed) != 2 || len(left) != 0 {
		t.Fatalf("the same group again: changed %v left %v", changed, left)
	}
}
