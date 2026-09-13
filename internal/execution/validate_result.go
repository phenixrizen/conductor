package execution

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// ValidateResult classifies factual production independently of the transport's
// success. A completed process alone cannot substitute for exact verification
// evidence. Source and patch digests are still checked by the isolated verifier.
func ValidateResult(request Request, profile Profile, image string, result Result) (string, error) {
	invalid := func() (string, error) { return "", fmt.Errorf("%w: result evidence binding", ErrSandbox) }
	if ValidateRequest(request) != nil || profile.Validate() != nil || !validImage(image) {
		return invalid()
	}
	encoded, _ := json.Marshal(result)
	if len(encoded) > 16<<20 {
		return invalid()
	}
	profileJSON, _ := json.Marshal(profile)
	if !result.CleanupConfirmed || result.InputDigest != InputDigest(request) || result.ProfileDigest != Sum(profileJSON) || result.Image != image || result.Adapter != profile.Adapter || result.StartedAt.IsZero() || result.FinishedAt.Before(result.StartedAt) {
		return invalid()
	}
	producer := result.Producer
	if len(producer.Requirements) != 0 {
		return invalid()
	}
	if producer.ID != "producer" || producer.SourceDigest != result.InputDigest || len(producer.Output) > MaxLogBytes || !digest.MatchString(producer.OutputDigest) {
		return invalid()
	}
	if profile.Adapter == "command/v1" && producer.OutputDigest != Sum([]byte(producer.Output)) {
		return invalid()
	}
	if producer.State == "blocked" {
		if producer.ExitCode != nil || len(result.Patches) != 0 || len(result.Checks) != len(request.Checks) {
			return invalid()
		}
		for i, c := range request.Checks {
			e := result.Checks[i]
			if e.ID != c.ID || e.RepositoryID != c.RepositoryID || !reflect.DeepEqual(e.Argv, c.Argv) || !slices.Equal(e.Requirements, c.Requirements) || e.State != "unexecuted" || e.ExitCode != nil {
				return invalid()
			}
		}
		return "blocked", nil
	}
	version := strings.Split(profile.Adapter, "/")[1]
	if profile.Adapter == "command/v1" {
		version = "1"
	}
	if result.AdapterVersion != version || producer.StartedAt.IsZero() || producer.FinishedAt.Before(producer.StartedAt) {
		return invalid()
	}
	if !evidenceState(producer.State) {
		return invalid()
	}
	if producer.State == "passed" && (producer.ExitCode == nil || *producer.ExitCode != 0 || producer.Truncated) {
		return invalid()
	}
	if profile.Adapter == "command/v1" && !reflect.DeepEqual(producer.Argv, profile.Command) {
		return invalid()
	}
	if len(result.Patches) != len(request.Repositories) {
		return invalid()
	}
	patches := map[string]Patch{}
	for _, p := range result.Patches {
		if _, exists := patches[p.RepositoryID]; exists {
			return invalid()
		}
		if !oid.MatchString(p.BaseTree) || !oid.MatchString(p.ResultTree) || p.Digest != Sum(p.Patch) || len(p.Patch) > MaxPatchBytes || len(p.Paths) > MaxFiles {
			return invalid()
		}
		patches[p.RepositoryID] = p
	}
	for _, repo := range request.Repositories {
		p, ok := patches[repo.ID]
		if !ok || p.BaseCommit != repo.Commit {
			return invalid()
		}
		inherited := map[string]bool{}
		for _, dependency := range repo.Dependencies {
			if dependency.BaseTree != p.BaseTree {
				return invalid()
			}
			for _, name := range dependency.Paths {
				inherited[name] = true
			}
		}
		seen := map[string]bool{}
		for _, name := range p.Paths {
			if !safePath(name) || seen[name] || (!allowedPath(name, repo.WritablePaths) && !inherited[name]) {
				return invalid()
			}
			seen[name] = true
		}
	}
	if len(result.Checks) != len(request.Checks) {
		return invalid()
	}
	patchJSON, _ := json.Marshal(result.Patches)
	sourceDigest := Sum(patchJSON)
	outcome := "succeeded"
	if producer.State != "passed" {
		outcome = "failed"
	}
	for i, c := range request.Checks {
		e := result.Checks[i]
		if e.ID != c.ID || e.RepositoryID != c.RepositoryID || !reflect.DeepEqual(e.Argv, c.Argv) || !slices.Equal(e.Requirements, c.Requirements) || len(e.Output) > MaxLogBytes || e.OutputDigest != Sum([]byte(e.Output)) {
			return invalid()
		}
		if e.State == "unexecuted" {
			if e.ExitCode != nil || e.SourceDigest != result.InputDigest {
				return invalid()
			}
			outcome = "failed"
			continue
		}
		if !evidenceState(e.State) {
			return invalid()
		}
		if producer.State != "passed" || e.SourceDigest != sourceDigest || e.StartedAt.IsZero() || e.FinishedAt.Before(e.StartedAt) {
			return invalid()
		}
		if e.State == "passed" {
			if e.ExitCode == nil || *e.ExitCode != 0 || e.Truncated {
				return invalid()
			}
		} else {
			outcome = "failed"
		}
	}
	return outcome, nil
}

func evidenceState(state string) bool {
	switch state {
	case "passed", "failed", "cancelled", "timed_out", "output_limit", "unavailable":
		return true
	}
	return false
}
