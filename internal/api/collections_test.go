package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

type collectionCall struct {
	operation, id, key, before, collectionID, digest string
	input                                            domain.CollectionInput
	limit                                            int
	expected                                         int64
	access                                           domain.AccessRequest
}

type collectionTransportService struct {
	queryGuardService
	calls []collectionCall
	page  domain.CollectionPage
	err   error
}

func (s *collectionTransportService) record(ctx context.Context, call collectionCall) {
	call.access, _ = domain.AccessFromContext(ctx)
	s.calls = append(s.calls, call)
}
func (s *collectionTransportService) CreateCollection(ctx context.Context, key string, input domain.CollectionInput) (domain.Collection, error) {
	s.record(ctx, collectionCall{operation: "create", key: key, input: input})
	return domain.Collection{}, s.err
}
func (s *collectionTransportService) GetCollection(ctx context.Context, id string) (domain.Collection, error) {
	s.record(ctx, collectionCall{operation: "get", id: id})
	return domain.Collection{}, s.err
}
func (s *collectionTransportService) ListCollections(ctx context.Context, before string, limit int) (domain.CollectionPage, error) {
	s.record(ctx, collectionCall{operation: "list", before: before, limit: limit})
	return s.page, s.err
}
func (s *collectionTransportService) CancelCollection(ctx context.Context, id string) (domain.Collection, error) {
	s.record(ctx, collectionCall{operation: "cancel", id: id})
	return domain.Collection{}, s.err
}
func (s *collectionTransportService) AttachCollection(ctx context.Context, id string, expected int64, collectionID, digest string) (domain.Package, error) {
	s.record(ctx, collectionCall{operation: "attach", id: id, expected: expected, collectionID: collectionID, digest: digest})
	return domain.Package{}, s.err
}

func collectionRequest(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer synthetic.token")
	r.Header.Set("X-Conductor-Workspace", "team")
	r.Header.Set("X-Conductor-Repository", "application")
	r.Header.Set("Idempotency-Key", "synthetic-request-1")
	return r
}

func TestCollectionCommandsCarryOnlyInspectedInputsAndVerifiedScope(t *testing.T) {
	commit, digest := strings.Repeat("a", 40), strings.Repeat("b", 64)
	for _, tc := range []struct {
		method, path, body string
		status             int
		call               collectionCall
	}{
		{"POST", "/api/v1/context-collections", `{"commit":"` + commit + `","paths":["docs/design.md","file,with,commas.md"]}`, 202,
			collectionCall{operation: "create", key: "synthetic-request-1", input: domain.CollectionInput{Commit: commit, Paths: []string{"docs/design.md", "file,with,commas.md"}}}},
		{"GET", "/api/v1/context-collections", "", 200, collectionCall{operation: "list", limit: 20}},
		{"GET", "/api/v1/context-collections?before=opaque-cursor&limit=7", "", 200, collectionCall{operation: "list", before: "opaque-cursor", limit: 7}},
		{"GET", "/api/v1/context-collections/COL-inspected", "", 200, collectionCall{operation: "get", id: "COL-inspected"}},
		{"POST", "/api/v1/context-collections/COL-inspected/cancellation", `{}`, 202, collectionCall{operation: "cancel", id: "COL-inspected"}},
		{"POST", "/api/v1/changes/CHG-inspected/context-attachments", `{"expectedRevision":7,"collectionId":"COL-inspected","digest":"` + digest + `"}`, 201,
			collectionCall{operation: "attach", id: "CHG-inspected", expected: 7, collectionID: "COL-inspected", digest: digest}},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			s := &collectionTransportService{}
			w := httptest.NewRecorder()
			NewAuthenticated(s, queryGuardVerifier{}).ServeHTTP(w, collectionRequest(tc.method, tc.path, tc.body))
			tc.call.access = domain.AccessRequest{Identity: domain.AccessIdentity{Issuer: "https://identity.example.test", Subject: "synthetic-reviewer"}, WorkspaceID: "team", RepositoryID: "application"}
			if w.Code != tc.status || !reflect.DeepEqual(s.calls, []collectionCall{tc.call}) || s.queryGuardService.calls != 0 {
				t.Fatalf("status=%d calls=%+v package calls=%d body=%s", w.Code, s.calls, s.queryGuardService.calls, w.Body)
			}
		})
	}
}

