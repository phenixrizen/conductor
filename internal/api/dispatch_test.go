package api

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rocksolidlabs/conductor/internal/config"
	"github.com/rocksolidlabs/conductor/internal/crew"
	"github.com/rocksolidlabs/conductor/internal/dispatch"
	"github.com/rocksolidlabs/conductor/internal/session"
)

// The Dispatch routes (docs/plans/dispatch.md, PR-5), through the real
// store in a temp data directory. Each test has a server and a store of
// its own, and runs beside the others after the package's sequential
// tests: a store's every commit is synced to the disk, and the largest
// fixtures take hundreds of them.

// dispatchOps mints the tests' operation ids.
var dispatchOps atomic.Int64

// opID is a new operation id.
func opID() string { return fmt.Sprintf("op-%d", dispatchOps.Add(1)) }

// dispatchTemplate is the database file of a new Dispatch store, made
// once by the test binary, its directory gone at once.
var dispatchTemplate = sync.OnceValues(func() ([]byte, error) {
	dir, err := os.MkdirTemp("", "dispatch-template-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	ds, err := dispatch.Open(dir)
	if err != nil {
		return nil, err
	}
	if err := ds.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(dir, dispatch.File))
})

// seedDispatch puts a copy of a new Dispatch store where the server opens
// its own under dataDir, as a restart finds one. Making a store's schema
// is most of what opening a new one costs (a tenth of a second under the
// race detector), which some four hundred test environments would pay
// each; a server that finds the schema made only checks it.
func seedDispatch(t *testing.T, dataDir string) {
	t.Helper()
	b, err := dispatchTemplate()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(dataDir, "dispatch")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, dispatch.File), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// dispatchEnv is a test server whose Dispatch store is open for writing,
// made by the server in a data directory of the test's.
func dispatchEnv(t *testing.T) *testEnv {
	t.Helper()
	e := newTestEnv(t, func(cfg *config.Config) { cfg.DataDir = filepath.Join(t.TempDir(), "data") })
	if e.srv.dispatch == nil {
		t.Fatal("no Dispatch store")
	}
	if ro, why := e.srv.dispatch.ReadOnly(); ro {
		t.Fatalf("Dispatch's store is read-only: %s", why)
	}
	return e
}

// encodeNoHTML is v as JSON with HTML escaping off, as the CLI encodes a
// body.
func encodeNoHTML(t *testing.T, v any) []byte {
	t.Helper()
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}

// call sends body with the admin's token (JSON with HTML escaping off, or
// the bytes given as they are) and returns the reply's status, its bytes
// and the reply decoded.
func (e *testEnv) call(t *testing.T, method, path string, body any) (int, []byte, map[string]any) {
	t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
	default:
		rd = bytes.NewReader(encodeNoHTML(t, body))
	}
	req, err := http.NewRequest(method, e.http.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+adminToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if len(data) > 0 {
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("%s %s: the reply is not JSON: %v: %.200s", method, path, err, data)
		}
	}
	return resp.StatusCode, data, out
}

// expect sends body as call does and fails the test unless the reply has
// the status and, for an error, the code; it returns the reply decoded.
func (e *testEnv) expect(t *testing.T, method, path string, body any, status int, code string) map[string]any {
	t.Helper()
	got, data, out := e.call(t, method, path, body)
	if got != status || (code != "" && errorCode(out) != code) {
		t.Fatalf("%s %s: %d %.300s, want %d %s", method, path, got, data, status, code)
	}
	return out
}

// entityOf is the entity a reply names.
func entityOf(t *testing.T, out map[string]any, name string) map[string]any {
	t.Helper()
	v, ok := out[name].(map[string]any)
	if !ok {
		t.Fatalf("no %s in %v", name, out)
	}
	return v
}

// newDispatchProject makes a project through the route, with the fields
// given beside its key and name, and returns it.
func (e *testEnv) newDispatchProject(t *testing.T, key string, fields map[string]any) map[string]any {
	t.Helper()
	body := map[string]any{"opId": opID(), "key": key, "name": key}
	maps.Copy(body, fields)
	return entityOf(t, e.expect(t, "POST", "/api/dispatch/projects", body, http.StatusCreated, ""), "project")
}

// newDispatchTicket makes a ticket of the project through the route, with
// the fields given beside its title, and returns it.
func (e *testEnv) newDispatchTicket(t *testing.T, project, title string, fields map[string]any) map[string]any {
	t.Helper()
	body := map[string]any{"opId": opID(), "project": project, "title": title}
	maps.Copy(body, fields)
	return entityOf(t, e.expect(t, "POST", "/api/dispatch/tickets", body, http.StatusCreated, ""), "ticket")
}

// adminOp is an operation of the admin's, by the display name given, for
// a test that writes through the store.
func adminOp(by string) dispatch.Op { return dispatch.Op{Actor: adminActor, ID: opID(), By: by} }

// doneOf is a check that fails t unless an operation of the store's was
// done, and gives its receipt: done := doneOf(t); done(ds.AddPerson(…)).
func doneOf(t *testing.T) func(dispatch.Receipt, error) dispatch.Receipt {
	return func(rc dispatch.Receipt, err error) dispatch.Receipt {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		if rc.Status != dispatch.StatusDone {
			t.Fatalf("refused: %+v", rc)
		}
		return rc
	}
}

// dispatchRequests is a request of every Dispatch route, by its pattern,
// as a client makes it: what the token and the switchyard tests send.
var dispatchRequests = map[string]struct {
	path string
	body any
}{
	"GET /api/dispatch/projects":                  {"/api/dispatch/projects", nil},
	"POST /api/dispatch/projects":                 {"/api/dispatch/projects", map[string]any{"opId": "a1", "key": "CND", "name": "Conductor"}},
	"GET /api/dispatch/projects/{key}":            {"/api/dispatch/projects/CND", nil},
	"POST /api/dispatch/projects/{key}/edit":      {"/api/dispatch/projects/CND/edit", map[string]any{"opId": "a2", "name": "x"}},
	"POST /api/dispatch/projects/{key}/archive":   {"/api/dispatch/projects/CND/archive", map[string]any{"opId": "a3"}},
	"POST /api/dispatch/projects/{key}/unarchive": {"/api/dispatch/projects/CND/unarchive", map[string]any{"opId": "a4"}},
	"GET /api/dispatch/people":                    {"/api/dispatch/people", nil},
	"POST /api/dispatch/people":                   {"/api/dispatch/people", map[string]any{"opId": "a5", "name": "Alice"}},
	"GET /api/dispatch/tickets":                   {"/api/dispatch/tickets", nil},
	"POST /api/dispatch/tickets":                  {"/api/dispatch/tickets", map[string]any{"opId": "a6", "project": "CND", "title": "t"}},
	"GET /api/dispatch/tickets/{key}":             {"/api/dispatch/tickets/CND-1", nil},
	"POST /api/dispatch/tickets/{key}/edit":       {"/api/dispatch/tickets/CND-1/edit", map[string]any{"opId": "a7", "set": map[string]any{"title": "x"}}},
	"POST /api/dispatch/tickets/{key}/accept":     {"/api/dispatch/tickets/CND-1/accept", map[string]any{"opId": "a8"}},
	"POST /api/dispatch/tickets/{key}/unaccept":   {"/api/dispatch/tickets/CND-1/unaccept", map[string]any{"opId": "a9"}},
	"POST /api/dispatch/tickets/{key}/delete":     {"/api/dispatch/tickets/CND-1/delete", map[string]any{"opId": "a10"}},
	"POST /api/dispatch/tickets/{key}/undelete":   {"/api/dispatch/tickets/CND-1/undelete", map[string]any{"opId": "a11"}},
	"GET /api/dispatch/tickets/{key}/history":     {"/api/dispatch/tickets/CND-1/history", nil},
	"GET /api/dispatch/tickets/{key}/comments":    {"/api/dispatch/tickets/CND-1/comments", nil},
	"GET /api/dispatch/links":                     {"/api/dispatch/links?run=team-0000aaaa", nil},
	"GET /api/dispatch/ops/{opId}":                {"/api/dispatch/ops/a1", nil},
}

// eachDispatchRoute calls f with every Dispatch route the server has, by
// its method and a request of it, and fails the test for a route
// dispatchRequests has no request of, so a route added later is covered.
func eachDispatchRoute(t *testing.T, srv *Server, f func(method, path string, body any)) {
	t.Helper()
	routes := srv.dispatchRoutes()
	if len(routes) != len(dispatchRequests) {
		t.Fatalf("%d routes, %d requests", len(routes), len(dispatchRequests))
	}
	for _, r := range routes {
		req, ok := dispatchRequests[r.pattern]
		if !ok {
			t.Fatalf("no request of %s", r.pattern)
		}
		f(strings.Fields(r.pattern)[0], req.path, req.body)
	}
}

