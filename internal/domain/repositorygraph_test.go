package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGraphSourceStrictSelectorsAndRecordRoundTrip(t *testing.T) {
	for _, s := range []string{`{"repositoryId":"a","repositoryId":"b"}`, `{"RepositoryId":"a"}`, `{"repositoryId":"a","source":"forged"}`} {
		var v GraphSource
		if json.Unmarshal([]byte(s), &v) == nil {
			t.Fatalf("ambiguous selector accepted: %s", s)
		}
	}
	v := GraphSourceRecord{GraphSource: GraphSource{RepositoryID: "a", CollectionID: strings.Repeat("a", 32), Digest: strings.Repeat("b", 64)}, Commit: strings.Repeat("c", 40), Freshness: "unknown"}
	encoded, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var got GraphSourceRecord
	if err = json.Unmarshal(encoded, &got); err != nil || got.GraphSource != v.GraphSource || got.Commit != v.Commit {
		t.Fatalf("record roundtrip: %+v %v", got, err)
	}
}
func TestGraphInputRejectsDuplicateRepositoriesAndQueriesAreBounded(t *testing.T) {
	source := GraphSource{RepositoryID: "a", CollectionID: strings.Repeat("a", 32), Digest: strings.Repeat("b", 64)}
	if _, err := NormalizeGraphInput(GraphInput{Sources: []GraphSource{source, source}}); err == nil {
		t.Fatal("duplicate repository accepted")
	}
	for _, q := range []GraphQuery{{Limit: 101}, {Limit: 1, Depth: 6}, {Limit: 1, Depth: -1}, {Limit: 1, Search: "a", NodeID: strings.Repeat("a", 64)}, {Limit: 1, Search: strings.Repeat("a", 257)}} {
		if ValidateGraphQuery(q) == nil {
			t.Fatalf("unbounded query: %+v", q)
		}
	}
}
