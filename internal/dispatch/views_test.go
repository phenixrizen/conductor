package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// newView saves a view through view.create and answers its id.
func newView(t *testing.T, s *Store, op string, v NewView) string {
	t.Helper()
	rc, err := s.CreateView(context.Background(), testOp(op), v)
	return mustDone(t, "view "+v.Name, rc, err).ID
}

// viewNames is the names of the views, in their order.
func viewNames(vs []View) string {
	var names []string
	for _, v := range vs {
		names = append(names, v.Name)
	}
	return strings.Join(names, ", ")
}

func TestCreateEditAndDeleteAView(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	pid := newProject(t, s, "CND")
	query := `{"status":["ready","in_progress"],"sort":"priority","desc":true,"group":"status"}`
	id := newView(t, s, "v1", NewView{Project: "CND", Name: "My work", Kind: ViewList, Query: query})
	v, err := s.View(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if v.ProjectID != pid || v.Name != "My work" || v.Kind != ViewList || string(v.Query) != query || v.Rev != 1 || v.CreatedAt.IsZero() || !v.DeletedAt.IsZero() {
		t.Fatalf("the view: %+v", v)
	}
	// The query reads back as the JSON it is, and as a list's filter.
	vq, err := ParseViewQuery(string(v.Query))
	if err != nil || strings.Join(vq.Status, ",") != "ready,in_progress" || vq.Sort != SortPriority || !vq.Desc || vq.Group != "status" {
		t.Fatalf("the query: %+v %v", vq, err)
	}
	if b, err := json.Marshal(v); err != nil || !strings.Contains(string(b), `"query":`+query) {
		t.Fatalf("as JSON: %s %v", b, err)
	}
	// One create row, then one set row per field the default lacks.
	if got := strings.Join(rowsOf(t, s, "v1"), " | "); !strings.HasPrefix(got, "create:=") ||
		!strings.Contains(got, "set:name=My work<") || !strings.Contains(got, "set:kind=list<") || !strings.Contains(got, "set:query="+query+"<{}") {
		t.Fatalf("the create's rows: %s", got)
	}
	// An edit sets what it names, one row each, the rev bumped once.
	rc, err := s.EditView(ctx, Op{Actor: "test", ID: "v2", ExpectedRev: 1}, id, ViewEdit{Name: ptr("Board"), Kind: ptr(ViewBoard)})
	if mustDone(t, "edit", rc, err).Rev != 2 {
		t.Fatalf("the edit's rev: %+v", rc)
	}
	if got := strings.Join(rowsOf(t, s, "v2"), " | "); got != "set:name=Board<My work | set:kind=board<list" {
		t.Fatalf("the edit's rows: %s", got)
	}
	rc, err = s.EditView(ctx, Op{Actor: "test", ID: "v3", ExpectedRev: 1}, id, ViewEdit{Name: ptr("Stale")})
	want(t, rc, err, StatusFailed, RefusalStaleRevision)
	rc, err = s.EditView(ctx, testOp("v4"), id, ViewEdit{Name: ptr("Board")})
	want(t, rc, err, StatusFailed, RefusalInvalid)
	rc, err = s.EditView(ctx, testOp("v5"), id, ViewEdit{Kind: ptr("calendar")})
	want(t, rc, err, StatusFailed, RefusalInvalid)
	// A delete is a tombstone: the row and its history kept, the views
	// read without it, every write on it not_found.
	rc, err = s.DeleteView(ctx, testOp("v6"), id)
	mustDone(t, "delete", rc, err)
	v, err = s.View(ctx, id)
	if err != nil || v.DeletedAt.IsZero() || v.Name != "Board" {
		t.Fatalf("the tombstone: %+v %v", v, err)
	}
	if vs, err := s.Views(ctx, "CND"); err != nil || len(vs) != 0 {
		t.Fatalf("the views after the delete: %v %v", viewNames(vs), err)
	}
	for _, rc := range []func() (Receipt, error){
		func() (Receipt, error) { return s.EditView(ctx, testOp("v7"), id, ViewEdit{Name: ptr("Again")}) },
		func() (Receipt, error) { return s.DeleteView(ctx, testOp("v8"), id) },
		func() (Receipt, error) { return s.EditView(ctx, testOp("v9"), newID(), ViewEdit{Name: ptr("None")}) },
	} {
		r, err := rc()
		want(t, r, err, StatusFailed, RefusalNotFound)
	}
	if _, err := s.View(ctx, newID()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no view: %v", err)
	}
}

func TestAViewsQueryIsAListsFilterWithinItsBound(t *testing.T) {
	s := openStore(t, storeDir(t))
	newProject(t, s, "CND")
	// At its bound, padded with white space, it is saved; one byte past
	// it is bound.
	pad := func(n int) string {
		body := `{"sort":"title"`
		return body + strings.Repeat(" ", n-len(body)-1) + "}"
	}
	newView(t, s, "at", NewView{Project: "CND", Name: "At the bound", Kind: ViewList, Query: pad(MaxViewQuery)})
	rc, err := s.CreateView(context.Background(), testOp("past"), NewView{Project: "CND", Name: "Past", Kind: ViewList, Query: pad(MaxViewQuery + 1)})
	want(t, rc, err, StatusFailed, RefusalBound)
	for i, q := range []string{
		``, `{`, `[]`, `"list"`, `null`, `{} {}`, `{}}`,
		`{"color":"red"}`,
		`{"status":["open"]}`,
		`{"priority":[9]}`,
		`{"labels":["api"]}`,
		`{"sort":"rank"}`,
		`{"group":"label"}`,
		`{"q":"` + strings.Repeat("a", MaxQueryText+1) + `"}`,
	} {
		rc, err := s.CreateView(context.Background(), testOp(opID("bad", i)), NewView{Project: "CND", Name: "Bad", Kind: ViewList, Query: q})
		if err != nil || rc.Status != StatusFailed || (rc.Refusal != RefusalInvalid && rc.Refusal != RefusalBound) {
			t.Errorf("the query %.40q: %+v %v", q, rc, err)
		}
	}
	// Every field of a list's filter and every grouping is a view's.
	full := `{"status":["backlog"],"priority":[0,4],"labels":["0123456789abcdef"],"owner":"Alice","assignees":["agent:claude"],` +
		`"milestone":"0123456789abcdef","horizon":["","now"],"parent":"CND-1","blocked":true,"q":"login","deleted":true,"sort":"milestone","desc":true,"group":"owner"}`
	newView(t, s, "full", NewView{Project: "CND", Name: "Every field", Kind: ViewRoadmap, Query: full})
	for _, g := range Groupings {
		q, _ := json.Marshal(ViewQuery{Group: g})
		newView(t, s, "group-"+g, NewView{Project: "CND", Name: "By " + g, Kind: ViewList, Query: string(q)})
	}
}

func TestAViewOfEveryProjectAndTheViewsAProjectOffers(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	newProject(t, s, "CND")
	newProject(t, s, "OPS")
	every := newView(t, s, "e", NewView{Name: "Everything open", Kind: ViewList, Query: `{"status":["ready"]}`})
	newView(t, s, "c", NewView{Project: "CND", Name: "cnd board", Kind: ViewBoard, Query: `{}`})
	newView(t, s, "o", NewView{Project: "OPS", Name: "Ops roadmap", Kind: ViewRoadmap, Query: `{}`})
	v, err := s.View(ctx, every)
	if err != nil || v.ProjectID != "" {
		t.Fatalf("a view of every project: %+v %v", v, err)
	}
	// A project offers its own and every project's, by name whatever the
	// case; "" reads every view.
	for project, want := range map[string]string{"CND": "cnd board, Everything open", "OPS": "Everything open, Ops roadmap", "": "cnd board, Everything open, Ops roadmap"} {
		vs, err := s.Views(ctx, project)
		if err != nil || viewNames(vs) != want {
			t.Errorf("the views of %q: %s %v", project, viewNames(vs), err)
		}
	}
	if _, err := s.Views(ctx, "NONE"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the views of no project: %v", err)
	}
	// Its change rows name no project.
	if rows := changesOf(t, s, "test", "e"); len(rows) == 0 || rows[0].ProjectID != "" || rows[0].Entity != EntityView {
		t.Fatalf("its rows: %+v", rows)
	}
	// A view of no project is not_found; of an archived one, archived,
	// and so is every write on a view of it.
	rc, err := s.CreateView(ctx, testOp("n"), NewView{Project: "NONE", Name: "None", Kind: ViewList, Query: `{}`})
	want(t, rc, err, StatusFailed, RefusalNotFound)
	cnd, err := s.Views(ctx, "CND")
	if err != nil {
		t.Fatal(err)
	}
	rc, err = s.ArchiveProject(ctx, testOp("a"), "CND")
	mustDone(t, "archive", rc, err)
	rc, err = s.CreateView(ctx, testOp("x"), NewView{Project: "CND", Name: "Archived", Kind: ViewList, Query: `{}`})
	want(t, rc, err, StatusFailed, RefusalArchived)
	for _, v := range cnd {
		if v.ProjectID == "" {
			continue
		}
		rc, err = s.EditView(ctx, testOp("y"), v.ID, ViewEdit{Name: ptr("Renamed")})
		want(t, rc, err, StatusFailed, RefusalArchived)
		rc, err = s.DeleteView(ctx, testOp("z"), v.ID)
		want(t, rc, err, StatusFailed, RefusalArchived)
	}
	// A view of every project is no archived project's.
	rc, err = s.EditView(ctx, testOp("w"), every, ViewEdit{Name: ptr("Everything")})
	mustDone(t, "edit a view of every project", rc, err)
}

func TestAtMostMaxViewsLive(t *testing.T) {
	s := openStore(t, storeDir(t))
	ctx := context.Background()
	pid := newProject(t, s, "CND")
	now := stamp(time.Now())
	for range MaxViews - 1 {
		if _, err := s.db.Exec("INSERT INTO views (id, project_id, name, kind, query, created_at, updated_at, rev) VALUES (?, ?, 'v', 'list', '{}', ?, ?, 1)",
			newID(), pid, now, now); err != nil {
			t.Fatal(err)
		}
	}
	last := newView(t, s, "last", NewView{Name: "The last", Kind: ViewList, Query: `{}`})
	rc, err := s.CreateView(ctx, testOp("past"), NewView{Project: "CND", Name: "Past", Kind: ViewList, Query: `{}`})
	want(t, rc, err, StatusFailed, RefusalBound)
	rc, err = s.DeleteView(ctx, testOp("del"), last)
	mustDone(t, "delete", rc, err)
	newView(t, s, "again", NewView{Project: "CND", Name: "Again", Kind: ViewList, Query: `{}`})
}
