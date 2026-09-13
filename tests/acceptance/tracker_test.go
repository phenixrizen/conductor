package acceptance_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/tracker"
	"github.com/phenixrizen/conductor/internal/trackerworker"
	"github.com/phenixrizen/conductor/internal/trackerworkflow"
	"github.com/phenixrizen/conductor/pkg/client"
	enumspb "go.temporal.io/api/enums/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const trackerOrg = "11111111-1111-4111-8111-111111111111"
const trackerTeam = "22222222-2222-4222-8222-222222222222"
const trackerIssueID = "33333333-3333-4333-8333-333333333333"
const trackerStatus = "44444444-4444-4444-8444-444444444444"
const trackerToken = "synthetic-tracker-secret"

type trackerProviderFixture struct {
	mu                                           sync.Mutex
	provider, issue, scope, status, organization string
	projection                                   *domain.TrackerProjection
	writes, reads                                int
	dropAfterWrite, dropBeforeWrite              bool
	title                                        string
}

func trackerConfig(provider string) domain.TrackerConfig {
	c := domain.TrackerConfig{WorkspaceID: "team", Provider: provider, Host: "linear.app", OrganizationID: trackerOrg, ScopeID: trackerTeam, Profile: domain.LinearTrackerProfile, CredentialID: "tracker-api", WebhookCredentialID: "tracker-hook", ConductorOrigin: "https://conductor.example.invalid", Enabled: true, Statuses: []domain.TrackerStatusMapping{{ID: trackerStatus, Display: "Tracker planning done"}}, Grants: []domain.TrackerGrant{}}
	if provider == "jira" {
		c.Host = "synthetic.atlassian.net"
		c.OrganizationID = ""
		c.ScopeID = "100"
		c.Profile = domain.JiraTrackerProfile
		c.Statuses[0].ID = "300"
	}
	for _, name := range []string{"author", "reviewer", "agent", "reader"} {
		c.Grants = append(c.Grants, domain.TrackerGrant{PrincipalID: "person-" + name, CanRead: true, CanSync: name != "reader", CanResolve: name == "author" || name == "agent"})
	}
	return c
}
func newTrackerProvider(t *testing.T, c domain.TrackerConfig) (*trackerProviderFixture, trackerworker.Factory) {
	t.Helper()
	p := &trackerProviderFixture{provider: c.Provider, issue: trackerIssueID, scope: c.ScopeID, status: c.Statuses[0].ID, organization: c.OrganizationID, title: "Synthetic tracker source canary"}
	if c.Provider == "jira" {
		p.issue = "200"
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		if c.Provider == "linear" {
			if r.Header.Get("Authorization") != trackerToken {
				t.Error("missing scoped Linear credential")
				w.WriteHeader(401)
				return
			}
		} else {
			email, token, ok := r.BasicAuth()
			if !ok || email != "synthetic@example.invalid" || token != trackerToken {
				t.Error("missing scoped Jira credential")
				w.WriteHeader(401)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		output := func(v any) { _ = json.NewEncoder(w).Encode(v) }
		drop := func() {
			conn, _, e := w.(http.Hijacker).Hijack()
			if e != nil {
				t.Error(e)
			} else {
				_ = conn.Close()
			}
		}
		if c.Provider == "linear" {
			var request struct {
				Query     string                     `json:"query"`
				Variables map[string]json.RawMessage `json:"variables"`
			}
			if json.NewDecoder(r.Body).Decode(&request) != nil {
				t.Error("invalid GraphQL request")
				w.WriteHeader(400)
				return
			}
			if strings.Contains(request.Query, "attachmentCreate") {
				p.writes++
				if p.dropBeforeWrite {
					drop()
					return
				}
				var input struct{ URL, Title, Subtitle, IssueID string }
				_ = json.Unmarshal(request.Variables["input"], &input)
				if input.IssueID != p.issue {
					t.Error("foreign issue write")
				}
				p.projection = &domain.TrackerProjection{URL: input.URL, Title: input.Title, Summary: input.Subtitle}
				if p.dropAfterWrite {
					drop()
					return
				}
				output(map[string]any{"data": map[string]any{"attachmentCreate": map[string]any{"success": true, "attachment": map[string]string{"id": "synthetic-attachment"}}}})
				return
			}
			p.reads++
			nodes := []any{}
			if p.projection != nil {
				nodes = append(nodes, map[string]any{"url": p.projection.URL, "title": p.projection.Title, "subtitle": p.projection.Summary, "issue": map[string]string{"id": p.issue}})
			}
			output(map[string]any{"data": map[string]any{"organization": map[string]string{"id": p.organization}, "issue": map[string]any{"id": p.issue, "identifier": "SYN-1", "url": "https://linear.app/synthetic/issue/SYN-1", "title": p.title, "description": "Synthetic tracker-owned description", "priority": 2, "updatedAt": "2026-09-13T00:00:00Z", "team": map[string]string{"id": p.scope}, "assignee": map[string]string{"id": "synthetic-assignee"}, "state": map[string]string{"id": p.status, "name": "Done"}}, "attachmentsForURL": map[string]any{"nodes": nodes, "pageInfo": map[string]bool{"hasNextPage": false}}}})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/remotelink") {
			if r.Method == "GET" {
				p.reads++
				if p.projection == nil {
					w.WriteHeader(404)
					return
				}
				output(map[string]any{"globalId": p.projection.URL, "object": p.projection})
				return
			}
			if r.Method == "POST" {
				p.writes++
				if p.dropBeforeWrite {
					drop()
					return
				}
				var input struct {
					GlobalID string                   `json:"globalId"`
					Object   domain.TrackerProjection `json:"object"`
				}
				if json.NewDecoder(r.Body).Decode(&input) != nil {
					t.Error("invalid remote link write")
				}
				if input.GlobalID != input.Object.URL {
					t.Error("lost idempotent remote link identity")
				}
				p.projection = &input.Object
				if p.dropAfterWrite {
					drop()
					return
				}
				output(map[string]any{"id": 1})
				return
			}
		}
		p.reads++
		output(map[string]any{"id": p.issue, "key": "SYN-1", "fields": map[string]any{"summary": p.title, "description": map[string]any{"type": "doc", "version": 1, "content": []any{}}, "updated": "2026-09-13T00:00:00.000+0000", "project": map[string]string{"id": p.scope}, "priority": map[string]string{"id": "2", "name": "High"}, "assignee": map[string]string{"accountId": "synthetic-assignee"}, "status": map[string]string{"id": p.status, "name": "Done"}}})
	}))
	t.Cleanup(server.Close)
	return p, func(config domain.TrackerConfig, credential tracker.Credential, check func(context.Context) error) (trackerworker.Provider, error) {
		return tracker.New(config, credential, check, tracker.Options{Origin: server.URL, AllowInsecureLoopback: true})
	}
}
func trackerCredentials(_ context.Context, c domain.TrackerConfig, id string) (tracker.Credential, error) {
	if id != c.CredentialID && id != c.WebhookCredentialID {
		return tracker.Credential{}, domain.ErrForbidden
	}
	return tracker.Credential{Token: trackerToken, Email: "synthetic@example.invalid"}, nil
}
func trackerClient(t *testing.T, f *accessFixture, name string) *client.Client {
	t.Helper()
	c, e := client.NewAuthenticated(f.server.URL, f.tokens[name], "team", "application")
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func createTrackerLink(t *testing.T, f *accessFixture, c *client.Client, p *trackerProviderFixture, private bool) domain.TrackerLink {
	t.Helper()
	pkg := f.create("author", "team", "application", domain.Content{"intent": "Synthetic tracked package"})
	refs := []domain.TrackerPackageRef{{RepositoryID: "application", PackageID: pkg.ID, Revision: pkg.Revision.Number, Digest: pkg.Revision.Digest}}
	if private {
		v := f.create("author", "team", "private", domain.Content{"intent": "Private synthetic package"})
		refs = append(refs, domain.TrackerPackageRef{RepositoryID: "private", PackageID: v.ID, Revision: v.Revision.Number, Digest: v.Revision.Digest})
	}
	link, e := c.CreateTrackerLink(f.ctx, "synthetic-tracker-link", domain.TrackerLinkInput{IssueID: p.issue, Packages: refs})
	if e != nil {
		t.Fatal(e)
	}
	if link.LatestSyncID == "" {
		t.Fatal("initial durable refresh was not exposed")
	}
	return link
}
func runTrackerActivity(t *testing.T, f *accessFixture, a *trackerworker.Activity, id string) trackerworkflow.Result {
	t.Helper()
	var binding string
	if e := f.sql.QueryRow(f.ctx, `SELECT binding FROM tracker_syncs WHERE id=$1`, id).Scan(&binding); e != nil {
		t.Fatal(e)
	}
	result, e := a.Sync(f.ctx, trackerworkflow.Reference{ID: id, Binding: binding})
	if e != nil {
		t.Fatal(e)
	}
	return result
}
func TestTrackerProvidersSynchronizationConflictsAndLostAcknowledgment(t *testing.T) {
	for _, provider := range []string{"linear", "jira"} {
		t.Run(provider, func(t *testing.T) {
			f := newAccessFixture(t)
			config := trackerConfig(provider)
			if e := f.db.ApplyTrackerConfig(f.ctx, "synthetic-operator", config); e != nil {
				t.Fatal(e)
			}
			p, factory := newTrackerProvider(t, config)
			a, e := trackerworker.NewActivity(f.db, trackerCredentials, factory)
			if e != nil {
				t.Fatal(e)
			}
			c := trackerClient(t, f, "author")
			settings, e := c.GetTracker(f.ctx)
			if e != nil || settings.Provider != provider || !settings.CanSync || !settings.CanResolve {
				t.Fatalf("tracker settings: %+v %v", settings, e)
			}
			link := createTrackerLink(t, f, c, p, true)
			reader := trackerClient(t, f, "reviewer")
			if _, e = reader.GetTrackerLink(f.ctx, link.ID); e == nil {
				t.Fatal("private package link leaked across repositories")
			}
			page, e := reader.ListTrackerLinks(f.ctx, "", 1)
			if e != nil || len(page.Links) != 0 || page.Truncated || page.Next != "" {
				t.Fatalf("hidden ticket leaked pagination: %+v %v", page, e)
			}
			runTrackerActivity(t, f, a, link.LatestSyncID)
			link, e = c.GetTrackerLink(f.ctx, link.ID)
			if e != nil || link.Observation == nil || link.Observation.State != "refreshed" || link.Observation.Issue.Mapping != "mapped" {
				t.Fatalf("authoritative refresh: %+v %v", link, e)
			}
			publish, e := c.RequestTrackerSync(f.ctx, link.ID, "publish-once", domain.TrackerSyncInput{LinkDigest: link.Digest, Mode: "publish"})
			if e != nil {
				t.Fatal(e)
			}
			p.mu.Lock()
			p.dropAfterWrite = true
			p.mu.Unlock()
			result := runTrackerActivity(t, f, a, publish.ID)
			syncResult, e := c.GetTrackerSync(f.ctx, publish.ID)
			if e != nil || syncResult.Observation == nil || syncResult.Observation.State != "synchronized" {
				t.Fatalf("lost acknowledgement was not reconciled: %+v %v", syncResult, e)
			}
			if replay := runTrackerActivity(t, f, a, publish.ID); replay != result {
				t.Fatal("committed synchronization changed on retry")
			}
			p.mu.Lock()
			if p.writes != 1 {
				t.Errorf("hidden mutation replay: %d", p.writes)
			}
			p.dropAfterWrite = false
			p.projection.Summary = "External competing edit"
			p.mu.Unlock()
			refresh, e := c.RequestTrackerSync(f.ctx, link.ID, "refresh-conflict", domain.TrackerSyncInput{LinkDigest: link.Digest, Mode: "refresh"})
			if e != nil {
				t.Fatal(e)
			}
			runTrackerActivity(t, f, a, refresh.ID)
			link, e = c.GetTrackerLink(f.ctx, link.ID)
			if e != nil || link.Observation.State != "conflict" {
				t.Fatalf("external projection edit was not visible: %+v %v", link.Observation, e)
			}
			in := domain.TrackerSyncInput{LinkDigest: link.Digest, Mode: "restore", ExpectedProjectionDigest: link.Observation.ProjectionDigest}
			agent := trackerClient(t, f, "agent")
			if _, e = agent.RequestTrackerSync(f.ctx, link.ID, "agent-restore", in); e == nil {
				t.Fatal("agent resolved a human projection conflict")
			}
			restore, e := c.RequestTrackerSync(f.ctx, link.ID, "human-restore", in)
			if e != nil {
				t.Fatal(e)
			}
			runTrackerActivity(t, f, a, restore.ID)
			got, e := c.GetTrackerSync(f.ctx, restore.ID)
			if e != nil || got.Observation.State != "synchronized" {
				t.Fatalf("explicit conflict resolution: %+v %v", got, e)
			}
			for _, ref := range link.Input.Packages {
				var approvals int
				if e = f.sql.QueryRow(f.ctx, `SELECT count(*) FROM approvals WHERE change_id=$1`, ref.PackageID).Scan(&approvals); e != nil || approvals != 0 {
					t.Fatal("tracker done state granted design approval")
				}
			}
			switched := config
			if provider == "linear" {
				switched.ScopeID = "55555555-5555-4555-8555-555555555555"
			} else {
				switched.ScopeID = "101"
			}
			if e = f.db.ApplyTrackerConfig(f.ctx, "synthetic-operator", switched); !errors.Is(e, domain.ErrConflict) {
				t.Fatalf("provider scope replaced retained identity: %v", e)
			}
			if _, e = f.sql.Exec(f.ctx, `UPDATE tracker_grants SET can_read=false,can_sync=false,can_resolve=false WHERE workspace_id='team' AND principal_id='person-author'`); e != nil {
				t.Fatal(e)
			}
			if _, e = c.GetTrackerLink(f.ctx, link.ID); e == nil {
				t.Fatal("tracker read grant revocation ignored")
			}
			if _, e = c.GetTrackerSync(f.ctx, publish.ID); e == nil {
				t.Fatal("revoked principal read sync")
			}
		})
	}
}
func TestTrackerUnknownWriteAndSignedInbox(t *testing.T) {
	for _, provider := range []string{"linear", "jira"} {
		t.Run(provider, func(t *testing.T) {
			f := newAccessFixture(t)
			config := trackerConfig(provider)
			if e := f.db.ApplyTrackerConfig(f.ctx, "synthetic-operator", config); e != nil {
				t.Fatal(e)
			}
			p, factory := newTrackerProvider(t, config)
			a, e := trackerworker.NewActivity(f.db, trackerCredentials, factory)
			if e != nil {
				t.Fatal(e)
			}
			c := trackerClient(t, f, "author")
			link := createTrackerLink(t, f, c, p, false)
			runTrackerActivity(t, f, a, link.LatestSyncID)
			request, e := c.RequestTrackerSync(f.ctx, link.ID, "unknown-write", domain.TrackerSyncInput{LinkDigest: link.Digest, Mode: "publish"})
			if e != nil {
				t.Fatal(e)
			}
			p.mu.Lock()
			p.dropBeforeWrite = true
			p.mu.Unlock()
			runTrackerActivity(t, f, a, request.ID)
			runTrackerActivity(t, f, a, request.ID)
			got, e := c.GetTrackerSync(f.ctx, request.ID)
			if e != nil || got.Observation.State != "unknown" {
				t.Fatalf("uncertain write was hidden: %+v %v", got, e)
			}
			p.mu.Lock()
			if p.writes != 1 {
				t.Error("uncertain write was repeated")
			}
			p.mu.Unlock()
			handler := trackerworker.WebhookHandler(f.db, trackerCredentials)
			payload := map[string]any{"type": "Issue", "organizationId": config.OrganizationID, "webhookTimestamp": time.Now().UnixMilli(), "data": map[string]string{"id": p.issue, "title": "Forged payload title is not authoritative"}}
			if provider == "jira" {
				payload = map[string]any{"webhookEvent": "jira:issue_updated", "timestamp": time.Now().UnixMilli(), "issue": map[string]any{"id": p.issue, "fields": map[string]any{"project": map[string]string{"id": config.ScopeID}, "summary": "Forged payload title is not authoritative"}}}
			}
			body, _ := json.Marshal(payload)
			mac := hmac.New(sha256.New, []byte(trackerToken))
			_, _ = mac.Write(body)
			signature := hex.EncodeToString(mac.Sum(nil))
			header := "Linear-Signature"
			if provider == "jira" {
				header = "X-Hub-Signature"
				signature = "sha256=" + signature
			}
			for _, sig := range []string{signature, signature, "forged"} {
				r := httptest.NewRequest("POST", "/webhooks/tracker/team", strings.NewReader(string(body)))
				r.Header.Set(header, sig)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				want := 200
				if sig == "forged" {
					want = 401
				}
				if w.Code != want {
					t.Fatalf("webhook %d want%d", w.Code, want)
				}
			}
			var inbox, syncs int
			if e = f.sql.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM tracker_inbox),(SELECT count(*) FROM tracker_syncs WHERE idempotency_key LIKE 'webhook-%')`).Scan(&inbox, &syncs); e != nil || inbox != 1 || syncs != 1 {
				t.Fatalf("signed inbox dedup: %d %d %v", inbox, syncs, e)
			}
			link, e = c.GetTrackerLink(f.ctx, link.ID)
			if e != nil {
				t.Fatal(e)
			}
			runTrackerActivity(t, f, a, link.LatestSyncID)
			link, e = c.GetTrackerLink(f.ctx, link.ID)
			if e != nil || link.Observation.Issue.Title != p.title {
				t.Fatalf("event body replaced authoritative record: %+v %v", link.Observation, e)
			}
			p.mu.Lock()
			if p.writes != 1 {
				t.Error("incoming event caused echo write")
			}
			p.mu.Unlock()
			var auditCount int
			if e = f.sql.QueryRow(f.ctx, `SELECT count(*) FROM tracker_audit`).Scan(&auditCount); e != nil || auditCount < 4 {
				t.Fatalf("missing tracker audit: %d %v", auditCount, e)
			}
		})
	}
}

func TestTrackerTemporalWorkflowAndRetainedReceipt(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_TEMPORAL") != "1" {
		t.Skip("set CONDUCTOR_TEST_TEMPORAL=1 for owned tracker runtime acceptance")
	}
	for _, provider := range []string{"linear", "jira"} {
		t.Run(provider, func(t *testing.T) {
			f := newAccessFixture(t)
			config := trackerConfig(provider)
			if e := f.db.ApplyTrackerConfig(f.ctx, "synthetic-operator", config); e != nil {
				t.Fatal(e)
			}
			p, factory := newTrackerProvider(t, config)
			a, e := trackerworker.NewActivity(f.db, trackerCredentials, factory)
			if e != nil {
				t.Fatal(e)
			}
			c := trackerClient(t, f, "author")
			link := createTrackerLink(t, f, c, p, false)
			directory := t.TempDir()
			address := durableAddress(t)
			process := durableStartTemporal(t, directory, address, "tracker-before-restart")
			defer process.stop()
			engine, e := durableEngine(f.ctx, address)
			if e != nil {
				t.Fatal(e)
			}
			defer engine.Close()
			workerCtx, stopWorker := context.WithCancel(f.ctx)
			done := make(chan error, 1)
			go func() { done <- trackerworkflow.RunWorker(workerCtx, engine, a.Sync) }()
			dispatcher, e := trackerworker.NewDispatcher(f.ctx, f.db, engine, address, durableNamespace)
			if e != nil {
				t.Fatal(e)
			}
			if handled, e := dispatcher.Step(f.ctx); e != nil || !handled {
				t.Fatalf("tracker dispatch: %v %v", handled, e)
			}
			var result trackerworkflow.Result
			if e = engine.GetWorkflow(f.ctx, trackerworkflow.WorkflowName+"/"+link.LatestSyncID, "").Get(f.ctx, &result); e != nil {
				t.Fatal(e)
			}
			got, e := c.GetTrackerSync(f.ctx, link.LatestSyncID)
			if e != nil || got.Observation == nil || got.Observation.State != "refreshed" {
				t.Fatalf("actual tracker workflow receipt: %+v %v", got, e)
			}
			history := engine.GetWorkflowHistory(f.ctx, trackerworkflow.WorkflowName+"/"+link.LatestSyncID, "", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
			events := 0
			for history.HasNext() {
				event, e := history.Next()
				if e != nil {
					t.Fatal(e)
				}
				events++
				if events > 256 {
					t.Fatal("tracker history bound exceeded")
				}
				trackerHistoryPrivate(t, event.ProtoReflect())
			}
			stopWorker()
			if e = <-done; e != nil {
				t.Fatal(e)
			}
			engine.Close()
			process.stop()
			process = durableStartTemporal(t, directory, address, "tracker-after-restart")
			defer process.stop()
			recovered, e := durableEngine(f.ctx, address)
			if e != nil {
				t.Fatal(e)
			}
			defer recovered.Close()
			var after trackerworkflow.Result
			if e = recovered.GetWorkflow(f.ctx, trackerworkflow.WorkflowName+"/"+link.LatestSyncID, "").Get(f.ctx, &after); e != nil || after != result {
				t.Fatalf("owned Temporal restart lost tracker receipt: %+v %v", after, e)
			}
			// Database admission is independent of a retained completed workflow result.
			if _, e = f.sql.Exec(f.ctx, `UPDATE tracker_grants SET can_sync=false,can_resolve=false WHERE workspace_id='team' AND principal_id='person-author'`); e != nil {
				t.Fatal(e)
			}
			if replay := runTrackerActivity(t, f, a, link.LatestSyncID); replay != result {
				t.Fatal("receipt retry fetched new provider data after revocation")
			}
		})
	}
}
func trackerHistoryPrivate(t *testing.T, m protoreflect.Message) {
	t.Helper()
	m.Range(func(field protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		check := func(v protoreflect.Value) {
			switch field.Kind() {
			case protoreflect.MessageKind:
				trackerHistoryPrivate(t, v.Message())
			case protoreflect.StringKind, protoreflect.BytesKind:
				var data string
				if field.Kind() == protoreflect.BytesKind {
					data = string(v.Bytes())
				} else {
					data = v.String()
				}
				if strings.Contains(data, trackerToken) || strings.Contains(data, "Synthetic tracker source canary") || strings.Contains(data, "tracker-owned description") {
					t.Fatal("source or credential entered tracker workflow history")
				}
			}
		}
		if field.IsList() {
			for i := 0; i < v.List().Len(); i++ {
				check(v.List().Get(i))
			}
		} else if field.IsMap() {
			v.Map().Range(func(_ protoreflect.MapKey, item protoreflect.Value) bool {
				if field.MapValue().Kind() == protoreflect.MessageKind {
					trackerHistoryPrivate(t, item.Message())
				}
				return true
			})
		} else {
			check(v)
		}
		return true
	})
}
func TestTrackerAuditFailureAndCurrentAuthority(t *testing.T) {
	f := newAccessFixture(t)
	config := trackerConfig("linear")
	if e := f.db.ApplyTrackerConfig(f.ctx, "synthetic-operator", config); e != nil {
		t.Fatal(e)
	}
	p, factory := newTrackerProvider(t, config)
	a, e := trackerworker.NewActivity(f.db, trackerCredentials, factory)
	if e != nil {
		t.Fatal(e)
	}
	c := trackerClient(t, f, "author")
	pkg := f.create("author", "team", "application", domain.Content{"intent": "Audit rollback"})
	in := domain.TrackerLinkInput{IssueID: p.issue, Packages: []domain.TrackerPackageRef{{RepositoryID: "application", PackageID: pkg.ID, Revision: 1, Digest: pkg.Revision.Digest}}}
	if _, e = f.sql.Exec(f.ctx, `CREATE FUNCTION reject_tracker_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic tracker audit rejection'; END $$; CREATE TRIGGER reject_tracker_audit BEFORE INSERT ON tracker_audit FOR EACH ROW EXECUTE FUNCTION reject_tracker_audit()`); e != nil {
		t.Fatal(e)
	}
	if _, e = c.CreateTrackerLink(f.ctx, "audit-rejection", in); e == nil {
		t.Fatal("audit failure reported successful link")
	}
	var count int
	if e = f.sql.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM tracker_links)+(SELECT count(*) FROM tracker_syncs)+(SELECT count(*) FROM tracker_sync_outbox)`).Scan(&count); e != nil || count != 0 {
		t.Fatalf("partial tracker link/outbox survived rollback: %d %v", count, e)
	}
	if _, e = f.sql.Exec(f.ctx, `DROP TRIGGER reject_tracker_audit ON tracker_audit`); e != nil {
		t.Fatal(e)
	}
	link, e := c.CreateTrackerLink(f.ctx, "audit-rejection", in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.sql.Exec(f.ctx, `UPDATE repository_grants SET can_author=false WHERE repository_id='application' AND principal_id='person-author'`); e != nil {
		t.Fatal(e)
	}
	runTrackerActivity(t, f, a, link.LatestSyncID)
	p.mu.Lock()
	if p.reads != 0 || p.writes != 0 {
		t.Fatal("revoked author caused provider I/O")
	}
	p.mu.Unlock()
	got, e := c.GetTrackerSync(f.ctx, link.LatestSyncID)
	if e != nil || got.Observation.State != "unavailable" {
		t.Fatalf("revocation failure was hidden: %+v %v", got, e)
	}
}

func TestTrackerDelayedProviderSnapshotRemainsExplicitlyStale(t *testing.T) {
	f := newAccessFixture(t)
	config := trackerConfig("linear")
	if err := f.db.ApplyTrackerConfig(f.ctx, "synthetic-operator", config); err != nil {
		t.Fatal(err)
	}
	provider, factory := newTrackerProvider(t, config)
	c := trackerClient(t, f, "author")
	link := createTrackerLink(t, f, c, provider, false)
	activity, err := trackerworker.NewActivity(f.db, trackerCredentials, factory)
	if err != nil {
		t.Fatal(err)
	}
	runTrackerActivity(t, f, activity, link.LatestSyncID)
	link, err = c.GetTrackerLink(f.ctx, link.ID)
	if err != nil {
		t.Fatal(err)
	}
	sync, err := c.RequestTrackerSync(f.ctx, link.ID, "delayed-provider-snapshot", domain.TrackerSyncInput{LinkDigest: link.Digest, Mode: "refresh"})
	if err != nil {
		t.Fatal(err)
	}
	var binding string
	if err = f.sql.QueryRow(f.ctx, `SELECT binding FROM tracker_syncs WHERE id=$1`, sync.ID).Scan(&binding); err != nil {
		t.Fatal(err)
	}
	stale := *link.Observation
	issue := *stale.Issue
	issue.UpdatedAt = issue.UpdatedAt.Add(-time.Hour)
	issue.Title = "Older synthetic snapshot"
	stale.Issue = &issue
	receipt, err := f.db.CompleteTrackerSync(f.ctx, sync.ID, binding, stale)
	if err != nil {
		t.Fatal(err)
	}
	link, err = c.GetTrackerLink(f.ctx, link.ID)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.State != "conflict" || receipt.Code != "provider_snapshot_older" || link.Observation.Current {
		t.Fatal("delayed provider read looked current")
	}
	if _, err = c.RequestTrackerSync(f.ctx, link.ID, "cannot-publish-stale", domain.TrackerSyncInput{LinkDigest: link.Digest, Mode: "publish"}); err == nil {
		t.Fatal("stale projection enabled provider write")
	}
}
