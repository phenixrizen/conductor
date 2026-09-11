package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
