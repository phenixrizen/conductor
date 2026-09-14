package api

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/pkg/client"
)

type listStub struct {
	reviewService
	repository, before string
	limit, calls       int
	err                error
	page               domain.ChangePage
}

func (s *listStub) List(_ context.Context, repository, before string, limit int) (domain.ChangePage, error) {
	s.repository, s.before, s.limit = repository, before, limit
	s.calls++
	if s.page.Changes != nil {
		return s.page, s.err
	}
	return domain.ChangePage{Changes: []domain.ChangeSummary{{ID: "CHG-shared", Revision: 2, Repository: "example/repo"}}}, s.err
}

func TestSharedChangeSummaryThroughAPIClient(t *testing.T) {
	want := domain.ChangePage{Changes: []domain.ChangeSummary{
		{ID: "CHG-readable", Revision: 2, Digest: strings.Repeat("a", 64), Title: strings.Repeat("🧭", 200), TitleTruncated: true, Intent: strings.Repeat("界", 400), IntentTruncated: true},
		{ID: "CHG-legacy", Revision: 1, Digest: strings.Repeat("b", 64)},
	}}
	s := &listStub{page: want}
	server := httptest.NewServer(New(s))
	defer server.Close()
	page, err := client.New(server.URL, "developer").ListChanges(context.Background(), "", "", 20)
	if err != nil || !reflect.DeepEqual(page, want) || s.calls != 1 {
		t.Fatalf("summary transport lost previews or made extra reads: %+v calls=%d err=%v", page, s.calls, err)
	}
}

func TestSharedChangeListRoutesAndQueries(t *testing.T) {
	cursor, err := domain.EncodeChangeCursor(time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC), "CHG-last")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query, repository, before string
		limit                     int
	}{
		{"", "", "", 20},
		{"?repository=&before=", "", "", 20},
		{"?repository=" + url.QueryEscape("example/repo") + "&before=" + cursor + "&limit=100", "example/repo", cursor, 100},
	} {
		s := &listStub{}
		res := perform(t, New(s), http.MethodGet, "/api/v1/changes"+tc.query, "developer", nil)
		if res.Code != http.StatusOK || s.calls != 1 || s.repository != tc.repository || s.before != tc.before || s.limit != tc.limit || !strings.Contains(res.Body.String(), `"id":"CHG-shared"`) {
			t.Fatalf("list %s: status=%d body=%s service=%+v", tc.query, res.Code, res.Body.String(), s)
		}
	}
}

func TestSharedChangeListRejectsInvalidQueries(t *testing.T) {
	queries := []string{
		"limit=0", "limit=-1", "limit=101", "limit=", "limit=one", "limit=9223372036854775808", "limit=1&limit=2",
		"repository=one&repository=two", "before=&before=", "unknown=1", "limit=1;other=2", "before=%zz",
		"before=not-a-cursor", "before=" + strings.Repeat("a", 1025), "repository=" + strings.Repeat("r", 2049), "repository=%00",
	}
	for _, cursor := range []string{
		`{}`, `null`, `{"createdAt":"2026-09-12T12:00:00Z"}`, `{"createdAt":"invalid","id":"x"}`,
		`{"createdAt":"2026-09-12T12:00:00Z","id":"x","extra":true}`,
		`{"createdAt":"2026-09-12T12:00:00Z","id":"x"}{}`,
	} {
		queries = append(queries, "before="+base64.RawURLEncoding.EncodeToString([]byte(cursor)))
	}
	for _, query := range queries {
		s := &listStub{}
		res := perform(t, New(s), http.MethodGet, "/api/v1/changes?"+query, "developer", nil)
		if res.Code != http.StatusBadRequest || s.calls != 0 || !strings.Contains(res.Body.String(), `"code":"invalid_input"`) {
			t.Fatalf("invalid query %q reached list: code=%d calls=%d body=%s", query, res.Code, s.calls, res.Body.String())
		}
	}
}

func TestSharedChangeListAuthenticationAndErrors(t *testing.T) {
	s := &listStub{}
	res := perform(t, New(s), http.MethodGet, "/api/v1/changes", "", nil)
	if res.Code != http.StatusUnauthorized || s.calls != 0 {
		t.Fatalf("unauthenticated list: code=%d calls=%d", res.Code, s.calls)
	}
	res = perform(t, New(&reviewService{}), http.MethodGet, "/api/v1/changes", "developer", nil)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("unsupported list returned %d", res.Code)
	}
	s.err = domain.ErrUnavailable
	res = perform(t, New(s), http.MethodGet, "/api/v1/changes", "developer", nil)
	if res.Code != http.StatusServiceUnavailable || !strings.Contains(res.Body.String(), `"code":"service_unavailable"`) {
		t.Fatalf("list error: %d %s", res.Code, res.Body.String())
	}
}
