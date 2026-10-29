package dispatch

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestCreateAProject(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	rc, err := s.CreateProject(ctx, testOp("p1"), NewProject{Key: "CND", Name: "Conductor", Description: "The app.\n\n- one\n\t- two", Cwd: "/srv/conductor"})
	want(t, rc, err, StatusDone, "")
	if rc.Key != "CND" || len(rc.ID) != IDLen || rc.Rev != 1 || rc.Intent != "project.create" {
		t.Fatalf("the receipt: %+v", rc)
	}
	p := projectOf(t, s, "CND")
	if p.ID != rc.ID || p.Name != "Conductor" || p.Description != "The app.\n\n- one\n\t- two" || p.Cwd != "/srv/conductor" || p.Tracker != TrackerNative ||
		p.TrackerURL != "" || !slices.Equal(p.CloseRequires, []string{RequireAcceptance}) || p.NextNumber != 1 || p.NumberFrom != 0 || p.NumberTo != 0 ||
		!p.ArchivedAt.IsZero() || p.CreatedAt.IsZero() || p.UpdatedAt.Before(p.CreatedAt) || p.Rev != 1 {
		t.Fatalf("the project: %+v", p)
	}
	// One create row with what never changes, then one set row per field
	// the schema's default does not hold: the tracker is native already.
	rows := changesOf(t, s, "test", "p1")
	var fields []string
	for i, row := range rows {
		if row.N != i || row.Entity != EntityProject || row.EntityID != p.ID || row.ProjectID != p.ID || row.TicketID != "" {
			t.Fatalf("row %d: %+v", i, row)
		}
		fields = append(fields, row.Op+":"+row.Field)
	}
	if strings.Join(fields, " ") != "create: set:name set:description set:cwd set:close_requires" {
		t.Fatalf("the rows: %v", fields)
	}
	var created map[string]any
	if err := json.Unmarshal([]byte(rows[0].Value), &created); err != nil || created["id"] != p.ID || created["key"] != "CND" || created["createdAt"] == nil || len(created) != 3 {
		t.Fatalf("the create row's value: %s %v", rows[0].Value, err)
	}
	if rows[1].Value != "Conductor" || rows[1].Prior != "" || rows[4].Value != `["acceptance"]` || rows[4].Prior != "[]" {
		t.Fatalf("the set rows: %+v", rows[1:])
	}
	// An external project: its tracker's URL, and no requirement by
	// default, its tracker deciding.
	rc, err = s.CreateProject(ctx, testOp("p2"), NewProject{Key: "GH", Name: "GitHub's", Tracker: TrackerExternal, TrackerURL: "https://github.com/rocksolidlabs/conductor/issues"})
	want(t, rc, err, StatusDone, "")
	if gh := projectOf(t, s, "GH"); gh.Tracker != TrackerExternal || gh.TrackerURL != "https://github.com/rocksolidlabs/conductor/issues" || gh.CloseRequires == nil || len(gh.CloseRequires) != 0 {
		t.Fatalf("the external project: %+v", gh)
	}
	if rows := changesOf(t, s, "test", "p2"); len(rows) != 4 || rows[2].Field != "tracker" || rows[3].Field != "tracker_url" {
		t.Fatalf("the external project's rows: %+v", rows)
	}
	// A policy given is kept in its order, whatever the order given; an
	// empty one is none.
	for key, requires := range map[string][]string{"BOTH": {RequireDelivery, RequireAcceptance}, "NONE": {}} {
		rc, err = s.CreateProject(ctx, testOp("p-"+key), NewProject{Key: key, Name: key, CloseRequires: &requires})
		want(t, rc, err, StatusDone, "")
	}
	if got := projectOf(t, s, "BOTH").CloseRequires; !slices.Equal(got, []string{RequireAcceptance, RequireDelivery}) {
		t.Fatalf("both: %v", got)
	}
	if got := projectOf(t, s, "NONE").CloseRequires; len(got) != 0 {
		t.Fatalf("none: %v", got)
	}
}

func TestAKeyHeldIsTaken(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	newProject(t, s, "CND")
	rc, err := s.CreateProject(ctx, testOp("again"), NewProject{Key: "CND", Name: "Another"})
	want(t, rc, err, StatusFailed, RefusalKeyTaken)
	// An archived project's key is still its: projects are never deleted.
	newProject(t, s, "OLD")
	rc, err = s.ArchiveProject(ctx, testOp("archive"), "OLD")
	want(t, rc, err, StatusDone, "")
	rc, err = s.CreateProject(ctx, testOp("old-again"), NewProject{Key: "OLD", Name: "Another"})
	want(t, rc, err, StatusFailed, RefusalKeyTaken)
	if n := count(t, s, "SELECT COUNT(*) FROM projects"); n != 2 {
		t.Fatalf("%d projects", n)
	}
}