// Every Dispatch route is the admin's (Owner decisions, 17): no token, a
// wrong one, a host token and a share token, a controller's link
// included, are refused 401, and nothing is written.
func TestDispatchRoutesAreTheAdminsAlone(t *testing.T) {
	t.Parallel()
	e := dispatchEnv(t)
	// Every refusal is the token's: the per-client limiter would answer
	// some with 429.
	e.srv.limiter = newRateLimiter(1e6, 1e6)
	_, share, err := e.srv.links.Create("s1", session.RoleControl, "", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	eachDispatchRoute(t, e.srv, func(method, path string, body any) {
		for _, token := range []string{"", "wrong", "test-host-token", share} {
			if resp, out := e.do(method, path, token, body); resp.StatusCode != http.StatusUnauthorized || errorCode(out) != "unauthorized" {
				t.Errorf("%s %s with token %.12q: %d %v", method, path, token, resp.StatusCode, out)
			}
		}
	})
	ctx := context.Background()
	if page, err := e.srv.dispatch.Projects(ctx, true, 0, ""); err != nil || len(page.Projects) != 0 {
		t.Fatalf("a refused call made a project: %+v %v", page, err)
	}
	if people, err := e.srv.dispatch.People(ctx); err != nil || len(people) != 0 {
		t.Fatalf("a refused call added a person: %v %v", people, err)
	}
}

// A switchyard keeps no tickets: every Dispatch route answers 403
// switchyard, and no store is opened in its data directory.
func TestDispatchOnASwitchyard(t *testing.T) {
	t.Parallel()
	e := switchyardEnv(t, true)
	eachDispatchRoute(t, e.srv, func(method, path string, body any) {
		if resp, out := e.do(method, path, adminToken, body); resp.StatusCode != http.StatusForbidden || errorCode(out) != "switchyard" {
			t.Errorf("%s %s: %d %v", method, path, resp.StatusCode, out)
		}
	})
	if e.srv.dispatch != nil {
		t.Fatal("a switchyard opened Dispatch's store")
	}
	if _, err := os.Stat(filepath.Join(e.srv.store.Dir(), "dispatch")); !os.IsNotExist(err) {
		t.Fatalf("a switchyard made Dispatch's directory: %v", err)
	}
}

// Without a data directory there is no store: every route answers 503
// store_unavailable, as the crews routes do. (With one, the environment's
// store, a copy of a new one, opens for writing as a store made there
// would.)
func TestDispatchWithoutADataDirectory(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t, nil)
	if e.srv.dispatch == nil {
		t.Fatal("no Dispatch store in a data directory")
	}
	if ro, why := e.srv.dispatch.ReadOnly(); ro {
		t.Fatalf("the environment's store is read-only: %s", why)
	}
	e.expect(t, "POST", "/api/dispatch/people", map[string]any{"opId": opID(), "name": "Ada"}, http.StatusCreated, "")
	srv, err := New(e.srv.cfg, e.srv.base, e.srv.log, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	none := e.serve(srv)
	eachDispatchRoute(t, srv, func(method, path string, body any) {
		if resp, out := none.do(method, path, adminToken, body); resp.StatusCode != http.StatusServiceUnavailable || errorCode(out) != "store_unavailable" {
			t.Errorf("%s %s: %d %v", method, path, resp.StatusCode, out)
		}
	})
}

// A store that cannot be opened at all (its file a symbolic link) leaves
// the server running without it: every route answers 503
// dispatch_unavailable, unavailable rather than absent.
func TestDispatchStoreThatCannotOpen(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t, func(cfg *config.Config) {
		cfg.DataDir = filepath.Join(t.TempDir(), "data")
		dir := filepath.Join(cfg.DataDir, "dispatch")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(dir, "elsewhere.sqlite"), filepath.Join(dir, dispatch.File)); err != nil {
			t.Fatal(err)
		}
	})
	if e.srv.dispatch != nil {
		t.Fatal("Dispatch opened a store on a symbolic link")
	}
	eachDispatchRoute(t, e.srv, func(method, path string, body any) {
		if resp, out := e.do(method, path, adminToken, body); resp.StatusCode != http.StatusServiceUnavailable || errorCode(out) != "dispatch_unavailable" {
			t.Errorf("%s %s: %d %v", method, path, resp.StatusCode, out)
		}
	})
	if resp, _ := e.do("GET", "/api/crews", adminToken, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("the rest of the API: %d", resp.StatusCode)
	}
}

