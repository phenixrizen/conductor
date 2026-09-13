package delivery

import (
	"encoding/json"
	"reflect"
	"strings"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/execution"
)

// SelectedPatch checks the persisted receipt against the immutable task contract.
// Persistence already verifies the producer's input digest at receipt admission;
// publication still fails closed for omitted, failed or source-unbound checks.
func SelectedPatch(raw json.RawMessage, digest, repository string, task domain.CoordinationTask) (execution.Patch, error) {
	var result execution.Result
	if len(raw) == 0 || len(raw) > 16<<20 || json.Unmarshal(raw, &result) != nil {
		return execution.Patch{}, ErrArtifact
	}
	actual, err := domain.JSONDigest(result)
	if err != nil || actual != digest {
		return execution.Patch{}, ErrArtifact
	}
	// The coordinator admitted this immutable result only after isolated cleanup
	// and exact input/profile/image validation. Do not publish legacy loose output.
	if !result.CleanupConfirmed || !domain.IsLowerHex(result.ProfileDigest, 64) ||
		!strings.HasPrefix(result.Image, "sha256:") || !domain.IsLowerHex(strings.TrimPrefix(result.Image, "sha256:"), 64) ||
		result.Adapter == "" || result.AdapterVersion == "" || !domain.IsLowerHex(result.InputDigest, 64) {
		return execution.Patch{}, ErrArtifact
	}
	producer := result.Producer
	if producer.Truncated || producer.State != "passed" || producer.ExitCode == nil || *producer.ExitCode != 0 || len(result.Checks) != len(task.Checks) {
		return execution.Patch{}, ErrArtifact
	}
	patchJSON, err := json.Marshal(result.Patches)
	if err != nil || result.Producer.SourceDigest != result.InputDigest || !domain.IsLowerHex(result.Producer.OutputDigest, 64) || result.Adapter == "command/v1" && result.Producer.OutputDigest != execution.Sum([]byte(result.Producer.Output)) {
		return execution.Patch{}, ErrArtifact
	}
	checkSource := execution.Sum(patchJSON)
	covered := false
	seen := map[string]bool{}
	for _, expected := range task.Checks {
		covered = covered || expected.RepositoryID == repository
		matched := false
		for _, check := range result.Checks {
			if check.ID != expected.ID {
				continue
			}
			if matched || seen[check.ID] || check.RepositoryID != expected.RepositoryID || !reflect.DeepEqual(check.Argv, expected.Argv) || check.State != "passed" || check.ExitCode == nil || *check.ExitCode != 0 || check.Truncated || check.SourceDigest != checkSource || check.OutputDigest != execution.Sum([]byte(check.Output)) {
				return execution.Patch{}, ErrArtifact
			}
			matched = true
			seen[check.ID] = true
		}
		if !matched {
			return execution.Patch{}, ErrArtifact
		}
	}
	if !covered {
		return execution.Patch{}, ErrArtifact
	}
	var selected execution.Patch
	found := false
	for _, patch := range result.Patches {
		if patch.RepositoryID != repository {
			continue
		}
		if found {
			return execution.Patch{}, ErrArtifact
		}
		selected = patch
		found = true
	}
	if !found || len(selected.Patch) == 0 || selected.Digest != execution.Sum(selected.Patch) {
		return execution.Patch{}, ErrArtifact
	}
	return selected, nil
}
