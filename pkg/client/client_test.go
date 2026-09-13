package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestReturnsTypedAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Conductor-Actor") != "reviewer" {
			t.Error("actor header missing")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"revision_conflict","message":"stale","correlationId":"request-1"}}`))
	}))
	defer server.Close()

	_, err := New(server.URL, "reviewer").Approve(context.Background(), "CHG-test", 1, "digest")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %v", err)
	}
	if apiErr.StatusCode != http.StatusConflict || apiErr.Code != "revision_conflict" || apiErr.CorrelationID != "request-1" {
		t.Fatalf("unexpected error: %#v", apiErr)
	}
}

func TestRedirectNeverReplaysOrRefreshesCommand(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var destinationCalls atomic.Int32
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				destinationCalls.Add(1)
				_, _ = w.Write([]byte(`{"approved":true}`))
			}))
			defer destination.Close()
			var commandCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				commandCalls.Add(1)
				w.Header().Set("X-Correlation-ID", "redirected-command")
				http.Redirect(w, r, destination.URL, status)
			}))
			defer server.Close()
			c := New(server.URL, "reviewer")
			// Custom clients must not weaken the single-command boundary.
			c.HTTP = server.Client()
			_, err := c.Approve(context.Background(), "CHG-test", 1, "inspected-digest")
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != status || apiErr.Code != "unexpected_redirect" || apiErr.CorrelationID != "redirected-command" {
				t.Fatalf("redirect should be an explicit error, got %v", err)
			}
			if commandCalls.Load() != 1 || destinationCalls.Load() != 0 {
				t.Fatalf("command followed redirect: source=%d destination=%d", commandCalls.Load(), destinationCalls.Load())
			}
			if c.HTTP.CheckRedirect != nil {
				t.Fatal("request mutated the caller's HTTP client")
			}
		})
	}
}

func TestHistoryAndRevisionResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/changes/CHG-test/history":
			if r.URL.Query().Get("beforeRevision") != "5" || r.URL.Query().Get("limit") != "2" {
				t.Errorf("wrong history cursor: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"revisions":[{"number":4,"digest":"digest","author":"author","approvalCount":1}],"nextBeforeRevision":4}`))
		case "/api/v1/changes/CHG-test/revisions/4":
			_, _ = w.Write([]byte(`{"revision":{"number":4,"content":{"future":{"items":["preserved"]}}},"approvals":[{"revision":4,"reviewer":"reviewer"}],"approvalsTruncated":false}`))
		case "/api/v1/changes/CHG-test/events":
			if r.URL.Query().Get("afterSequence") != "7" || r.URL.Query().Get("limit") != "2" {
				t.Errorf("wrong event cursor: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"events":[{"sequence":8,"eventType":"review.approved"}],"nextAfterSequence":8}`))
		default:
			t.Errorf("unexpected request: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	c := New(server.URL, "reviewer")
	history, err := c.History(context.Background(), "CHG-test", 5, 2)
	if err != nil || len(history.Revisions) != 1 || history.NextBeforeRevision != 4 || history.Revisions[0].ApprovalCount != 1 {
		t.Fatalf("history: %+v, %v", history, err)
	}
	record, err := c.Revision(context.Background(), "CHG-test", 4)
	if err != nil || record.Revision.Number != 4 || len(record.Approvals) != 1 || record.Revision.Content["future"] == nil {
		t.Fatalf("revision: %+v, %v", record, err)
	}
	events, err := c.Events(context.Background(), "CHG-test", 7, 2)
	if err != nil || len(events.Events) != 1 || events.NextAfterSequence != 8 {
		t.Fatalf("events: %+v, %v", events, err)
	}
}

func TestRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat(" ", (2<<20)+1)))
	}))
	defer server.Close()
	_, err := New(server.URL, "reviewer").Get(context.Background(), "CHG-test")
	if err == nil || !strings.Contains(err.Error(), "exceeds 2 MiB") {
		t.Fatalf("oversized response: %v", err)
	}
}

func TestEscapesChangeIDAsOnePathSegment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/api/v1/changes/team%2Fchange" {
			t.Errorf("unexpected path %q", r.URL.EscapedPath())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"team/change","approved":false,"revision":{"number":1}}`))
	}))
	defer server.Close()
	if _, err := New(server.URL, "reviewer").Get(context.Background(), "team/change"); err != nil {
		t.Fatal(err)
	}
}
