package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

type historyStub struct {
	reviewService
	operation string
	id        string
	cursor    int64
	limit     int
	calls     int
	err       error
}

func (s *historyStub) History(_ context.Context, id string, beforeRevision int64, limit int) (domain.HistoryPage, error) {
	s.operation, s.id, s.cursor, s.limit = "history", id, beforeRevision, limit
	s.calls++
	return domain.HistoryPage{Revisions: []domain.RevisionSummary{{Number: 3, Digest: strings.Repeat("a", 64), Author: "author", ApprovalCount: 1}}, NextBeforeRevision: 3}, s.err
}

func (s *historyStub) Revision(_ context.Context, id string, revision int64) (domain.RevisionRecord, error) {
	s.operation, s.id, s.cursor = "revision", id, revision
	s.calls++
	return domain.RevisionRecord{
		Revision:           domain.Revision{ChangeID: id, Number: revision, Content: domain.Content{"futureField": map[string]any{"preserved": true}}},
		Approvals:          []domain.Approval{{ChangeID: id, Revision: revision, Reviewer: "reviewer", CreatedAt: time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)}},
		ApprovalsTruncated: true,
	}, s.err
}

func (s *historyStub) Events(_ context.Context, id string, afterSequence int64, limit int) (domain.AuditPage, error) {
	s.operation, s.id, s.cursor, s.limit = "events", id, afterSequence, limit
	s.calls++
	return domain.AuditPage{Events: []domain.AuditEvent{{Sequence: 7, ChangeID: id, EventType: "review.approved", Actor: "reviewer", Revision: 1, Data: map[string]any{"digest": strings.Repeat("a", 64)}}}, NextAfterSequence: 7}, s.err
}

func TestHistoryRoutesAndPaginationInputs(t *testing.T) {
	for _, tc := range []struct {
		path, operation string
		cursor          int64
		limit           int
	}{
		{"history", "history", 0, 20},
		{"history?beforeRevision=9&limit=100", "history", 9, 100},
		{"events", "events", 0, 20},
		{"events?afterSequence=9223372036854775807&limit=1", "events", 9223372036854775807, 1},
		{"revisions/2", "revision", 2, 0},
	} {
		t.Run(tc.path, func(t *testing.T) {
			s := &historyStub{}
			res := perform(t, New(s), http.MethodGet, "/api/v1/changes/CHG-test/"+tc.path, "reviewer", nil)
			if res.Code != http.StatusOK || s.calls != 1 || s.operation != tc.operation || s.id != "CHG-test" || s.cursor != tc.cursor || s.limit != tc.limit {
				t.Fatalf("route %s: code=%d body=%s service=%+v", tc.path, res.Code, res.Body.String(), s)
			}
			var body map[string]any
			if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			switch tc.operation {
			case "history":
				if body["nextBeforeRevision"] != float64(3) || len(body["revisions"].([]any)) != 1 {
					t.Fatalf("history payload: %v", body)
				}
			case "events":
				if body["nextAfterSequence"] != float64(7) || len(body["events"].([]any)) != 1 {
					t.Fatalf("events payload: %v", body)
				}
			case "revision":
				if _, present := body["approved"]; present {
					t.Fatal("historical record exposed effective approval")
				}
				if body["approvalsTruncated"] != true || len(body["approvals"].([]any)) != 1 {
					t.Fatalf("historical approvals payload: %v", body)
				}
				content := body["revision"].(map[string]any)["content"]
				if !reflect.DeepEqual(content, map[string]any{"futureField": map[string]any{"preserved": true}}) {
					t.Fatalf("historical content lost unknown fields: %v", content)
				}
			}
		})
	}
}

func TestHistoryRequiresAuthenticationAndReportsUnavailable(t *testing.T) {
	for _, path := range []string{"history", "revisions/1", "events"} {
		s := &historyStub{}
		res := perform(t, New(s), http.MethodGet, "/api/v1/changes/CHG-test/"+path, "", nil)
		if res.Code != http.StatusUnauthorized || s.calls != 0 {
			t.Fatalf("unauthenticated %s: %d calls=%d", path, res.Code, s.calls)
		}
		unavailable := perform(t, New(&reviewService{}), http.MethodGet, "/api/v1/changes/CHG-test/"+path, "reviewer", nil)
		if unavailable.Code != http.StatusServiceUnavailable || !strings.Contains(unavailable.Body.String(), `"code":"service_unavailable"`) {
			t.Fatalf("unavailable %s: %d %s", path, unavailable.Code, unavailable.Body.String())
		}
	}
}

func TestHistoryRejectsMalformedParametersBeforeCallingService(t *testing.T) {
	paths := []string{
		"revisions/0", "revisions/-1", "revisions/one", "revisions/9223372036854775808", "revisions/1?limit=20",
	}
	for _, route := range []struct{ path, cursor string }{{"history", "beforeRevision"}, {"events", "afterSequence"}} {
		for _, query := range []string{
			"limit=0", "limit=-1", "limit=101", "limit=one", "limit=", "limit=20&limit=20", "limit=9223372036854775808",
			route.cursor + "=-1", route.cursor + "=one", route.cursor + "=", route.cursor + "=9223372036854775808",
			route.cursor + "=1&" + route.cursor + "=2", "unknown=1", "limit=1;unexpected=1", "limit=%zz",
		} {
			paths = append(paths, route.path+"?"+query)
		}
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			s := &historyStub{}
			res := perform(t, New(s), http.MethodGet, "/api/v1/changes/CHG-test/"+path, "reviewer", nil)
			if res.Code != http.StatusBadRequest || s.calls != 0 || !strings.Contains(res.Body.String(), `"code":"invalid_input"`) {
				t.Fatalf("invalid parameters reached service: status=%d calls=%d body=%s", res.Code, s.calls, res.Body.String())
			}
		})
	}
}

func TestHistoryTypedAndUnexpectedErrors(t *testing.T) {
	for _, path := range []string{"history", "revisions/999", "events"} {
		for _, tc := range []struct {
			err    error
			status int
			code   string
		}{
			{domain.ErrNotFound, http.StatusNotFound, "not_found"},
			{domain.ErrInvalidInput, http.StatusBadRequest, "invalid_input"},
			{domain.ErrUnavailable, http.StatusServiceUnavailable, "service_unavailable"},
			{errors.New("synthetic database connection detail"), http.StatusInternalServerError, "internal_error"},
		} {
			s := &historyStub{err: tc.err}
			res := perform(t, New(s), http.MethodGet, "/api/v1/changes/CHG-test/"+path, "reviewer", nil)
			var envelope struct {
				Error struct {
					Code, Message, CorrelationID string
				}
			}
			if err := json.Unmarshal(res.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if res.Code != tc.status || envelope.Error.Code != tc.code || envelope.Error.CorrelationID != res.Header().Get("X-Correlation-ID") {
				t.Fatalf("%s error response: %d %s", path, res.Code, res.Body.String())
			}
			if tc.status == http.StatusInternalServerError && envelope.Error.Message != "internal server error" {
				t.Fatalf("internal details exposed: %s", res.Body.String())
			}
		}
	}
}