func TestAProjectsValuesAreChecked(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	verified, twice := []string{"verified"}, []string{RequireDelivery, RequireDelivery}
	for i, c := range []struct {
		p       NewProject
		refusal string
	}{
		{NewProject{Key: "A", Name: "x"}, RefusalInvalid},
		{NewProject{Key: "ABCDEFG", Name: "x"}, RefusalInvalid},
		{NewProject{Key: "C", Name: ""}, RefusalInvalid},
		{NewProject{Key: "CD", Name: "a\tb"}, RefusalInvalid},
		{NewProject{Key: "CD", Name: strings.Repeat("é", MaxProjectName+1)}, RefusalBound},
		{NewProject{Key: "CD", Name: "x", Description: strings.Repeat("a", MaxProjectDescription+1)}, RefusalBound},
		{NewProject{Key: "CD", Name: "x", Description: "a\rb"}, RefusalInvalid},
		{NewProject{Key: "CD", Name: "x", Cwd: strings.Repeat("a", MaxCwd+1)}, RefusalBound},
		{NewProject{Key: "CD", Name: "x", Cwd: "/a\nb"}, RefusalInvalid},
		{NewProject{Key: "CD", Name: "x", Tracker: "jira"}, RefusalInvalid},
		{NewProject{Key: "CD", Name: "x", TrackerURL: "https://github.com/x"}, RefusalInvalid},
		{NewProject{Key: "CD", Name: "x", Tracker: TrackerExternal}, RefusalInvalid},
		{NewProject{Key: "CD", Name: "x", Tracker: TrackerExternal, TrackerURL: "ftp://h/x"}, RefusalInvalid},
		{NewProject{Key: "CD", Name: "x", Tracker: TrackerExternal, TrackerURL: "https://:443/issues"}, RefusalInvalid},
		{NewProject{Key: "CD", Name: "x", Tracker: TrackerExternal, TrackerURL: "https://h/" + strings.Repeat("a", MaxTrackerURL)}, RefusalBound},
		{NewProject{Key: "CD", Name: "x", CloseRequires: &verified}, RefusalInvalid},
		{NewProject{Key: "CD", Name: "x", CloseRequires: &twice}, RefusalInvalid},
	} {
		rc, err := s.CreateProject(ctx, testOp(opID("bad", i)), c.p)
		want(t, rc, err, StatusFailed, c.refusal)
	}
	if n := count(t, s, "SELECT COUNT(*) FROM projects"); n != 0 {
		t.Fatalf("%d projects made by refused creates", n)
	}
	// At their bounds, every field is taken whole.
	p := NewProject{Key: "ABCDEF", Name: strings.Repeat("é", MaxProjectName), Description: strings.Repeat("d", MaxProjectDescription),
		Cwd: strings.Repeat("c", MaxCwd), Tracker: TrackerExternal, TrackerURL: "https://h/" + strings.Repeat("u", MaxTrackerURL-len("https://h/"))}
	rc, err := s.CreateProject(ctx, testOp("full"), p)
	want(t, rc, err, StatusDone, "")
	got := projectOf(t, s, "ABCDEF")
	if got.Name != p.Name || got.Description != p.Description || got.Cwd != p.Cwd || got.TrackerURL != p.TrackerURL {
		t.Fatal("a field at its bound was not kept whole")
	}
}

func TestEditAProject(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	pid := newProject(t, s, "CND")
	url := "https://github.com/rocksolidlabs/conductor/issues"
	both := []string{RequireDelivery, RequireAcceptance}
	op := testOp("e1")
	op.ExpectedRev = 1
	rc, err := s.EditProject(ctx, op, "CND", ProjectEdit{Name: ptr("Conductor"), Description: ptr("The app"), Tracker: ptr(TrackerExternal), TrackerURL: &url, CloseRequires: &both})
	want(t, rc, err, StatusDone, "")
	if rc.Rev != 2 {
		t.Fatalf("the receipt: %+v", rc)
	}
	rows := changesOf(t, s, "test", "e1")
	var got []string
	for _, row := range rows {
		got = append(got, row.Field+"="+row.Value+"<"+row.Prior)
	}
	if strings.Join(got, " | ") != `name=Conductor<CND | description=The app< | tracker=external<native | tracker_url=`+url+`< | close_requires=["acceptance","delivery"]<["acceptance"]` {
		t.Fatalf("the rows: %v", got)
	}
	p := projectOf(t, s, "CND")
	if p.ID != pid || p.Name != "Conductor" || p.Tracker != TrackerExternal || p.TrackerURL != url || !slices.Equal(p.CloseRequires, []string{RequireAcceptance, RequireDelivery}) || p.Rev != 2 || p.Key != "CND" {
		t.Fatalf("the project: %+v", p)
	}
	// Only what changes is written: the name again and the folder.
	rc, err = s.EditProject(ctx, testOp("e2"), "CND", ProjectEdit{Name: ptr("Conductor"), Cwd: ptr("/srv")})
	want(t, rc, err, StatusDone, "")
	if rows := changesOf(t, s, "test", "e2"); len(rows) != 1 || rows[0].Field != "cwd" || rows[0].Value != "/srv" || rows[0].Prior != "" {
		t.Fatalf("an edit with one change: %+v", rows)
	}
	// An edit that changes nothing is refused, and so is a stale one.
	rc, err = s.EditProject(ctx, testOp("e3"), "CND", ProjectEdit{Name: ptr("Conductor")})
	want(t, rc, err, StatusFailed, RefusalInvalid)
	op = testOp("e4")
	op.ExpectedRev = 2
	rc, err = s.EditProject(ctx, op, "CND", ProjectEdit{Name: ptr("Stale")})
	want(t, rc, err, StatusFailed, RefusalStaleRevision)
	// Back to native: the URL goes with it, or the edit is refused.
	rc, err = s.EditProject(ctx, testOp("e5"), "CND", ProjectEdit{Tracker: ptr(TrackerNative)})
	want(t, rc, err, StatusFailed, RefusalInvalid)
	rc, err = s.EditProject(ctx, testOp("e6"), "CND", ProjectEdit{Tracker: ptr(TrackerNative), TrackerURL: ptr("")})
	want(t, rc, err, StatusDone, "")
	// Each field is checked as a create checks it.
	for i, e := range []ProjectEdit{
		{Name: ptr("")},
		{Name: ptr(strings.Repeat("n", MaxProjectName+1))},
		{Description: ptr("a\x00b")},
		{Cwd: ptr(strings.Repeat("c", MaxCwd+1))},
		{TrackerURL: ptr("https://h/x")},
		{CloseRequires: &[]string{"verified"}},
	} {
		rc, err := s.EditProject(ctx, testOp(opID("bad", i)), "CND", e)
		if err != nil || rc.Status != StatusFailed || (rc.Refusal != RefusalInvalid && rc.Refusal != RefusalBound) {
			t.Fatalf("edit %d: %+v %v", i, rc, err)
		}
	}
	rc, err = s.EditProject(ctx, testOp("none"), "NOPE", ProjectEdit{Name: ptr("x")})
	want(t, rc, err, StatusFailed, RefusalNotFound)
}

