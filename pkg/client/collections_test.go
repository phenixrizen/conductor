package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/domain"
)

func TestCollectionClientUsesFixedScopeAndOneExactRequest(t *testing.T) {
	commit, digest := strings.Repeat("a", 40), strings.Repeat("b", 64)
	for _, tc := range []struct {
		name, method, path, key string
		body                    any
		call                    func(context.Context, *Client) error
	}{
		{"create", "POST", "/api/v1/context-collections", "stable-request-1", map[string]any{"commit": commit, "paths": []any{"README.md", "file,with,comma.md"}}, func(ctx context.Context, c *Client) error {
			_, err := c.CreateCollection(ctx, "stable-request-1", domain.CollectionInput{Commit: commit, Paths: []string{"README.md", "file,with,comma.md"}})
			return err
		}},
		{"inspect escaped ID", "GET", "/api/v1/context-collections/COL%2Finspect%3Fvalue", "", nil, func(ctx context.Context, c *Client) error {
			_, err := c.GetCollection(ctx, "COL/inspect?value")
			return err
		}},
		{"list cursor", "GET", "/api/v1/context-collections?before=opaque%2Bcursor%3D&limit=7", "", nil, func(ctx context.Context, c *Client) error {
			_, err := c.ListCollections(ctx, "opaque+cursor=", 7)
			return err
		}},
		{"cancel", "POST", "/api/v1/context-collections/COL%2Finspect%3Fvalue/cancellation", "", map[string]any{}, func(ctx context.Context, c *Client) error {
			_, err := c.CancelCollection(ctx, "COL/inspect?value")
			return err
		}},
		{"attach inspected tuple", "POST", "/api/v1/changes/CHG%2Finspect%3Fvalue/context-attachments", "", map[string]any{"expectedRevision": float64(7), "collectionId": "COL-inspected", "digest": digest}, func(ctx context.Context, c *Client) error {
			_, err := c.AttachCollection(ctx, "CHG/inspect?value", 7, "COL-inspected", digest)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != tc.method || r.RequestURI != tc.path || r.Header.Get("Idempotency-Key") != tc.key ||
					r.Header.Get("Authorization") != "Bearer synthetic.token" || r.Header.Get("X-Conductor-Workspace") != "team" ||
					r.Header.Get("X-Conductor-Repository") != "application" || len(r.Header.Values("X-Conductor-Actor")) != 0 {
					t.Error("request changed its method, path, key, identity, or canonical scope")
				}
				if tc.key != "" && len(r.Header.Values("Idempotency-Key")) != 1 {
					t.Error("idempotency key was not single valued")
				}
				var body any
				if tc.body != nil {
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if !reflect.DeepEqual(body, tc.body) {
						t.Errorf("body=%#v want=%#v", body, tc.body)
					}
				}
				_, _ = w.Write([]byte(`{}`))
			}))
			defer server.Close()
			c, err := NewAuthenticated(server.URL, "synthetic.token", "team", "application")
			if err != nil {
				t.Fatal(err)
			}
			if err := tc.call(context.Background(), c); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("command made %d requests, including an unintended refresh or retry", calls)
			}
		})
	}
}

func TestCollectionCommandsRejectRedirectsWithoutRetryOrInspection(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		for _, command := range []string{"create", "cancel", "attach"} {
			t.Run(command+http.StatusText(status), func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					w.Header().Set("Location", "/unexpected-refresh")
					w.WriteHeader(status)
				}))
				defer server.Close()
				c, err := NewAuthenticated(server.URL, "synthetic.token", "team", "application")
				if err != nil {
					t.Fatal(err)
				}
				c.HTTP = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
					t.Error("caller-provided redirect policy was used")
					return nil
				}}
				switch command {
				case "create":
					_, err = c.CreateCollection(context.Background(), "request-1", domain.CollectionInput{Commit: strings.Repeat("a", 40), Paths: []string{"README.md"}})
				case "cancel":
					_, err = c.CancelCollection(context.Background(), "COL-inspected")
				case "attach":
					_, err = c.AttachCollection(context.Background(), "CHG-inspected", 7, "COL-inspected", strings.Repeat("b", 64))
				}
				var apiError *APIError
				if !errors.As(err, &apiError) || apiError.Code != "unexpected_redirect" || apiError.StatusCode != status || calls != 1 {
					t.Fatalf("error=%v requests=%d", err, calls)
				}
			})
		}
	}
}

func TestCollectionClientDecodesSharedReceiptAndExplicitProgress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"COL-shared","workspaceId":"team","repositoryId":"application","requesterId":"engineer-one","input":{"commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","paths":["README.md"]},"inputDigest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","receipt":{"id":"COL-shared","digest":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","snapshot":{"schemaVersion":2,"artifacts":[{"path":"README.md","state":"missing","message":"Not present at this commit"}]}},"execution":{"namespace":"synthetic-namespace","workflowId":"context-COL-shared","runId":"recorded-run","state":"completed","current":false}}`))
	}))
	defer server.Close()
	c, err := NewAuthenticated(server.URL, "synthetic.token", "team", "application")
	if err != nil {
		t.Fatal(err)
	}
	value, err := c.GetCollection(context.Background(), "COL-shared")
	if err != nil || value.Receipt == nil || value.Receipt.Snapshot.Artifacts[0].State != "missing" || value.Execution == nil || value.Execution.Current || value.Execution.State != "completed" || value.Execution.Namespace != "synthetic-namespace" {
		t.Fatalf("lost receipt coverage or observation freshness: %+v err=%v", value, err)
	}
}

func TestCollectionClientRetainsSummaryExecutionIdentityAndUnavailableProgress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"collections":[{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","workspaceId":"team","repositoryId":"application","requesterId":"engineer","commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","createdAt":"2026-09-13T12:00:00Z","execution":{"namespace":"synthetic-namespace","workflowId":"context/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","runId":"original-run","state":"unavailable","observedAt":"2026-09-13T12:01:00Z","current":false}},{"id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","workspaceId":"team","repositoryId":"application","requesterId":"engineer","commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","createdAt":"2026-09-13T11:00:00Z"}],"nextBefore":"opaque-cursor"}`))
	}))
	defer server.Close()
	c, err := NewAuthenticated(server.URL, "synthetic.token", "team", "application")
	if err != nil {
		t.Fatal(err)
	}
	page, err := c.ListCollections(context.Background(), "", 20)
	if err != nil || len(page.Collections) != 2 || page.NextBefore != "opaque-cursor" {
		t.Fatalf("page=%+v error=%v", page, err)
	}
	execution := page.Collections[0].Execution
	if execution == nil || execution.Namespace != "synthetic-namespace" || execution.RunID != "original-run" ||
		execution.State != "unavailable" || execution.Current || execution.ObservedAt.IsZero() || page.Collections[1].Execution != nil {
		t.Fatalf("summary lost unavailable/missing progress or execution identity: %+v", page.Collections)
	}
}