// openRaw opens the store's database file beside the store, for a test
// that plants what no route makes yet.
func openRaw(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// A database a newer Conductor wrote opens read-only: every read answers
// as it can, and every write is 503 dispatch_unavailable with why, the
// store left as it was.
func TestDispatchReadOnlyStoreAnswersReadsAndRefusesWrites(t *testing.T) {
	t.Parallel()
	done := doneOf(t)
	data := filepath.Join(t.TempDir(), "data")
	dir := filepath.Join(data, "dispatch")
	ds, err := dispatch.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	done(ds.CreateProject(ctx, adminOp(""), dispatch.NewProject{Key: "CND", Name: "Kept"}))
	done(ds.CreateTicket(ctx, adminOp(""), dispatch.NewTicket{Project: "CND", Title: "Kept too"}))
	if err := ds.Close(); err != nil {
		t.Fatal(err)
	}
	db := openRaw(t, filepath.Join(dir, dispatch.File))
	if _, err := db.Exec("UPDATE schema_version SET version = ?", dispatch.Version+1); err != nil {
		t.Fatal(err)
	}
	db.Close()

	e := newTestEnv(t, func(cfg *config.Config) { cfg.DataDir = data })
	if e.srv.dispatch == nil {
		t.Fatal("no store")
	}
	ro, why := e.srv.dispatch.ReadOnly()
	if !ro || !strings.Contains(why, "newer Conductor") {
		t.Fatalf("read-only %v: %s", ro, why)
	}
	out := e.expect(t, "GET", "/api/dispatch/projects", nil, http.StatusOK, "")
	if ps := out["projects"].([]any); len(ps) != 1 || ps[0].(map[string]any)["name"] != "Kept" {
		t.Fatalf("projects: %v", out)
	}
	if tk := entityOf(t, e.expect(t, "GET", "/api/dispatch/tickets/CND-1", nil, http.StatusOK, ""), "ticket"); tk["title"] != "Kept too" {
		t.Fatalf("ticket: %v", tk)
	}
	if out := e.expect(t, "GET", "/api/dispatch/tickets?project=CND", nil, http.StatusOK, ""); out["total"] != 1.0 {
		t.Fatalf("list: %v", out)
	}
	eachDispatchRoute(t, e.srv, func(method, path string, body any) {
		if method != "POST" {
			return
		}
		resp, out := e.do(method, path, adminToken, body)
		if resp.StatusCode != http.StatusServiceUnavailable || errorCode(out) != "dispatch_unavailable" ||
			!strings.Contains(out["error"].(map[string]any)["message"].(string), "newer Conductor") {
			t.Errorf("%s %s: %d %v", method, path, resp.StatusCode, out)
		}
	})
	if tk := entityOf(t, e.expect(t, "GET", "/api/dispatch/tickets/CND-1", nil, http.StatusOK, ""), "ticket"); tk["rev"] != 1.0 {
		t.Fatalf("a refused write changed the ticket: %v", tk)
	}
}

// The project routes: a create, its repeat and a conflicting one, a key
// taken, the read with what its pages offer, an edit with and without the
// revision expected, the archive and its refusals, the list with and
// without the archived ones and its counts, and the bodies the store
// would not look at.
func TestDispatchProjectRoutes(t *testing.T) {
	t.Parallel()
	e := dispatchEnv(t)
	create := map[string]any{"opId": "p1", "byName": "Nate", "key": "CND", "name": "Conductor", "description": "The <app> & more"}
	status, raw, out := e.call(t, "POST", "/api/dispatch/projects", create)
	if status != http.StatusCreated {
		t.Fatalf("create: %d %s", status, raw)
	}
	rc, p := receiptOf(t, out), entityOf(t, out, "project")
	if rc["status"] != "done" || rc["intent"] != "project.create" || rc["key"] != "CND" || rc["id"] != p["id"] || rc["repeat"] != nil {
		t.Fatalf("receipt: %v", rc)
	}
	if p["key"] != "CND" || p["description"] != "The <app> & more" || !slices.Equal(p["closeRequires"].([]any), []any{"acceptance"}) || p["rev"] != rc["rev"] {
		t.Fatalf("project: %v", p)
	}
	// Replies are written with HTML escaping off.
	if !bytes.Contains(raw, []byte(`"The <app> & more"`)) {
		t.Fatalf("the reply escapes HTML: %s", raw)
	}
	// The same again answers the receipt, and makes nothing.
	out = e.expect(t, "POST", "/api/dispatch/projects", create, http.StatusOK, "")
	if again := receiptOf(t, out); again["repeat"] != true || again["id"] != rc["id"] {
		t.Fatalf("repeat: %v", again)
	}
	// The same id with other content is a conflict.
	conflict := maps.Clone(create)
	conflict["name"] = "Other"
	if rc := receiptOf(t, e.expect(t, "POST", "/api/dispatch/projects", conflict, http.StatusConflict, "op_conflict")); rc["status"] != "failed" {
		t.Fatalf("conflict: %v", rc)
	}
	// Another operation with the key: key_taken, kept as a failed receipt.
	taken := maps.Clone(create)
	taken["opId"] = "p2"
	if rc := receiptOf(t, e.expect(t, "POST", "/api/dispatch/projects", taken, http.StatusConflict, "key_taken")); rc["refusal"] != "key_taken" {
		t.Fatalf("key taken: %v", rc)
	}
	e.expect(t, "POST", "/api/dispatch/projects", taken, http.StatusConflict, "key_taken")
	// A key the store refuses, and a field past its bound.
	e.expect(t, "POST", "/api/dispatch/projects", map[string]any{"opId": opID(), "key": "cnd", "name": "x"}, http.StatusConflict, "invalid")
	e.expect(t, "POST", "/api/dispatch/projects", map[string]any{"opId": opID(), "key": "LONG", "name": strings.Repeat("n", dispatch.MaxProjectName+1)}, http.StatusConflict, "bound")
	// What the store would not look at.
	for _, body := range []any{
		map[string]any{"opId": opID(), "key": "BAD", "name": "x", "bogus": true},
		map[string]any{"opId": "an id with spaces", "key": "BAD", "name": "x"},
		map[string]any{"opId": "", "key": "BAD", "name": "x"},
		map[string]any{"opId": opID(), "expectedRev": -1, "key": "BAD", "name": "x"},
		map[string]any{"opId": opID(), "byName": "a\x01b", "key": "BAD", "name": "x"},
		[]byte(`{"opId":"x1","key":"BAD","name":"x"} {}`),
		[]byte(``),
	} {
		e.expect(t, "POST", "/api/dispatch/projects", body, http.StatusBadRequest, "invalid_request")
	}

	// The project's read, with what its pages offer.
	out = e.expect(t, "GET", "/api/dispatch/projects/CND", nil, http.StatusOK, "")
	if entityOf(t, out, "project")["name"] != "Conductor" || len(out["labels"].([]any)) != 0 || len(out["milestones"].([]any)) != 0 || len(out["views"].([]any)) != 0 {
		t.Fatalf("read: %v", out)
	}
	e.expect(t, "GET", "/api/dispatch/projects/NOPE", nil, http.StatusNotFound, "not_found")
	e.expect(t, "GET", "/api/dispatch/projects/cnd", nil, http.StatusBadRequest, "invalid_request")

	// An edit, at the revision it expects; one behind it is refused.
	rev := p["rev"]
	out = e.expect(t, "POST", "/api/dispatch/projects/CND/edit", map[string]any{"opId": opID(), "expectedRev": rev, "name": "Conductor 2"}, http.StatusOK, "")
	if p2 := entityOf(t, out, "project"); p2["name"] != "Conductor 2" || p2["rev"] != rev.(float64)+1 || receiptOf(t, out)["rev"] != p2["rev"] {
		t.Fatalf("edit: %v", out)
	}
	e.expect(t, "POST", "/api/dispatch/projects/CND/edit", map[string]any{"opId": opID(), "expectedRev": rev, "name": "Lost"}, http.StatusConflict, "stale_revision")
	e.expect(t, "POST", "/api/dispatch/projects/NOPE/edit", map[string]any{"opId": opID(), "name": "x"}, http.StatusNotFound, "not_found")
	e.expect(t, "POST", "/api/dispatch/projects/CND/edit", map[string]any{"opId": opID(), "closeRequires": []string{"verified"}}, http.StatusConflict, "invalid")
	e.expect(t, "POST", "/api/dispatch/projects/C/edit", map[string]any{"opId": opID(), "name": "x"}, http.StatusBadRequest, "invalid_request")

	// Archived: every write under it refused, archived again refused.
	if a := entityOf(t, e.expect(t, "POST", "/api/dispatch/projects/CND/archive", map[string]any{"opId": opID()}, http.StatusOK, ""), "project"); a["archivedAt"] == nil {
		t.Fatalf("archive: %v", a)
	}
	e.expect(t, "POST", "/api/dispatch/projects/CND/archive", map[string]any{"opId": opID()}, http.StatusConflict, "invalid")
	e.expect(t, "POST", "/api/dispatch/projects/CND/edit", map[string]any{"opId": opID(), "name": "x"}, http.StatusConflict, "archived")
	e.expect(t, "POST", "/api/dispatch/tickets", map[string]any{"opId": opID(), "project": "CND", "title": "x"}, http.StatusConflict, "archived")

	// The list leaves the archived out unless asked, and counts each
	// project's open and done tickets.
	e.newDispatchProject(t, "OPEN", map[string]any{"closeRequires": []string{}})
	for _, st := range []string{"backlog", "in_progress", "done", "cancelled"} {
		e.newDispatchTicket(t, "OPEN", st, map[string]any{"status": st})
	}
	out = e.expect(t, "GET", "/api/dispatch/projects", nil, http.StatusOK, "")
	if ps := out["projects"].([]any); len(ps) != 1 || ps[0].(map[string]any)["key"] != "OPEN" || out["next"] != nil {
		t.Fatalf("list: %v", out)
	}
	open := out["projects"].([]any)[0].(map[string]any)
	if open["open"] != 2.0 || open["done"] != 1.0 || open["tracker"] != "native" {
		t.Fatalf("counts: %v", open)
	}
	for _, field := range []string{"description", "cwd", "trackerUrl", "closeRequires"} {
		if _, has := open[field]; has {
			t.Fatalf("a summary carries its %s: %v", field, open)
		}
	}
	out = e.expect(t, "GET", "/api/dispatch/projects?archived=1", nil, http.StatusOK, "")
	if ps := out["projects"].([]any); len(ps) != 2 || ps[0].(map[string]any)["key"] != "CND" || ps[0].(map[string]any)["archivedAt"] == nil {
		t.Fatalf("list with the archived: %v", out)
	}
	for _, q := range []string{"?limit=201", "?limit=-1", "?limit=x", "?cursor=nope", "?archived=maybe", "?bogus=1", "?limit=1&limit=2"} {
		e.expect(t, "GET", "/api/dispatch/projects"+q, nil, http.StatusBadRequest, "invalid_request")
	}
	// A page of one goes on from its cursor.
	out = e.expect(t, "GET", "/api/dispatch/projects?archived=1&limit=1", nil, http.StatusOK, "")
	next, _ := out["next"].(string)
	if next == "" {
		t.Fatalf("no next: %v", out)
	}
	out = e.expect(t, "GET", "/api/dispatch/projects?archived=true&limit=1&cursor="+next, nil, http.StatusOK, "")
	if ps := out["projects"].([]any); len(ps) != 1 || ps[0].(map[string]any)["key"] != "OPEN" || out["next"] != nil {
		t.Fatalf("second page: %v", out)
	}

	// Unarchived, it is written again.
	if u := entityOf(t, e.expect(t, "POST", "/api/dispatch/projects/CND/unarchive", map[string]any{"opId": opID()}, http.StatusOK, ""), "project"); u["archivedAt"] != nil {
		t.Fatalf("unarchive: %v", u)
	}
	e.expect(t, "POST", "/api/dispatch/projects/CND/unarchive", map[string]any{"opId": opID()}, http.StatusConflict, "invalid")
	e.expect(t, "POST", "/api/dispatch/projects/CND/edit", map[string]any{"opId": opID(), "name": "Conductor"}, http.StatusOK, "")
}

// The people: the directory read by name, a name added, and one held
// already refused.
func TestDispatchPeopleRoutes(t *testing.T) {
	t.Parallel()
	e := dispatchEnv(t)
	if out := e.expect(t, "GET", "/api/dispatch/people", nil, http.StatusOK, ""); len(out["people"].([]any)) != 0 {
		t.Fatalf("people: %v", out)
	}
	out := e.expect(t, "POST", "/api/dispatch/people", map[string]any{"opId": "n1", "name": "Bob"}, http.StatusCreated, "")
	if receiptOf(t, out)["id"] != "Bob" || entityOf(t, out, "person")["name"] != "Bob" {
		t.Fatalf("add: %v", out)
	}
	e.expect(t, "POST", "/api/dispatch/people", map[string]any{"opId": "n1", "name": "Bob"}, http.StatusOK, "")
	e.expect(t, "POST", "/api/dispatch/people", map[string]any{"opId": opID(), "name": "Bob"}, http.StatusConflict, "invalid")
	e.expect(t, "POST", "/api/dispatch/people", map[string]any{"opId": opID(), "name": ""}, http.StatusConflict, "invalid")
	e.expect(t, "POST", "/api/dispatch/people", map[string]any{"opId": opID(), "name": "Alice"}, http.StatusCreated, "")
	out = e.expect(t, "GET", "/api/dispatch/people", nil, http.StatusOK, "")
	var names []string
	for _, p := range out["people"].([]any) {
		names = append(names, p.(map[string]any)["name"].(string))
	}
	if !slices.Equal(names, []string{"Alice", "Bob"}) {
		t.Fatalf("people: %v", out)
	}
}

// The ticket routes: a create with its key and close state, the read, an
// edit with and without the revision, the close policy's refusal naming
// what is missing, acceptance and its refusal without an owner, a parent
// cycle, a delete and what a tombstone answers, its undelete, the history
// and the comments.
func TestDispatchTicketRoutes(t *testing.T) {
	t.Parallel()
	done := doneOf(t)
	e := dispatchEnv(t)
	ctx := context.Background()
	e.newDispatchProject(t, "CND", nil)
	status, raw, out := e.call(t, "POST", "/api/dispatch/tickets", map[string]any{
		"opId": "t1", "byName": "Nate", "project": "CND", "title": "Ship <it>", "description": "Body", "acceptance": "It ships", "priority": 2,
	})
	if status != http.StatusCreated {
		t.Fatalf("create: %d %s", status, raw)
	}
	rc, tk := receiptOf(t, out), entityOf(t, out, "ticket")
	if rc["key"] != "CND-1" || rc["id"] != tk["id"] || tk["key"] != "CND-1" || tk["title"] != "Ship <it>" || tk["createdBy"] != "Nate" || tk["priority"] != 2.0 {
		t.Fatalf("create: %v", out)
	}
	if !bytes.Contains(raw, []byte(`"Ship <it>"`)) {
		t.Fatalf("the reply escapes HTML: %.300s", raw)
	}
	closeOf := func(tk map[string]any) map[string]any { return tk["close"].(map[string]any) }
	if c := closeOf(tk); !slices.Equal(c["requires"].([]any), []any{"acceptance"}) || !slices.Equal(c["missing"].([]any), []any{"acceptance"}) {
		t.Fatalf("close: %v", c)
	}
	// The read answers the same ticket.
	if got := entityOf(t, e.expect(t, "GET", "/api/dispatch/tickets/CND-1", nil, http.StatusOK, ""), "ticket"); got["rev"] != tk["rev"] || got["description"] != "Body" {
		t.Fatalf("read: %v", got)
	}
	e.expect(t, "GET", "/api/dispatch/tickets/CND-99", nil, http.StatusNotFound, "not_found")
	for _, key := range []string{"cnd-1", "CND-0", "CND-01", "CND"} {
		e.expect(t, "GET", "/api/dispatch/tickets/"+key, nil, http.StatusBadRequest, "invalid_request")
	}
	// A create naming what is not there, or not a project's key.
	e.expect(t, "POST", "/api/dispatch/tickets", map[string]any{"opId": opID(), "project": "NOPE", "title": "x"}, http.StatusNotFound, "not_found")
	e.expect(t, "POST", "/api/dispatch/tickets", map[string]any{"opId": opID(), "project": "CND", "title": "x", "parent": "CND-9"}, http.StatusNotFound, "not_found")
	e.expect(t, "POST", "/api/dispatch/tickets", map[string]any{"opId": opID(), "project": "nope", "title": "x"}, http.StatusBadRequest, "invalid_request")
	e.expect(t, "POST", "/api/dispatch/tickets", map[string]any{"opId": opID(), "project": "CND", "title": "x", "status": "shipped"}, http.StatusConflict, "invalid")

	// An edit of several fields at once, at the revision expected.
	edit := func(key string, body map[string]any, status int, code string) map[string]any {
		t.Helper()
		body["opId"] = opID()
		return e.expect(t, "POST", "/api/dispatch/tickets/"+key+"/edit", body, status, code)
	}
	rev := tk["rev"]
	out = edit("CND-1", map[string]any{"expectedRev": rev, "set": map[string]any{"title": "Ship it", "priority": 3}, "add": map[string]any{"assignees": []string{"person:Ada"}}}, http.StatusOK, "")
	tk = entityOf(t, out, "ticket")
	if tk["title"] != "Ship it" || tk["priority"] != 3.0 || !slices.Equal(tk["assignees"].([]any), []any{"person:Ada"}) || tk["rev"] != rev.(float64)+1 {
		t.Fatalf("edit: %v", tk)
	}
	edit("CND-1", map[string]any{"expectedRev": rev, "set": map[string]any{"title": "Lost"}}, http.StatusConflict, "stale_revision")
	edit("CND-1", map[string]any{"set": map[string]any{"title": "x"}, "bogus": 1}, http.StatusBadRequest, "invalid_request")
	// Done is refused until the policy is met, the receipt naming what it lacks.
	out = edit("CND-1", map[string]any{"set": map[string]any{"status": "done"}}, http.StatusConflict, "close_policy")
	if rc := receiptOf(t, out); !slices.Equal(rc["missing"].([]any), []any{"acceptance"}) {
		t.Fatalf("close_policy: %v", rc)
	}
	// Acceptance is the owner's: refused without one.
	e.expect(t, "POST", "/api/dispatch/tickets/CND-1/accept", map[string]any{"opId": opID()}, http.StatusConflict, "invalid")
	edit("CND-1", map[string]any{"set": map[string]any{"owner": "Alice"}}, http.StatusOK, "")
	out = e.expect(t, "POST", "/api/dispatch/tickets/CND-1/accept", map[string]any{"opId": opID(), "byName": "Nate", "note": "Looks <right>"}, http.StatusOK, "")
	tk = entityOf(t, out, "ticket")
	if tk["acceptedFor"] != "Alice" || tk["acceptedBy"] != "Nate" || tk["acceptedNote"] != "Looks <right>" || closeOf(tk)["met"].(map[string]any)["acceptance"] != true {
		t.Fatalf("accept: %v", tk)
	}
	if tk = entityOf(t, edit("CND-1", map[string]any{"set": map[string]any{"status": "done"}}, http.StatusOK, ""), "ticket"); tk["status"] != "done" || tk["closedAt"] == nil {
		t.Fatalf("done: %v", tk)
	}
	if tk = entityOf(t, e.expect(t, "POST", "/api/dispatch/tickets/CND-1/unaccept", map[string]any{"opId": opID()}, http.StatusOK, ""), "ticket"); tk["acceptedAt"] != nil || tk["status"] != "done" {
		t.Fatalf("unaccept: %v", tk)
	}
	e.expect(t, "POST", "/api/dispatch/tickets/CND-1/unaccept", map[string]any{"opId": opID()}, http.StatusConflict, "invalid")

	// A parent that would make a cycle.
	e.newDispatchTicket(t, "CND", "Child", map[string]any{"parent": "CND-1"})
	edit("CND-1", map[string]any{"set": map[string]any{"parent": "CND-2"}}, http.StatusConflict, "cycle")

	// A delete: a tombstone, read as such, listed only when asked, and
	// every write on it but its undelete not_found.
	if tk = entityOf(t, e.expect(t, "POST", "/api/dispatch/tickets/CND-2/delete", map[string]any{"opId": opID()}, http.StatusOK, ""), "ticket"); tk["deletedAt"] == nil {
		t.Fatalf("delete: %v", tk)
	}
	if tk = entityOf(t, e.expect(t, "GET", "/api/dispatch/tickets/CND-2", nil, http.StatusOK, ""), "ticket"); tk["deletedAt"] == nil || tk["key"] != "CND-2" {
		t.Fatalf("a tombstone's read: %v", tk)
	}
	if rc := receiptOf(t, edit("CND-2", map[string]any{"set": map[string]any{"title": "x"}}, http.StatusNotFound, "not_found")); rc["status"] != "failed" {
		t.Fatalf("edit of a tombstone: %v", rc)
	}
	for _, route := range []string{"accept", "unaccept", "delete"} {
		e.expect(t, "POST", "/api/dispatch/tickets/CND-2/"+route, map[string]any{"opId": opID()}, http.StatusNotFound, "not_found")
	}
	keysOf := func(out map[string]any) []string {
		var keys []string
		for _, tk := range out["tickets"].([]any) {
			keys = append(keys, tk.(map[string]any)["key"].(string))
		}
		slices.Sort(keys)
		return keys
	}
	if keys := keysOf(e.expect(t, "GET", "/api/dispatch/tickets?project=CND", nil, http.StatusOK, "")); !slices.Equal(keys, []string{"CND-1"}) {
		t.Fatalf("list: %v", keys)
	}
	if keys := keysOf(e.expect(t, "GET", "/api/dispatch/tickets?project=CND&deleted=1", nil, http.StatusOK, "")); !slices.Equal(keys, []string{"CND-1", "CND-2"}) {
		t.Fatalf("list with the tombstones: %v", keys)
	}
	if tk = entityOf(t, e.expect(t, "POST", "/api/dispatch/tickets/CND-2/undelete", map[string]any{"opId": opID()}, http.StatusOK, ""), "ticket"); tk["deletedAt"] != nil || tk["parent"].(map[string]any)["key"] != "CND-1" {
		t.Fatalf("undelete: %v", tk)
	}
	e.expect(t, "POST", "/api/dispatch/tickets/CND-2/undelete", map[string]any{"opId": opID()}, http.StatusConflict, "invalid")

	// The history: the fields as the store recorded them, paged.
	out = e.expect(t, "GET", "/api/dispatch/tickets/CND-1/history", nil, http.StatusOK, "")
	changes := out["changes"].([]any)
	var fields []string
	for _, c := range changes {
		if f, _ := c.(map[string]any)["field"].(string); f != "" && !slices.Contains(fields, f) {
			fields = append(fields, f)
		}
	}
	for _, want := range []string{"title", "description", "acceptance", "priority", "owner", "assignees", "status", "accepted_at", "closed_at"} {
		if !slices.Contains(fields, want) {
			t.Fatalf("the history has no %s: %v", want, fields)
		}
	}
	first := changes[0].(map[string]any)
	if first["op"] != "create" || first["actor"] != adminActor || first["by"] != "Nate" || first["opId"] != "t1" {
		t.Fatalf("the first change: %v", first)
	}
	out = e.expect(t, "GET", "/api/dispatch/tickets/CND-1/history?limit=2", nil, http.StatusOK, "")
	if len(out["changes"].([]any)) != 2 || out["next"] == nil {
		t.Fatalf("a page of two: %v", out)
	}
	after := fmt.Sprint(int64(out["next"].(float64)))
	out = e.expect(t, "GET", "/api/dispatch/tickets/CND-1/history?after="+after, nil, http.StatusOK, "")
	if len(out["changes"].([]any)) != len(changes)-2 {
		t.Fatalf("the rest: %d of %d", len(out["changes"].([]any)), len(changes))
	}
	for _, q := range []string{"?limit=501", "?limit=-1", "?after=-1", "?after=x", "?page=2"} {
		e.expect(t, "GET", "/api/dispatch/tickets/CND-1/history"+q, nil, http.StatusBadRequest, "invalid_request")
	}
	e.expect(t, "GET", "/api/dispatch/tickets/CND-9/history", nil, http.StatusNotFound, "not_found")

	// The comments beyond the ticket's page (their add is the next
	// change's route: here, the store's).
	for i := range 3 {
		done(e.srv.dispatch.AddComment(ctx, adminOp("Nate"), "CND-1", fmt.Sprintf("Comment %d", i+1)))
	}
	out = e.expect(t, "GET", "/api/dispatch/tickets/CND-1/comments?after=1&limit=1", nil, http.StatusOK, "")
	if cs := out["comments"].([]any); len(cs) != 1 || cs[0].(map[string]any)["n"] != 2.0 || cs[0].(map[string]any)["body"] != "Comment 2" {
		t.Fatalf("comments: %v", out)
	}
	if cs := e.expect(t, "GET", "/api/dispatch/tickets/CND-1/comments", nil, http.StatusOK, "")["comments"].([]any); len(cs) != 3 {
		t.Fatalf("comments: %v", cs)
	}
	for _, q := range []string{fmt.Sprintf("?limit=%d", maxCommentPage+1), "?limit=-1", "?after=-1", "?limit=x"} {
		e.expect(t, "GET", "/api/dispatch/tickets/CND-1/comments"+q, nil, http.StatusBadRequest, "invalid_request")
	}
	e.expect(t, "GET", "/api/dispatch/tickets/CND-9/comments", nil, http.StatusNotFound, "not_found")
}

// The list filters and sorts on the server, pages by its cursor and says
// how many it holds; a value outside its vocabulary or bound is 400.
func TestDispatchTicketListFiltersAndRefusesBadValues(t *testing.T) {
	t.Parallel()
	done := doneOf(t)
	e := dispatchEnv(t)
	ctx := context.Background()
	e.newDispatchProject(t, "LST", map[string]any{"closeRequires": []string{}})
	label := done(e.srv.dispatch.CreateLabel(ctx, adminOp(""), "LST", dispatch.NewLabel{Name: "api", Color: "blue"})).ID
	milestone := done(e.srv.dispatch.CreateMilestone(ctx, adminOp(""), "LST", dispatch.NewMilestone{Name: "1.0"})).ID
	e.newDispatchTicket(t, "LST", "Alpha search words", map[string]any{"priority": 1, "owner": "Alice", "horizon": "now"})
	e.newDispatchTicket(t, "LST", "Beta", map[string]any{"status": "in_progress", "priority": 3, "assignees": []string{"person:Bob"}, "labels": []string{label}, "horizon": "next", "milestone": milestone})
	e.newDispatchTicket(t, "LST", "Gamma", map[string]any{"status": "done", "parent": "LST-1"})
	done(e.srv.dispatch.AddRelation(ctx, adminOp(""), "LST-1", "blocks", "LST-2"))
	e.newDispatchProject(t, "OTH", nil)
	e.newDispatchTicket(t, "OTH", "Elsewhere", nil)
	keys := func(query string) []string {
		t.Helper()
		out := e.expect(t, "GET", "/api/dispatch/tickets"+query, nil, http.StatusOK, "")
		var keys []string
		for _, tk := range out["tickets"].([]any) {
			keys = append(keys, tk.(map[string]any)["key"].(string))
		}
		if int(out["total"].(float64)) < len(keys) {
			t.Fatalf("%s: a total of %v for %d", query, out["total"], len(keys))
		}
		return keys
	}
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"?project=LST&sort=key", []string{"LST-1", "LST-2", "LST-3"}},
		{"?sort=key", []string{"LST-1", "LST-2", "LST-3", "OTH-1"}},
		{"?project=LST&status=backlog&status=done&sort=key", []string{"LST-1", "LST-3"}},
		{"?project=LST&priority=3", []string{"LST-2"}},
		{"?project=LST&priority=0&priority=1&sort=key", []string{"LST-1", "LST-3"}},
		{"?project=LST&owner=Alice", []string{"LST-1"}},
		{"?project=LST&assignee=person:Bob", []string{"LST-2"}},
		{"?project=LST&label=" + label, []string{"LST-2"}},
		{"?project=LST&milestone=" + milestone, []string{"LST-2"}},
		{"?project=LST&horizon=now&horizon=next&sort=key", []string{"LST-1", "LST-2"}},
		{"?project=LST&horizon=", []string{"LST-3"}},
		{"?parent=LST-1", []string{"LST-3"}},
		{"?project=LST&blocked=1", []string{"LST-2"}},
		{"?project=LST&q=sea", []string{"LST-1"}},
		{"?project=LST&q=alpha+words", []string{"LST-1"}},
		{"?project=LST&sort=priority&desc=1", []string{"LST-2", "LST-1", "LST-3"}},
		{"?project=LST&status=cancelled", nil},
	} {
		if got := keys(tc.query); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.query, got, tc.want)
		}
	}
	// A page goes on from its cursor, and the total is the whole list's.
	out := e.expect(t, "GET", "/api/dispatch/tickets?project=LST&sort=key&limit=2", nil, http.StatusOK, "")
	if len(out["tickets"].([]any)) != 2 || out["total"] != 3.0 {
		t.Fatalf("a page of two: %v", out)
	}
	if got := keys("?project=LST&sort=key&limit=2&cursor=" + out["next"].(string)); !slices.Equal(got, []string{"LST-3"}) {
		t.Fatalf("the next page: %v", got)
	}
	// A summary carries no description.
	if _, has := out["tickets"].([]any)[0].(map[string]any)["description"]; has {
		t.Fatalf("a summary with its description: %v", out)
	}
	for _, q := range []string{
		"?status=shipped", "?priority=x", "?priority=9", "?label=api", "?milestone=1.0", "?horizon=soon", "?parent=LST", "?project=lst",
		"?assignee=Bob", "?sort=random", "?blocked=maybe", "?desc=2", "?limit=201", "?limit=x", "?cursor=nope",
		"?project=LST&project=OTH", "?q=" + strings.Repeat("w", dispatch.MaxQueryText+1), "?bogus=1", "?q=%zz",
	} {
		e.expect(t, "GET", "/api/dispatch/tickets"+q, nil, http.StatusBadRequest, "invalid_request")
	}
	// A cursor of another sort is not this list's.
	e.expect(t, "GET", "/api/dispatch/tickets?project=LST&sort=title&cursor="+out["next"].(string), nil, http.StatusBadRequest, "invalid_request")
}