// An archived project refuses every write under it, its own edits
// included, until it is unarchived.
func TestAnArchivedProjectRefusesWritesUnderIt(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	newProject(t, s, "CND")
	newTicket(t, s, "CND", "Live")
	newTicket(t, s, "CND", "Deleted")
	rc, err := s.DeleteTicket(ctx, testOp("del"), "CND-2")
	want(t, rc, err, StatusDone, "")
	rc, err = s.ArchiveProject(ctx, testOp("a1"), "CND")
	want(t, rc, err, StatusDone, "")
	p := projectOf(t, s, "CND")
	if p.ArchivedAt.IsZero() || p.Rev != 2 {
		t.Fatalf("archived: %+v", p)
	}
	if rows := changesOf(t, s, "test", "a1"); len(rows) != 1 || rows[0].Field != "archived_at" || rows[0].Value == "" || rows[0].Prior != "" {
		t.Fatalf("the archive's row: %+v", rows)
	}
	refused := func(what string, rc Receipt, err error) {
		t.Helper()
		if err != nil || rc.Status != StatusFailed || rc.Refusal != RefusalArchived {
			t.Fatalf("%s under an archived project: %+v %v", what, rc, err)
		}
	}
	rc, err = s.CreateTicket(ctx, testOp("w1"), NewTicket{Project: "CND", Title: "New"})
	refused("a create", rc, err)
	rc, err = s.EditTicket(ctx, testOp("w2"), "CND-1", TicketEdit{Set: TicketSet{Title: ptr("Renamed")}})
	refused("an edit", rc, err)
	rc, err = s.DeleteTicket(ctx, testOp("w3"), "CND-1")
	refused("a delete", rc, err)
	rc, err = s.UndeleteTicket(ctx, testOp("w4"), "CND-2")
	refused("an undelete", rc, err)
	rc, err = s.EditProject(ctx, testOp("w5"), "CND", ProjectEdit{Name: ptr("Renamed")})
	refused("the project's edit", rc, err)
	rc, err = s.ArchiveProject(ctx, testOp("a2"), "CND")
	want(t, rc, err, StatusFailed, RefusalInvalid)
	rc, err = s.UnarchiveProject(ctx, testOp("u1"), "CND")
	want(t, rc, err, StatusDone, "")
	if p := projectOf(t, s, "CND"); !p.ArchivedAt.IsZero() || p.Rev != 3 {
		t.Fatalf("unarchived: %+v", p)
	}
	if rows := changesOf(t, s, "test", "u1"); len(rows) != 1 || rows[0].Field != "archived_at" || rows[0].Value != "" || rows[0].Prior == "" {
		t.Fatalf("the unarchive's row: %+v", rows)
	}
	rc, err = s.UnarchiveProject(ctx, testOp("u2"), "CND")
	want(t, rc, err, StatusFailed, RefusalInvalid)
	rc, err = s.CreateTicket(ctx, testOp("w6"), NewTicket{Project: "CND", Title: "New"})
	want(t, rc, err, StatusDone, "")
	if rc.Key != "CND-3" {
		t.Fatalf("the create after the unarchive: %+v", rc)
	}
	rc, err = s.ArchiveProject(ctx, testOp("a3"), "NOPE")
	want(t, rc, err, StatusFailed, RefusalNotFound)
}