func TestCollectionTransportRejectsAmbiguousInputsBeforeService(t *testing.T) {
	valid := `{"commit":"` + strings.Repeat("a", 40) + `","paths":["README.md"]}`
	for _, tc := range []struct {
		name, method, path, body string
		change                   func(*http.Request)
	}{
		{"missing key", "POST", "/api/v1/context-collections", valid, func(r *http.Request) { r.Header.Del("Idempotency-Key") }},
		{"duplicate key", "POST", "/api/v1/context-collections", valid, func(r *http.Request) { r.Header.Add("Idempotency-Key", "other") }},
		{"combined key", "POST", "/api/v1/context-collections", valid, func(r *http.Request) { r.Header.Set("Idempotency-Key", "one,two") }},
		{"oversized key", "POST", "/api/v1/context-collections", valid, func(r *http.Request) { r.Header.Set("Idempotency-Key", strings.Repeat("a", 129)) }},
		{"trimmed key", "POST", "/api/v1/context-collections", valid, func(r *http.Request) { r.Header.Set("Idempotency-Key", " key ") }},
		{"forged actor", "POST", "/api/v1/context-collections", `{"commit":"a","paths":[],"actor":"operator"}`, nil},
		{"caller url", "POST", "/api/v1/context-collections", `{"commit":"a","paths":[],"url":"https://other.invalid"}`, nil},
		{"duplicate commit", "POST", "/api/v1/context-collections", `{"commit":"a","commit":"b","paths":["README.md"]}`, nil},
		{"duplicate escaped field", "POST", "/api/v1/context-collections", `{"commit":"a","comm\u0069t":"b","paths":["README.md"]}`, nil},
		{"case alias", "POST", "/api/v1/context-collections", `{"Commit":"a","paths":["README.md"]}`, nil},
		{"duplicate case alias", "POST", "/api/v1/context-collections", `{"commit":"a","COMMIT":"b","paths":["README.md"]}`, nil},
		{"invalid UTF-8 path", "POST", "/api/v1/context-collections", "{\"commit\":\"a\",\"paths\":[\"a\xff.md\"]}", nil},
		{"duplicate paths", "POST", "/api/v1/context-collections", `{"commit":"a","paths":["README.md"],"paths":["other.md"]}`, nil},
		{"null create", "POST", "/api/v1/context-collections", `null`, nil},
		{"multiple objects", "POST", "/api/v1/context-collections", valid + `{}`, nil},
		{"create query", "POST", "/api/v1/context-collections?repositoryId=other", valid, nil},
		{"inspection query", "GET", "/api/v1/context-collections/COL-inspected?refresh=true", "", nil},
		{"cancel unknown field", "POST", "/api/v1/context-collections/COL-inspected/cancellation", `{"force":true}`, nil},
		{"cancel null", "POST", "/api/v1/context-collections/COL-inspected/cancellation", `null`, nil},
		{"cancel query", "POST", "/api/v1/context-collections/COL-inspected/cancellation?force=true", `{}`, nil},
		{"attach actor", "POST", "/api/v1/changes/CHG-inspected/context-attachments", `{"expectedRevision":7,"collectionId":"COL-inspected","digest":"b","actor":"operator"}`, nil},
		{"duplicate inspected revision", "POST", "/api/v1/changes/CHG-inspected/context-attachments", `{"expectedRevision":7,"expectedRevision":99,"collectionId":"COL-inspected","digest":"b"}`, nil},
		{"attach null", "POST", "/api/v1/changes/CHG-inspected/context-attachments", `null`, nil},
		{"attach query", "POST", "/api/v1/changes/CHG-inspected/context-attachments?revision=99", `{}`, nil},
		{"unknown list query", "GET", "/api/v1/context-collections?provider=github", "", nil},
		{"duplicate cursor", "GET", "/api/v1/context-collections?before=one&before=two", "", nil},
		{"malformed query", "GET", "/api/v1/context-collections?before=%ZZ", "", nil},
		{"zero limit", "GET", "/api/v1/context-collections?limit=0", "", nil},
		{"oversized limit", "GET", "/api/v1/context-collections?limit=101", "", nil},
		{"bad limit", "GET", "/api/v1/context-collections?limit=no", "", nil},
		{"oversized cursor", "GET", "/api/v1/context-collections?before=" + strings.Repeat("a", 1025), "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &collectionTransportService{}
			r := collectionRequest(tc.method, tc.path, tc.body)
			if tc.change != nil {
				tc.change(r)
			}
			w := httptest.NewRecorder()
			NewAuthenticated(s, queryGuardVerifier{}).ServeHTTP(w, r)
			if w.Code != 400 || len(s.calls) != 0 {
				t.Fatalf("invalid request reached service: status=%d calls=%+v", w.Code, s.calls)
			}
		})
	}
}

func TestCollectionAuthenticationAndUnavailableModes(t *testing.T) {
	for _, operation := range []struct{ method, path, body string }{
		{"POST", "/api/v1/context-collections", `{}`},
		{"GET", "/api/v1/context-collections", ""},
		{"GET", "/api/v1/context-collections/COL-inspected", ""},
		{"POST", "/api/v1/context-collections/COL-inspected/cancellation", `{}`},
		{"POST", "/api/v1/changes/CHG-inspected/context-attachments", `{}`},
	} {
		for _, mode := range []string{"local", "unimplemented", "missing token", "mixed actor", "ambiguous scope"} {
			t.Run(mode+operation.path, func(t *testing.T) {
				s := &collectionTransportService{}
				var handler http.Handler = NewAuthenticated(s, queryGuardVerifier{})
				r := collectionRequest(operation.method, operation.path, operation.body)
				status := 401
				switch mode {
				case "local":
					handler, status = New(s), 503
					r.Header.Del("Authorization")
					r.Header.Set("X-Conductor-Actor", "operator")
				case "unimplemented":
					handler, status = NewAuthenticated(&queryGuardService{}, queryGuardVerifier{}), 503
				case "missing token":
					r.Header.Del("Authorization")
				case "mixed actor":
					r.Header.Set("X-Conductor-Actor", "operator")
				case "ambiguous scope":
					r.Header.Add("X-Conductor-Repository", "other")
					status = 400
				}
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				if w.Code != status || len(s.calls) != 0 {
					t.Fatalf("status=%d want=%d calls=%+v", w.Code, status, s.calls)
				}
			})
		}
	}
}

