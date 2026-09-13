package reviewinput

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

func TestRuntimePreviewBindsExactWindowWithoutInventingPolicy(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	input := domain.RuntimeInput{DeliveryID: strings.Repeat("a", 32), DeliveryDigest: strings.Repeat("b", 64), ObservationSequence: 1, DeploymentID: "deployment", Commit: strings.Repeat("c", 40), Environment: "production", Start: now.Add(-time.Minute), End: now.Add(-30 * time.Second), Requirements: []domain.RuntimeRequirement{}}
	raw, _ := json.Marshal(map[string]any{"idempotencyKey": "runtime", "input": input})
	d, err := Decode(raw, "runtime")
	if err != nil {
		t.Fatal(err)
	}
	var got domain.RuntimeInput
	if json.Unmarshal(d.Input, &got) != nil || !got.Start.Equal(input.Start) || got.Commit != input.Commit {
		t.Fatal("pins changed")
	}
	for _, bad := range []string{strings.Replace(string(raw), `"start":`, `"Start":`, 1), strings.Replace(string(raw), `"start":`, `"actor":"forged","start":`, 1), strings.Replace(string(raw), `"start":`, `"threshold":5,"start":`, 1), strings.Replace(string(raw), `"start":`, `"query":"arbitrary","start":`, 1), strings.Replace(string(raw), `"start":`, `"start":{},"start":`, 1)} {
		if _, err = Decode([]byte(bad), "runtime"); err == nil {
			t.Fatal("ambiguous policy or timestamp accepted")
		}
	}
	input.End = input.End.Add(-time.Second)
	raw, _ = json.Marshal(map[string]any{"idempotencyKey": "runtime", "input": input})
	next, err := Decode(raw, "runtime")
	if err != nil || next.Digest == d.Digest {
		t.Fatal("changed window not bound", err)
	}
}