// A query's parameters are separated by '&' alone, as a browser reads a
// form's: a ';' is part of a value, sent as it is (the web app's encoder
// leaves it so) or escaped, in a search and in a name alike, where
// url.ParseQuery would refuse the query whole.
func TestDispatchAQueryValueMayHoldASemicolon(t *testing.T) {
	t.Parallel()
	e := dispatchEnv(t)
	e.newDispatchProject(t, "SEM", nil)
	e.newDispatchTicket(t, "SEM", "a;b in the title", map[string]any{"owner": "Ann;Bo"})
	e.newDispatchTicket(t, "SEM", "Another", map[string]any{"owner": "Ann"})
	for _, q := range []string{"q=a;b", "q=a%3Bb", "owner=Ann;Bo", "owner=Ann%3BBo", "q=a;b&owner=Ann;Bo"} {
		out := e.expect(t, "GET", "/api/dispatch/tickets?project=SEM&"+q, nil, http.StatusOK, "")
		if tks := out["tickets"].([]any); len(tks) != 1 || tks[0].(map[string]any)["key"] != "SEM-1" {
			t.Errorf("%s: %v", q, out)
		}
	}
	// A ';' in a parameter's name makes no parameter the route takes.
	e.expect(t, "GET", "/api/dispatch/tickets?project=SEM&a;b=1", nil, http.StatusBadRequest, "invalid_request")
}

