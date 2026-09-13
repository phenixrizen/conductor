package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTrackerCommandsRejectAmbiguousFields(t *testing.T) {
	for _, body := range []string{
		`{"issueId":"a","IssueId":"b","packages":[]}`,
		`{"issueId":"a","issueId":"b","packages":[]}`,
		`{"issueId":"a","packages":[{"repositoryId":"r","PackageID":"p","revision":1,"digest":"d"}]}`,
		`{"issueId":"a","packages":[],"publications":[{"id":"a","digest":"b","DIGEST":"c"}]}`,
		`null`, `{} {}`,
	} {
		var in TrackerLinkInput
		if json.Unmarshal([]byte(body), &in) == nil {
			t.Errorf("accepted ambiguous input: %s", body)
		}
	}
	for _, body := range []string{`{"mode":"refresh","Mode":"restore"}`, `{"mode":"refresh","actor":"forged"}`, `{"mode":"refresh","mode":"restore"}`} {
		var in TrackerSyncInput
		if json.Unmarshal([]byte(body), &in) == nil {
			t.Errorf("accepted sync: %s", body)
		}
	}
	var in TrackerSyncInput
	if err := json.Unmarshal([]byte(`{"mode":"refresh","linkDigest":"`+strings.Repeat("a", 64)+`"}`), &in); err != nil || ValidateTrackerSync(in) != nil {
		t.Fatal("valid command rejected", err)
	}
}