func TestCollectionFailuresRetainStableCodes(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{domain.ErrIdempotencyConflict, 409, "idempotency_conflict"},
		{domain.ErrCollectionStopped, 409, "collection_stopped"},
		{domain.ErrCapacity, 429, "capacity_exceeded"},
		{domain.ErrUnavailable, 503, "service_unavailable"},
		{domain.ErrForbidden, 403, "permission_denied"},
		{domain.ErrNotFound, 404, "not_found"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			s := &collectionTransportService{err: tc.err}
			w := httptest.NewRecorder()
			NewAuthenticated(s, queryGuardVerifier{}).ServeHTTP(w, collectionRequest("GET", "/api/v1/context-collections/COL-inspected", ""))
			var envelope struct {
				Error struct{ Code, CorrelationID string }
			}
			if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.status || envelope.Error.Code != tc.code || envelope.Error.CorrelationID == "" {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
		})
	}
}

type collectionCookieStore struct{ browserStore }

func (collectionCookieStore) BrowserSession(context.Context, string) (domain.BrowserSession, error) {
	return domain.BrowserSession{Identity: domain.AccessIdentity{Issuer: "https://identity.example.test", Subject: "synthetic-reviewer"}, CSRFToken: strings.Repeat("A", 43), ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func TestCollectionCookieCommandsRequireOriginAndSessionCSRF(t *testing.T) {
	for _, operation := range []struct{ path, body string }{
		{"/api/v1/context-collections", `{"commit":"` + strings.Repeat("a", 40) + `","paths":["README.md"]}`},
		{"/api/v1/context-collections/COL-inspected/cancellation", `{}`},
		{"/api/v1/changes/CHG-inspected/context-attachments", `{"expectedRevision":7,"collectionId":"COL-inspected","digest":"` + strings.Repeat("a", 64) + `"}`},
	} {
		for _, mode := range []string{"missing csrf", "wrong origin", "valid"} {
			t.Run(mode+operation.path, func(t *testing.T) {
				s := &collectionTransportService{}
				handler := sharedHandler(s, queryGuardVerifier{}, &browserAuth{store: collectionCookieStore{}, origin: "https://conductor.example.test", host: "conductor.example.test"})
				r := collectionRequest("POST", "https://conductor.example.test"+operation.path, operation.body)
				r.Header.Del("Authorization")
				r.AddCookie(&http.Cookie{Name: sessionCookie, Value: strings.Repeat("A", 43)})
				r.Header.Set("Origin", "https://conductor.example.test")
				r.Header.Set("X-Conductor-CSRF", strings.Repeat("A", 43))
				if mode == "missing csrf" {
					r.Header.Del("X-Conductor-CSRF")
				} else if mode == "wrong origin" {
					r.Header.Set("Origin", "https://other.example.test")
				}
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				if mode == "valid" {
					if w.Code >= 300 || len(s.calls) != 1 {
						t.Fatalf("valid cookie command rejected: %d %s", w.Code, w.Body)
					}
				} else if w.Code != 403 || len(s.calls) != 0 {
					t.Fatalf("unsafe cookie command reached service: status=%d calls=%+v", w.Code, s.calls)
				}
			})
		}
	}
}

func TestCollectionListTransportsExecutionNamespaceAndUnavailableObservation(t *testing.T) {
	s := &collectionTransportService{page: domain.CollectionPage{Collections: []domain.CollectionSummary{{
		ID: strings.Repeat("a", 32), WorkspaceID: "team", RepositoryID: "application", RequesterID: "engineer", Commit: strings.Repeat("b", 40),
		Execution: &domain.CollectionExecution{Namespace: "synthetic-namespace", WorkflowID: "context/recorded-id", RunID: "recorded-run", State: "unavailable", ObservedAt: time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC), Current: false},
	}}}}
	w := httptest.NewRecorder()
	NewAuthenticated(s, queryGuardVerifier{}).ServeHTTP(w, collectionRequest("GET", "/api/v1/context-collections", ""))
	var response struct {
		Collections []struct {
			Execution struct {
				Namespace, WorkflowID, RunID, State string
				ObservedAt                          time.Time
				Current                             bool
			}
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 200 || len(response.Collections) != 1 {
		t.Fatalf("status=%d body=%s error=%v", w.Code, w.Body, err)
	}
	observation := response.Collections[0].Execution
	if observation.Namespace != "synthetic-namespace" || observation.WorkflowID != "context/recorded-id" || observation.RunID != "recorded-run" ||
		observation.State != "unavailable" || observation.ObservedAt.IsZero() || observation.Current {
		t.Fatalf("execution observation was lost or promoted to live progress: %+v", observation)
	}
}