// A read takes the parameters its route names and no other, as a write's
// body takes no field it does not name: one it does not take, and a query
// string holding a %-escape that is not one, are 400 invalid_request, on
// the reads that take none (the people, a project, a ticket, a receipt)
// as on those that take some, while the same read without them answers.
func TestDispatchReadsRefuseParametersTheyDoNotTake(t *testing.T) {
	t.Parallel()
	e := dispatchEnv(t)
	// What the requests of dispatchRequests read: the project its receipt
	// a1 made, and its ticket.
	e.expect(t, "POST", "/api/dispatch/projects", map[string]any{"opId": "a1", "key": "CND", "name": "Conductor"}, http.StatusCreated, "")
	e.newDispatchTicket(t, "CND", "Read me", nil)
	takeNone := []string{"/api/dispatch/people", "/api/dispatch/projects/CND", "/api/dispatch/tickets/CND-1", "/api/dispatch/ops/a1"}
	reads := 0
	eachDispatchRoute(t, e.srv, func(method, path string, _ any) {
		if method != http.MethodGet {
			return
		}
		reads++
		e.expect(t, method, path, nil, http.StatusOK, "")
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		refused := []string{"bogus=1", "q=%zz", "%zz=1"}
		if slices.Contains(takeNone, path) {
			refused = append(refused, "limit=1", "after=1", "q=x", "x")
		}
		for _, q := range refused {
			e.expect(t, method, path+sep+q, nil, http.StatusBadRequest, "invalid_request")
		}
	})
	if reads != 9 {
		t.Fatalf("%d reads", reads)
	}
}

// A receipt is read back by the admin's op id without acting: a done
// one's and a refused one's, 404 for an id unknown or another actor's,
// 400 for an id that is not one.
func TestDispatchReceiptIsReadBackWithoutActing(t *testing.T) {
	t.Parallel()
	done := doneOf(t)
	e := dispatchEnv(t)
	created := e.expect(t, "POST", "/api/dispatch/projects", map[string]any{"opId": "r1", "key": "CND", "name": "Conductor"}, http.StatusCreated, "")
	project := entityOf(t, created, "project")
	out := e.expect(t, "GET", "/api/dispatch/ops/r1", nil, http.StatusOK, "")
	rc := receiptOf(t, out)
	if rc["opId"] != "r1" || rc["intent"] != "project.create" || rc["status"] != "done" || rc["key"] != "CND" || rc["id"] != project["id"] || rc["rev"] != project["rev"] {
		t.Fatalf("receipt: %v", rc)
	}
	e.expect(t, "POST", "/api/dispatch/projects", map[string]any{"opId": "r2", "key": "CND", "name": "Again"}, http.StatusConflict, "key_taken")
	if rc := receiptOf(t, e.expect(t, "GET", "/api/dispatch/ops/r2", nil, http.StatusOK, "")); rc["status"] != "failed" || rc["refusal"] != "key_taken" {
		t.Fatalf("a refusal's receipt: %v", rc)
	}
	// Another actor's receipt is not the admin's.
	done(e.srv.dispatch.AddPerson(context.Background(), dispatch.Op{Actor: "agent:builder", ID: "r3"}, "Ada"))
	for _, id := range []string{"r3", "r9"} {
		e.expect(t, "GET", "/api/dispatch/ops/"+id, nil, http.StatusNotFound, "not_found")
	}
	e.expect(t, "GET", "/api/dispatch/ops/r%201", nil, http.StatusBadRequest, "invalid_request")
	e.expect(t, "GET", "/api/dispatch/ops/"+strings.Repeat("r", 33), nil, http.StatusBadRequest, "invalid_request")
	// The reads acted on nothing.
	if p := entityOf(t, e.expect(t, "GET", "/api/dispatch/projects/CND", nil, http.StatusOK, ""), "project"); p["rev"] != project["rev"] || p["name"] != "Conductor" {
		t.Fatalf("a read acted: %v", p)
	}
}

