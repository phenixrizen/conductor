package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/domain"
)

type assistanceTransportService struct {
	queryGuardService
	operations []string
	key, id    string
	input      any
	err        error
}

func (s *assistanceTransportService) RequestDesignAssistance(_ context.Context, key string, in domain.AssistanceInput) (domain.DesignAssistance, error) {
	s.operations = append(s.operations, "request")
	s.key = key
	s.input = in
	return domain.DesignAssistance{}, s.err
}
func (s *assistanceTransportService) ListDesignAssistance(_ context.Context, in domain.AssistanceListOptions) (domain.AssistancePage, error) {
	s.operations = append(s.operations, "list")
	s.input = in
	return domain.AssistancePage{}, s.err
}
func (s *assistanceTransportService) GetDesignAssistance(_ context.Context, id string) (domain.DesignAssistance, error) {
	s.operations = append(s.operations, "get")
	s.id = id
	return domain.DesignAssistance{}, s.err
}
func (s *assistanceTransportService) ProposeDesignSections(_ context.Context, id, key string, in domain.SuggestionInput) (domain.DesignAssistance, error) {
	s.operations = append(s.operations, "suggestion")
	s.id = id
	s.key = key
	s.input = in
	return domain.DesignAssistance{}, s.err
}
func (s *assistanceTransportService) ApplyDesignSuggestion(_ context.Context, id, key string, in domain.ApplySuggestionInput) (domain.DesignAssistance, error) {
	s.operations = append(s.operations, "application")
	s.id = id
	s.key = key
	s.input = in
	return domain.DesignAssistance{}, s.err
}
func TestDesignAssistanceTransportPreservesCommands(t *testing.T) {
	digest, id := strings.Repeat("a", 64), strings.Repeat("b", 32)
	for _, tc := range []struct {
		operation, path string
		input           any
	}{
		{"request", "/api/v1/design-assistance", domain.AssistanceInput{ChangeID: "CHG-inspected", ExpectedRevision: 7, ExpectedDigest: digest, Instruction: "Improve these exact sections", Sections: []string{"scope", "design"}}},
		{"suggestion", "/api/v1/design-assistance/" + id + "/suggestion", domain.SuggestionInput{RequestDigest: digest, Sections: map[string]string{"scope": "", "design": "Suggested"}, Note: "Synthetic"}},
		{"application", "/api/v1/design-assistance/" + id + "/application", domain.ApplySuggestionInput{RequestDigest: digest, SuggestionDigest: digest, ExpectedRevision: 7, ExpectedDigest: digest, Sections: []string{"scope", "design"}}},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			raw, _ := json.Marshal(tc.input)
			s := &assistanceTransportService{}
			w := httptest.NewRecorder()
			NewAuthenticated(s, queryGuardVerifier{}).ServeHTTP(w, collectionRequest(http.MethodPost, tc.path, string(raw)))
			if w.Code != http.StatusCreated || !reflect.DeepEqual(s.operations, []string{tc.operation}) || s.key != "synthetic-request-1" || !reflect.DeepEqual(s.input, tc.input) || s.queryGuardService.calls != 0 {
				t.Fatalf("status=%d operations=%v input=%+v body=%s", w.Code, s.operations, s.input, w.Body)
			}
		})
	}
}
func TestDesignAssistanceTransportRejectsAmbiguity(t *testing.T) {
	digest, id := strings.Repeat("a", 64), strings.Repeat("b", 32)
	valid := `{"changeId":"CHG-one","expectedRevision":1,"expectedDigest":"` + digest + `","instruction":"Improve","sections":["design"]}`
	for _, tc := range []struct {
		name, method, path, body string
		mutate                   func(*http.Request)
	}{
		{"missing key", "POST", "/api/v1/design-assistance", valid, func(r *http.Request) { r.Header.Del("Idempotency-Key") }},
		{"duplicate key", "POST", "/api/v1/design-assistance", valid, func(r *http.Request) { r.Header.Add("Idempotency-Key", "other") }},
		{"query command", "POST", "/api/v1/design-assistance?expectedRevision=2", valid, nil},
		{"query read", "GET", "/api/v1/design-assistance/" + id + "?refresh=true", "", nil},
		{"duplicate query", "GET", "/api/v1/design-assistance?limit=1&limit=2", "", nil},
		{"unknown query", "GET", "/api/v1/design-assistance?requesterId=other", "", nil},
		{"null", "POST", "/api/v1/design-assistance", "null", nil},
		{"unknown field", "POST", "/api/v1/design-assistance", strings.TrimSuffix(valid, "}") + `,"actor":"someone"}`, nil},
		{"case alias", "POST", "/api/v1/design-assistance", strings.Replace(valid, `"instruction"`, `"Instruction"`, 1), nil},
		{"duplicate field", "POST", "/api/v1/design-assistance", strings.TrimSuffix(valid, "}") + `,"instruction":"other"}`, nil},
		{"null proposed field", "POST", "/api/v1/design-assistance/" + id + "/suggestion", `{"requestDigest":"` + digest + `","sections":{"design":null}}`, nil},
		{"duplicate proposed field", "POST", "/api/v1/design-assistance/" + id + "/suggestion", `{"requestDigest":"` + digest + `","sections":{"design":"a","design":"b"}}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &assistanceTransportService{}
			w := httptest.NewRecorder()
			r := collectionRequest(tc.method, tc.path, tc.body)
			if tc.mutate != nil {
				tc.mutate(r)
			}
			NewAuthenticated(s, queryGuardVerifier{}).ServeHTTP(w, r)
			if w.Code != http.StatusBadRequest || len(s.operations) != 0 {
				t.Fatalf("status=%d operations=%v body=%s", w.Code, s.operations, w.Body)
			}
		})
	}
	s := &assistanceTransportService{}
	w := perform(t, New(s), "POST", "/api/v1/design-assistance", "local-author", map[string]any{})
	if w.Code != http.StatusServiceUnavailable || len(s.operations) != 0 {
		t.Fatalf("local mode used native assistance: %d", w.Code)
	}
}