// A ticket's body holds every field at its bound with each character
// doubled by escaping: a description at its bound of escaped characters
// is accepted and read back whole, a field past its own bound is the
// store's bound, and a body past maxTicketBody is 413 bound, on the
// create and on the edit. The other writes take 64 KiB.
func TestDispatchTicketBodiesAreBounded(t *testing.T) {
	t.Parallel()
	e := dispatchEnv(t)
	e.newDispatchProject(t, "BND", nil)
	description := strings.Repeat(`"`, dispatch.MaxTicketDescription/2) + strings.Repeat(`\`, dispatch.MaxTicketDescription/2)
	acceptance := strings.Repeat("\n", dispatch.MaxAcceptance)
	title := strings.Repeat(string(rune(0x2028)), dispatch.MaxTicketTitle)
	body := encodeNoHTML(t, map[string]any{"opId": opID(), "project": "BND", "title": title, "description": description, "acceptance": acceptance})
	if len(body) <= 2*(dispatch.MaxTicketDescription+dispatch.MaxAcceptance) || len(body) > maxTicketBody {
		t.Fatalf("a body of %d bytes", len(body))
	}
	e.expect(t, "POST", "/api/dispatch/tickets", body, http.StatusCreated, "")
	tk := entityOf(t, e.expect(t, "GET", "/api/dispatch/tickets/BND-1", nil, http.StatusOK, ""), "ticket")
	if tk["description"] != description || tk["acceptance"] != acceptance || tk["title"] != title {
		t.Fatal("the ticket did not read back whole")
	}
	// A field past its own bound, the body within its: the store's bound.
	e.expect(t, "POST", "/api/dispatch/tickets", map[string]any{"opId": opID(), "project": "BND", "title": "x", "description": strings.Repeat("d", dispatch.MaxTicketDescription+1)}, http.StatusConflict, "bound")
	// A body past its bound by one byte, on the create and the edit, and on
	// a write of 64 KiB.
	pad := func(prefix, suffix string, bound int) []byte {
		return []byte(prefix + strings.Repeat("<", bound+1-len(prefix)-len(suffix)) + suffix)
	}
	for _, tc := range []struct {
		path, prefix, suffix string
		bound                int
	}{
		{"/api/dispatch/tickets", `{"opId":"big1","project":"BND","title":"x","description":"`, `"}`, maxTicketBody},
		{"/api/dispatch/tickets/BND-1/edit", `{"opId":"big2","set":{"description":"`, `"}}`, maxTicketBody},
		{"/api/dispatch/projects", `{"opId":"big3","key":"BIG","name":"x","description":"`, `"}`, maxBody},
		{"/api/dispatch/tickets/BND-1/accept", `{"opId":"big4","note":"`, `"}`, maxBody},
	} {
		raw := pad(tc.prefix, tc.suffix, tc.bound)
		if len(raw) != tc.bound+1 {
			t.Fatalf("a body of %d bytes", len(raw))
		}
		e.expect(t, "POST", tc.path, raw, http.StatusRequestEntityTooLarge, "bound")
	}
	// An edit within the body's bound is taken, a description at its own.
	edit := encodeNoHTML(t, map[string]any{"opId": opID(), "set": map[string]any{"description": strings.Repeat(`\`, dispatch.MaxTicketDescription)}})
	if len(edit) <= 2*dispatch.MaxTicketDescription || len(edit) > maxTicketBody {
		t.Fatalf("an edit of %d bytes", len(edit))
	}
	e.expect(t, "POST", "/api/dispatch/tickets/BND-1/edit", edit, http.StatusOK, "")
}

// filler makes the texts of a test's largest entities from two
// characters, the bits of i choosing between them, so the i-th text of a
// length differs from the others where a rule keeps names unique.
type filler struct {
	name string
	// bytes are one byte each, for a field bounded in bytes; chars one
	// character each, for one bounded in characters.
	bytes, chars [2]string
}

var (
	// worstFiller makes every text at its escaped worst with HTML escaping
	// off: a field bounded in bytes of '"' and '\', two bytes each as
	// JSON; one bounded in characters of U+2028 and U+2029, three bytes
	// each and six as JSON whatever SetEscapeHTML says.
	worstFiller = filler{name: "escaped worst", bytes: [2]string{`"`, `\`}, chars: [2]string{string(rune(0x2028)), string(rune(0x2029))}}
	// angleFiller makes every text of '<' and '>', six bytes each as the
	// default encoder writes them and one as the replies do.
	angleFiller = filler{name: "angle brackets", bytes: [2]string{"<", ">"}, chars: [2]string{"<", ">"}}
)

// pick is n of the two, the j-th the second where bit j of i is set.
func pick(two [2]string, n, i int) string {
	var b strings.Builder
	for j := range n {
		k := 0
		if j < 62 && i>>j&1 == 1 {
			k = 1
		}
		b.WriteString(two[k])
	}
	return b.String()
}

// text is the i-th text of n bytes.
func (f filler) text(n, i int) string { return pick(f.bytes, n, i) }

// runes is the i-th text of n characters.
func (f filler) runes(n, i int) string { return pick(f.chars, n, i) }

// url is the i-th http URL of n bytes.
func (f filler) url(n, i int) string {
	const base = "https://h/"
	return base + f.text(n-len(base), i)
}

// file is the i-th attachment name at its bound of 255 bytes, which no
// '\' may be in: of the characters when they are longer than a byte.
func (f filler) file(i int) string {
	if n := len(f.chars[0]); n > 1 {
		return f.runes(dispatch.MaxAttachmentName/n, i)
	}
	return f.text(dispatch.MaxAttachmentName, i)
}

// largestTicket is what buildLargestTicket made, as the ticket's read
// must give it back.
type largestTicket struct {
	key, by, title, description, acceptance, owner, note, parentTitle string
	assignees                                                         []string
	comments, relationTitles, childTitles                             []string
	links                                                             []dispatch.NewLink
	attachments                                                       []string
}

// buildLargestTicket makes, through the store, the largest ticket the
// server can make, every field and set at its bound and every text of
// f's: its title, description, acceptance criteria, owner, eight
// assignees, twenty labels, a milestone, a parent and the acceptance with
// its note; fifty comments, fifty links, a hundred relations and fifty
// children, each other ticket's title at its bound; and fifty attachments,
// planted in the database as their upload will write them (that route is
// a later change's), every operation by a display name at its bound.
func buildLargestTicket(t *testing.T, e *testEnv, f filler, key string) largestTicket {
	t.Helper()
	done := doneOf(t)
	ctx := context.Background()
	ds := e.srv.dispatch
	by := f.runes(dispatch.MaxByName, 0)
	op := func() dispatch.Op { return adminOp(by) }
	done(ds.CreateProject(ctx, op(), dispatch.NewProject{Key: key, Name: key}))
	var labels []string
	for i := range dispatch.MaxTicketLabels {
		labels = append(labels, done(ds.CreateLabel(ctx, op(), key, dispatch.NewLabel{Name: f.runes(dispatch.MaxLabelName, i), Color: "yellow"})).ID)
	}
	milestone := done(ds.CreateMilestone(ctx, op(), key, dispatch.NewMilestone{Name: f.runes(dispatch.MaxMilestoneName, 0)})).ID
	want := largestTicket{
		by: by, title: f.runes(dispatch.MaxTicketTitle, 1), description: f.text(dispatch.MaxTicketDescription, 0),
		acceptance: f.text(dispatch.MaxAcceptance, 1), owner: f.runes(dispatch.MaxPersonName, 1), note: f.runes(dispatch.MaxAcceptedNote, 0),
		parentTitle: f.runes(dispatch.MaxTicketTitle, 2),
	}
	parent := done(ds.CreateTicket(ctx, op(), dispatch.NewTicket{Project: key, Title: want.parentTitle})).Key
	for i := range dispatch.MaxAssignees {
		want.assignees = append(want.assignees, "person:"+f.runes(dispatch.MaxPersonName, 2+i))
	}
	want.key = done(ds.CreateTicket(ctx, op(), dispatch.NewTicket{
		Project: key, Title: want.title, Description: want.description, Acceptance: want.acceptance,
		Status: dispatch.TicketInProgress, Priority: dispatch.MaxPriority, Horizon: dispatch.HorizonLater, Owner: want.owner,
		Assignees: want.assignees, Labels: labels, Milestone: milestone, Parent: parent,
	})).Key
	done(ds.AcceptTicket(ctx, op(), want.key, want.note))
	for i := range dispatch.MaxRelations {
		title := f.runes(dispatch.MaxTicketTitle, 3+i)
		n := dispatch.NewTicket{Project: key, Title: title, Status: dispatch.TicketInProgress}
		if i < dispatch.MaxTicketChildren {
			n.Parent = want.key
			want.childTitles = append(want.childTitles, title)
		}
		other := done(ds.CreateTicket(ctx, op(), n)).Key
		done(ds.AddRelation(ctx, op(), want.key, dispatch.RelationRelates, other))
		want.relationTitles = append(want.relationTitles, title)
	}
	for i := range dispatch.MaxLinks {
		l := dispatch.NewLink{Kind: dispatch.LinkExternal, Ref: f.text(dispatch.MaxLinkRef, i), URL: f.url(dispatch.MaxLinkURL, i), Note: f.runes(dispatch.MaxLinkNote, i)}
		done(ds.AddLink(ctx, op(), want.key, l))
		want.links = append(want.links, l)
	}
	for i := range dispatch.MaxTicketComments {
		body := f.text(dispatch.MaxCommentBody, i)
		done(ds.AddComment(ctx, op(), want.key, body))
		want.comments = append(want.comments, body)
	}
	tk, err := ds.Ticket(ctx, want.key)
	if err != nil {
		t.Fatal(err)
	}
	db := openRaw(t, filepath.Join(ds.Dir(), dispatch.File))
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	for i := range dispatch.MaxAttachments {
		name := f.file(i)
		if _, err := tx.Exec(`INSERT INTO attachments (id, ticket_id, name, size, sha256, content_type, created_at, created_by) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			fmt.Sprintf("%016x", i+1), tk.ID, name, dispatch.MaxAttachmentSize, strings.Repeat("0123456789abcdef", 4), f.text(dispatch.MaxContentType, i),
			at.Add(time.Duration(i)).Format("2006-01-02T15:04:05.000000000Z"), by); err != nil {
			t.Fatal(err)
		}
		want.attachments = append(want.attachments, name)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return want
}

// The largest ticket the server can make, every field and set at its
// bound, is read back whole and under maxTicketReply: with every text at
// its escaped worst, which is what the bound is, and with every text of
// '<' and '>', which the reply holds as they are and never as the six
// bytes of the default encoder.
func TestDispatchTheLargestTicketIsUnderItsBound(t *testing.T) {
	t.Parallel()
	for i, f := range []filler{worstFiller, angleFiller} {
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			e := dispatchEnv(t)
			want := buildLargestTicket(t, e, f, fmt.Sprintf("BIG%d", i))
			status, raw, _ := e.call(t, "GET", "/api/dispatch/tickets/"+want.key, nil)
			if status != http.StatusOK {
				t.Fatalf("read: %d %.300s", status, raw)
			}
			t.Logf("the largest ticket's reply, %s: %d bytes of %d", f.name, len(raw), maxTicketReply)
			if len(raw) >= maxTicketReply {
				t.Fatalf("the largest ticket's reply is %d bytes, not under %d", len(raw), maxTicketReply)
			}
			if f == angleFiller {
				for _, esc := range []string{"u003c", "u003e"} {
					if bytes.Contains(raw, []byte(`\`+esc)) {
						t.Fatalf("the reply holds the escape of %s", esc)
					}
				}
			}
			var out struct {
				Ticket dispatch.TicketView `json:"ticket"`
			}
			if err := json.Unmarshal(raw, &out); err != nil {
				t.Fatal(err)
			}
			tk := out.Ticket
			if tk.Title != want.title || tk.Description != want.description || tk.Acceptance != want.acceptance || tk.Owner != want.owner ||
				tk.AcceptedNote != want.note || tk.AcceptedBy != want.by || tk.AcceptedFor != want.owner || tk.CreatedBy != want.by {
				t.Fatal("the ticket's own fields did not read back whole")
			}
			if !tk.Close.Met.Acceptance || len(tk.Labels) != dispatch.MaxTicketLabels || tk.MilestoneID == "" || tk.Parent == nil || tk.Parent.Title != want.parentTitle {
				t.Fatalf("the ticket's references: %+v", tk.Close)
			}
			if !slices.Equal(tk.Assignees, slices.Sorted(slices.Values(want.assignees))) {
				t.Fatal("the assignees did not read back whole")
			}
			if len(tk.Comments) != len(want.comments) {
				t.Fatalf("%d comments", len(tk.Comments))
			}
			for i, c := range tk.Comments {
				if c.Body != want.comments[i] || c.By != want.by || c.N != i+1 {
					t.Fatalf("comment %d did not read back whole", i+1)
				}
			}
			if len(tk.Links) != len(want.links) {
				t.Fatalf("%d links", len(tk.Links))
			}
			for i, l := range tk.Links {
				if w := want.links[i]; l.Ref != w.Ref || l.URL != w.URL || l.Note != w.Note || l.Kind != w.Kind || l.CreatedBy != want.by {
					t.Fatalf("link %d did not read back whole", i)
				}
			}
			if len(tk.Relations) != dispatch.MaxRelations || tk.RelationsTotal != dispatch.MaxRelations {
				t.Fatalf("%d relations of %d", len(tk.Relations), tk.RelationsTotal)
			}
			var titles []string
			for _, r := range tk.Relations {
				titles = append(titles, r.Other.Title)
			}
			if !sameSet(titles, want.relationTitles) {
				t.Fatal("the relations' titles did not read back whole")
			}
			titles = titles[:0]
			for _, c := range tk.Children.Tickets {
				titles = append(titles, c.Title)
			}
			if tk.Children.Total != dispatch.MaxTicketChildren || !slices.Equal(titles, want.childTitles) {
				t.Fatalf("the children did not read back whole: %d of %d", len(titles), tk.Children.Total)
			}
			if len(tk.Attachments) != len(want.attachments) {
				t.Fatalf("%d attachments", len(tk.Attachments))
			}
			for i, a := range tk.Attachments {
				if a.Name != want.attachments[i] || a.CreatedBy != want.by {
					t.Fatalf("attachment %d did not read back whole", i)
				}
			}
		})
	}
}

// The largest page of a ticket's comments, maxCommentPage of them, each at
// its escaped worst (a body of 16 KiB of '"' and '\', the author and the
// display name at their bounds, edited and then deleted, so it carries
// both times), is read back whole and under maxTicketReply, as the
// ticket's own read is; the page after it holds the rest, and a page of
// one more is refused. The store's own bound, 200, would be more than
// twice maxTicketReply.
func TestDispatchTheLargestPageOfCommentsIsUnderItsBound(t *testing.T) {
	t.Parallel()
	done := doneOf(t)
	e := dispatchEnv(t)
	ctx := context.Background()
	ds, f := e.srv.dispatch, worstFiller
	actor, by := f.text(dispatch.MaxActor, 0), f.runes(dispatch.MaxByName, 0)
	op := func() dispatch.Op { return dispatch.Op{Actor: actor, ID: opID(), By: by} }
	done(ds.CreateProject(ctx, adminOp(""), dispatch.NewProject{Key: "CMT", Name: "Comments"}))
	key := done(ds.CreateTicket(ctx, adminOp(""), dispatch.NewTicket{Project: "CMT", Title: "Many comments"})).Key
	var bodies []string
	for i := range maxCommentPage + 1 {
		id := done(ds.AddComment(ctx, op(), key, f.text(dispatch.MaxCommentBody, i))).ID
		body := f.text(dispatch.MaxCommentBody, maxCommentPage+1+i)
		done(ds.EditComment(ctx, op(), key, id, body))
		done(ds.DeleteComment(ctx, op(), key, id))
		bodies = append(bodies, body)
	}
	page := func(query string) (raw []byte, comments []dispatch.Comment) {
		t.Helper()
		status, raw, _ := e.call(t, "GET", "/api/dispatch/tickets/"+key+"/comments"+query, nil)
		if status != http.StatusOK {
			t.Fatalf("%s: %d %.300s", query, status, raw)
		}
		var out struct {
			Comments []dispatch.Comment `json:"comments"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return raw, out.Comments
	}
	for _, query := range []string{fmt.Sprintf("?limit=%d", maxCommentPage), ""} {
		raw, comments := page(query)
		t.Logf("the largest page of comments (%q): %d bytes of %d", query, len(raw), maxTicketReply)
		if len(raw) >= maxTicketReply {
			t.Fatalf("the largest page of comments is %d bytes, not under %d", len(raw), maxTicketReply)
		}
		if len(comments) != maxCommentPage {
			t.Fatalf("%q: %d comments", query, len(comments))
		}
		for i, c := range comments {
			if c.N != i+1 || c.Body != bodies[i] || c.Author != actor || c.By != by || c.EditedAt.IsZero() || c.DeletedAt.IsZero() {
				t.Fatalf("comment %d did not read back whole", i+1)
			}
		}
	}
	if _, rest := page(fmt.Sprintf("?after=%d", maxCommentPage)); len(rest) != 1 || rest[0].N != maxCommentPage+1 || rest[0].Body != bodies[maxCommentPage] {
		t.Fatalf("the page after it: %d comments", len(rest))
	}
	e.expect(t, "GET", fmt.Sprintf("/api/dispatch/tickets/%s/comments?limit=%d", key, maxCommentPage+1), nil, http.StatusBadRequest, "invalid_request")
}

// sameSet reports whether a and b hold the same strings, in any order.
func sameSet(a, b []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b)))
}

// worstViewQuery is a saved view's query of as near MaxViewQuery bytes as
// it can be, as stored and as a reply carries it (a view's query is the
// JSON it is, compacted, which leaves it as it is): assignees whose names
// are written as the six-byte escape of U+2028.
func worstViewQuery(t *testing.T) string {
	t.Helper()
	esc := `\` + "u2028"
	entry := func(chars int) string { return `"person:` + strings.Repeat(esc, chars) + `"` }
	var entries []string
	size := func(extra string) int {
		return len(`{"assignees":[`) + len(strings.Join(append(entries, extra), ",")) + len(`]}`)
	}
	for size(entry(dispatch.MaxPersonName)) <= dispatch.MaxViewQuery {
		entries = append(entries, entry(dispatch.MaxPersonName))
	}
	for chars := dispatch.MaxPersonName - 1; chars > 0; chars-- {
		if size(entry(chars)) <= dispatch.MaxViewQuery {
			entries = append(entries, entry(chars))
			break
		}
	}
	q := `{"assignees":[` + strings.Join(entries, ",") + `]}`
	if len(q) > dispatch.MaxViewQuery || len(q) < dispatch.MaxViewQuery-len(esc) {
		t.Fatalf("a query of %d bytes", len(q))
	}
	if _, err := dispatch.ParseViewQuery(q); err != nil {
		t.Fatal(err)
	}
	return q
}

// The largest project read, its fields at their bounds, 100 labels, 100
// milestones with 4 KiB descriptions and 200 views with 8 KiB queries,
// every text at its escaped worst, is under maxProjectReply and read back
// whole.
func TestDispatchTheLargestProjectIsUnderItsBound(t *testing.T) {
	t.Parallel()
	done := doneOf(t)
	e := dispatchEnv(t)
	ctx := context.Background()
	ds, f := e.srv.dispatch, worstFiller
	by := f.runes(dispatch.MaxByName, 0)
	op := func() dispatch.Op { return adminOp(by) }
	project := dispatch.NewProject{
		Key: "LARGE", Name: f.runes(dispatch.MaxProjectName, 0), Description: f.text(dispatch.MaxProjectDescription, 0),
		Cwd: f.text(dispatch.MaxCwd, 1), Tracker: dispatch.TrackerExternal, TrackerURL: f.url(dispatch.MaxTrackerURL, 0),
		CloseRequires: &[]string{dispatch.RequireAcceptance, dispatch.RequireDelivery},
	}
	done(ds.CreateProject(ctx, op(), project))
	for i := range dispatch.MaxLabels {
		done(ds.CreateLabel(ctx, op(), "LARGE", dispatch.NewLabel{Name: f.runes(dispatch.MaxLabelName, i), Color: "yellow"}))
	}
	for i := range dispatch.MaxMilestones {
		done(ds.CreateMilestone(ctx, op(), "LARGE", dispatch.NewMilestone{
			Name: f.runes(dispatch.MaxMilestoneName, i), Description: f.text(dispatch.MaxMilestoneDescription, i), Target: "2026-12-31", State: dispatch.MilestoneClosed,
		}))
	}
	query := worstViewQuery(t)
	for i := range dispatch.MaxViews {
		done(ds.CreateView(ctx, op(), dispatch.NewView{Project: "LARGE", Name: f.runes(dispatch.MaxViewName, i), Kind: dispatch.ViewRoadmap, Query: query}))
	}
	status, raw, _ := e.call(t, "GET", "/api/dispatch/projects/LARGE", nil)
	if status != http.StatusOK {
		t.Fatalf("read: %d %.300s", status, raw)
	}
	t.Logf("the largest project's reply: %d bytes of %d", len(raw), maxProjectReply)
	if len(raw) >= maxProjectReply {
		t.Fatalf("the largest project's reply is %d bytes, not under %d", len(raw), maxProjectReply)
	}
	var out dispatch.ProjectView
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	p := out.Project
	if p.Name != project.Name || p.Description != project.Description || p.Cwd != project.Cwd || p.TrackerURL != project.TrackerURL {
		t.Fatal("the project's fields did not read back whole")
	}
	if len(out.Labels) != dispatch.MaxLabels || len(out.Milestones) != dispatch.MaxMilestones || len(out.Views) != dispatch.MaxViews {
		t.Fatalf("%d labels, %d milestones, %d views", len(out.Labels), len(out.Milestones), len(out.Views))
	}
	for _, v := range out.Views {
		if string(v.Query) != query {
			t.Fatal("a view's query did not read back whole")
		}
	}
	for _, m := range out.Milestones {
		if len(m.Description) != dispatch.MaxMilestoneDescription {
			t.Fatal("a milestone's description did not read back whole")
		}
	}
}

// 201 projects, each with a description of 16 KiB of backslashes, a name
// at its escaped worst, an external tracker's URL and archived, are listed
// whole over two pages, each under maxProjectsPage: a summary carries
// none of what grows.
func TestDispatchProjectsArePagedUnderTheirBound(t *testing.T) {
	t.Parallel()
	done := doneOf(t)
	e := dispatchEnv(t)
	ctx := context.Background()
	ds, f := e.srv.dispatch, worstFiller
	const n = dispatch.MaxProjectPage + 1
	for i := range n {
		key := fmt.Sprintf("P%05d", i)
		done(ds.CreateProject(ctx, adminOp(""), dispatch.NewProject{
			Key: key, Name: f.runes(dispatch.MaxProjectName, i), Description: strings.Repeat(`\`, dispatch.MaxProjectDescription),
			Tracker: dispatch.TrackerExternal, TrackerURL: f.url(dispatch.MaxTrackerURL, i),
		}))
		done(ds.ArchiveProject(ctx, adminOp(""), key))
	}
	var keys []string
	cursor, pages := "", 0
	for {
		path := "/api/dispatch/projects?archived=1"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		status, raw, out := e.call(t, "GET", path, nil)
		if status != http.StatusOK {
			t.Fatalf("page %d: %d %.300s", pages+1, status, raw)
		}
		pages++
		t.Logf("page %d: %d bytes of %d", pages, len(raw), maxProjectsPage)
		if len(raw) >= maxProjectsPage {
			t.Fatalf("page %d is %d bytes, not under %d", pages, len(raw), maxProjectsPage)
		}
		for _, p := range out["projects"].([]any) {
			keys = append(keys, p.(map[string]any)["key"].(string))
		}
		cursor, _ = out["next"].(string)
		if cursor == "" {
			break
		}
	}
	if pages != 2 || len(keys) != n || !slices.IsSorted(keys) || keys[0] != "P00000" || keys[n-1] != fmt.Sprintf("P%05d", n-1) {
		t.Fatalf("%d pages of %d projects", pages, len(keys))
	}
	if out := e.expect(t, "GET", "/api/dispatch/projects", nil, http.StatusOK, ""); len(out["projects"].([]any)) != 0 {
		t.Fatalf("archived projects listed without archived=1: %d", len(out["projects"].([]any)))
	}
}

// A run's ticket, for the run page's chip: none before a link; the one
// ticket a live run of the engine and a run's record are linked to; after
// that ticket's delete, still it with deletedAt; once the run is linked to
// a second ticket, the second, and still after the first's undelete,
// which removes the first's link. A run link names a run the engine holds
// or has a record of, and nothing else.
func TestDispatchRunTicketLookup(t *testing.T) {
	t.Parallel()
	done := doneOf(t)
	e := dispatchEnv(t)
	ctx := context.Background()
	ds := e.srv.dispatch
	live := e.launchCrew(t, "Linked", catMember("a", "immediately"))
	t.Cleanup(func() { e.do("POST", "/api/runs/"+live+"/stop", adminToken, nil) })
	recorded := crew.Run{ID: "recorded-0000beef", CrewID: "recorded", Name: "Recorded", StartedAt: time.Now().UTC().Add(-time.Hour), State: crew.RunFinished}
	if err := e.srv.records.Save(recorded); err != nil {
		t.Fatal(err)
	}
	if _, held := e.srv.runs.Get(recorded.ID); held {
		t.Fatal("the engine holds the recorded run")
	}
	e.newDispatchProject(t, "RUN", nil)
	lookup := func(run string) map[string]any {
		t.Helper()
		out := e.expect(t, "GET", "/api/dispatch/links?run="+run, nil, http.StatusOK, "")
		tk, _ := out["ticket"].(map[string]any)
		return tk
	}
	for _, run := range []string{live, recorded.ID} {
		first := e.newDispatchTicket(t, "RUN", "First for "+run, nil)["key"].(string)
		second := e.newDispatchTicket(t, "RUN", "Second for "+run, nil)["key"].(string)
		if tk := lookup(run); tk != nil {
			t.Fatalf("%s before any link: %v", run, tk)
		}
		done(ds.AddLink(ctx, adminOp(""), first, dispatch.NewLink{Kind: dispatch.LinkRun, Ref: run}))
		if tk := lookup(run); tk == nil || tk["key"] != first || tk["deletedAt"] != nil || tk["title"] != "First for "+run || tk["status"] != "backlog" {
			t.Fatalf("%s linked: %v", run, tk)
		}
		e.expect(t, "POST", "/api/dispatch/tickets/"+first+"/delete", map[string]any{"opId": opID()}, http.StatusOK, "")
		if tk := lookup(run); tk == nil || tk["key"] != first || tk["deletedAt"] == nil {
			t.Fatalf("%s after its ticket's delete: %v", run, tk)
		}
		done(ds.AddLink(ctx, adminOp(""), second, dispatch.NewLink{Kind: dispatch.LinkRun, Ref: run}))
		if tk := lookup(run); tk == nil || tk["key"] != second || tk["deletedAt"] != nil {
			t.Fatalf("%s linked again: %v", run, tk)
		}
		out := e.expect(t, "POST", "/api/dispatch/tickets/"+first+"/undelete", map[string]any{"opId": opID()}, http.StatusOK, "")
		if links := entityOf(t, out, "ticket")["links"].([]any); len(links) != 0 {
			t.Fatalf("the undeleted ticket keeps a link to a run another holds: %v", links)
		}
		if tk := lookup(run); tk == nil || tk["key"] != second || tk["deletedAt"] != nil {
			t.Fatalf("%s after the first's undelete: %v", run, tk)
		}
	}
	// A run neither held nor recorded is no run to link.
	tk := e.newDispatchTicket(t, "RUN", "Nothing", nil)["key"].(string)
	if rc, err := ds.AddLink(ctx, adminOp(""), tk, dispatch.NewLink{Kind: dispatch.LinkRun, Ref: "nobody-0000dead"}); err != nil || rc.Refusal != dispatch.RefusalNotFound {
		t.Fatalf("a run nobody has: %+v %v", rc, err)
	}
	for _, q := range []string{"", "?run=", "?run=not-a-run", "?run=" + live + "&run=" + live, "?run=" + live + "&bogus=1"} {
		e.expect(t, "GET", "/api/dispatch/links"+q, nil, http.StatusBadRequest, "invalid_request")
	}
	if got := lookup("nobody-0000dead"); got != nil {
		t.Fatalf("a run never linked: %v", got)
	}
}

// eventStream opens GET /api/events as the admin and returns what it
// streams as "<event> <data>" strings on a channel closed when the stream
// ends, so a wait on it ends with the stream. The subscription is in place
// when it returns.
func (e *testEnv) eventStream(t *testing.T) <-chan string {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), "GET", e.http.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+adminToken)
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events stream: %d", resp.StatusCode)
	}
	t.Cleanup(func() { resp.Body.Close() })
	events := make(chan string, 4096)
	go func() {
		defer close(events)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(nil, 1<<20)
		var name string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				select {
				case events <- name + " " + strings.TrimPrefix(line, "data: "):
				case <-t.Context().Done():
					return
				}
			}
		}
	}()
	return events
}

// Every write reaches the pages as dispatch events, one per entity it
// changed, a commit's queued together as one entry for every client
// before the write's reply is sent, and written on the stream: a
// project's create, a ticket's (with its new owner, a person whose event
// names no id, in the ticket's entry) and an edit; nothing for a
// refusal.
func TestDispatchEventsReachTheStream(t *testing.T) {
	t.Parallel()
	e := dispatchEnv(t)
	queued := e.srv.events.subscribe()
	defer e.srv.events.unsubscribe(queued)
	stream := e.eventStream(t)
	var want []string
	// commit is the events of the next entry queued, one commit's.
	commit := func(what string) []dispatchEvent {
		t.Helper()
		select {
		case msg := <-queued:
			var evs []dispatchEvent
			for _, data := range dispatchData(t, msg) {
				var ev dispatchEvent
				if err := json.Unmarshal([]byte(data), &ev); err != nil {
					t.Fatal(err)
				}
				want = append(want, "dispatch "+data)
				evs = append(evs, ev)
			}
			return evs
		default:
			t.Fatalf("%s: no event queued by the time the reply came", what)
		}
		return nil
	}
	project := e.newDispatchProject(t, "EVT", nil)
	if evs := commit("the project's create"); len(evs) != 1 || evs[0].Entity != "project" || evs[0].ID != project["id"] || evs[0].Ticket != "" ||
		evs[0].Project != "EVT" || evs[0].Seq < 1 {
		t.Fatalf("the project's events: %+v", evs)
	}
	tk := e.newDispatchTicket(t, "EVT", "Evented", map[string]any{"owner": "Alice"})
	evs := commit("the ticket's create")
	if len(evs) != 2 {
		t.Fatalf("the ticket's create: %+v, want its event and its owner's", evs)
	}
	ticketEv := evs[0]
	if ticketEv.Entity != "ticket" || ticketEv.ID != tk["id"] || ticketEv.Ticket != tk["id"] || ticketEv.Project != "EVT" {
		t.Fatalf("the ticket's event: %+v", ticketEv)
	}
	if ev := evs[1]; ev.Entity != "person" || ev.ID != "" || ev.Ticket != "" || ev.Project != "" || ev.Seq != ticketEv.Seq {
		t.Fatalf("the person's event: %+v", ev)
	}
	e.expect(t, "POST", "/api/dispatch/tickets/EVT-1/edit", map[string]any{"opId": opID(), "set": map[string]any{"title": "Edited"}}, http.StatusOK, "")
	if evs := commit("the edit"); len(evs) != 1 || evs[0].Entity != "ticket" || evs[0].ID != tk["id"] || evs[0].Seq <= ticketEv.Seq {
		t.Fatalf("the edit's events: %+v", evs)
	}
	e.expect(t, "POST", "/api/dispatch/tickets/EVT-1/edit", map[string]any{"opId": opID(), "expectedRev": 1, "set": map[string]any{"title": "Lost"}}, http.StatusConflict, "stale_revision")
	if len(queued) != 0 {
		t.Fatalf("a refusal sent %d events", len(queued))
	}
	// The stream carries each, in order.
	for _, w := range want {
		found := false
		for ev := range stream {
			if ev == w {
				found = true
				break
			}
			if strings.HasPrefix(ev, "dispatch ") {
				t.Fatalf("the stream sent %s before %s", ev, w)
			}
		}
		if !found {
			t.Fatalf("the stream ended before %s", w)
		}
	}
}

// A commit that changed more entities than a client's queue holds
// entries, a label deleted on more tickets than eventQueue, reaches every
// client as one entry and leaves its stream open: a client nobody drains
// while the store commits holds the commit as one entry, the label's
// event and one for the tickets it left, more than maxDispatchEach of
// them, and a live stream carries those and then the next commit's. An
// entry per change dropped the client nobody drained at the 257th, and
// the live stream too unless its handler wrote out enough of them while
// the commit queued them.
func TestDispatchALargeCommitKeepsTheStreamOpen(t *testing.T) {
	t.Parallel()
	e := dispatchEnv(t)
	ds, done, ctx := e.srv.dispatch, doneOf(t), context.Background()
	done(ds.CreateProject(ctx, adminOp("Tester"), dispatch.NewProject{Key: "BIG", Name: "Big"}))
	label := done(ds.CreateLabel(ctx, adminOp("Tester"), "BIG", dispatch.NewLabel{Name: "everywhere", Color: "teal"})).ID
	var tickets []string
	for i := range eventQueue + 1 {
		tickets = append(tickets, done(ds.CreateTicket(ctx, adminOp("Tester"), dispatch.NewTicket{Project: "BIG", Title: fmt.Sprintf("Ticket %d", i+1), Labels: []string{label}})).ID)
	}
	stream := e.eventStream(t)
	queued := e.srv.events.subscribe()
	defer e.srv.events.unsubscribe(queued)
	done(ds.DeleteLabel(ctx, adminOp("Tester"), "BIG", label))
	// The delete's events: the label's, then one for the tickets it left,
	// naming no ticket, with the commit's seq.
	if len(queued) != 1 {
		t.Fatalf("the delete queued %d entries, want 1", len(queued))
	}
	var want []dispatchEvent
	for _, data := range dispatchData(t, <-queued) {
		var ev dispatchEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			t.Fatal(err)
		}
		want = append(want, ev)
	}
	if len(want) != 2 || want[0].Entity != "label" || want[0].ID != label || want[0].Ticket != "" || want[0].Project != "BIG" ||
		want[1] != (dispatchEvent{Seq: want[0].Seq, Entity: "ticket", Project: "BIG"}) {
		t.Fatalf("the delete's events: %+v, want the label's and one for its tickets", want)
	}
	select {
	case _, open := <-queued:
		if !open {
			t.Fatal("the client nobody drained was dropped")
		}
		t.Fatal("an entry more than the delete's")
	default:
	}
	// next is the next dispatch event on the live stream.
	next := func(what string) dispatchEvent {
		t.Helper()
		for ev := range stream {
			data, ok := strings.CutPrefix(ev, "dispatch ")
			if !ok {
				continue
			}
			var got dispatchEvent
			if err := json.Unmarshal([]byte(data), &got); err != nil {
				t.Fatalf("%s: %s: %v", what, data, err)
			}
			return got
		}
		t.Fatalf("the stream ended before %s", what)
		return dispatchEvent{}
	}
	for i, w := range want {
		if ev := next(fmt.Sprintf("the delete's event %d", i)); ev != w {
			t.Fatalf("the stream's event %d of the delete: %+v, want %+v", i, ev, w)
		}
	}
	e.expect(t, "POST", "/api/dispatch/tickets/BIG-1/edit", map[string]any{"opId": opID(), "set": map[string]any{"title": "After"}}, http.StatusOK, "")
	if ev := next("the edit after the delete"); ev.Entity != "ticket" || ev.ID != tickets[0] || ev.Seq <= want[0].Seq {
		t.Fatalf("the edit's event: %+v", ev)
	}
}
