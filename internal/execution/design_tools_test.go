package execution

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/designtools"
)

func TestDockerNativeDesignToolsProduceAndVerify(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_DESIGN_EXECUTION") != "1" {
		t.Skip("set CONDUCTOR_TEST_DESIGN_EXECUTION=1 and CONDUCTOR_TEST_DESIGN_IMAGE for native isolated execution")
	}
	image := os.Getenv("CONDUCTOR_TEST_DESIGN_IMAGE")
	if !validImage(image) {
		t.Fatal("design acceptance requires immutable built image digest")
	}
	for _, kind := range []string{"spec-kit", "adrkit"} {
		t.Run(kind, func(t *testing.T) {
			request := validRequest()
			request.TimeoutSeconds = 120
			request.Prompt = "Produce only the reviewed synthetic design artifacts; all ADRs remain Proposed."
			request.Repositories[0].Bundle, request.Repositories[0].Commit = fixtureBundle(t, map[string]string{"README.md": "Synthetic design tools acceptance\n"})
			command := []string{"conductor-design-tools", "adr-new", "--title", "Synthetic database decision", "--dir", "docs/adr"}
			request.Repositories[0].WritablePaths = []string{"docs/adr"}
			request.Checks = []Check{{ID: "adr-schema", RepositoryID: "app", Argv: []string{"conductor-design-tools", "adr-lint", "--dir", "docs/adr"}, TimeoutSeconds: 30}}
			if kind == "spec-kit" {
				command = []string{"sh", "-c", "conductor-design-tools spec-template --feature specs/001-synthetic --kind spec && conductor-design-tools spec-template --feature specs/001-synthetic --kind plan && conductor-design-tools spec-template --feature specs/001-synthetic --kind tasks"}
				request.Repositories[0].WritablePaths = []string{"specs/001-synthetic"}
				request.Checks = []Check{{ID: "spec-prerequisites", RepositoryID: "app", Argv: []string{"conductor-design-tools", "spec-check", "--feature", "specs/001-synthetic"}, TimeoutSeconds: 30}}
			}
			runner := Runner{Image: image, Profile: Profile{Adapter: "command/v1", Command: command}}
			result, err := runner.Run(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if !result.CleanupConfirmed || result.Producer.State != "passed" || result.Image != image || len(result.Patches) != 1 || len(result.Patches[0].Paths) == 0 || result.Patches[0].BaseCommit != request.Repositories[0].Commit || result.Patches[0].ResultTree == result.Patches[0].BaseTree || len(result.Checks) != 1 {
				t.Fatalf("native design artifacts not source bound: %+v", result)
			}
			check := result.Checks[0]
			if check.State != "passed" || check.SourceDigest == "" || check.Truncated {
				t.Fatalf("native check unavailable: %+v", check)
			}
			var report designtools.Report
			if json.Unmarshal([]byte(check.Output), &report) != nil || report.Tool != kind || report.State != "passed" || report.InputDigest == "" || report.OutputDigest == "" {
				t.Fatalf("missing native report: %s", check.Output)
			}
			if kind == "adrkit" && (!strings.Contains(string(result.Patches[0].Patch), "+status: proposed") || strings.Contains(string(result.Patches[0].Patch), "+status: accepted")) {
				t.Fatal("ADR proposal acquired acceptance")
			}
			if kind == "spec-kit" && !strings.Contains(report.Scope, "prerequisites") {
				t.Fatal("prerequisite result implied semantic verification")
			}
		})
	}
}

func TestDockerNativeDesignFailedCheckStaysFailed(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_DESIGN_EXECUTION") != "1" {
		t.Skip("set CONDUCTOR_TEST_DESIGN_EXECUTION=1 for native failed-check acceptance")
	}
	image := os.Getenv("CONDUCTOR_TEST_DESIGN_IMAGE")
	if !validImage(image) {
		t.Fatal("requires immutable design image")
	}
	request := validRequest()
	request.Repositories[0].Bundle, request.Repositories[0].Commit = fixtureBundle(t, map[string]string{"docs/adr/0001-invalid.md": "Malformed synthetic ADR\n"})
	request.Repositories[0].WritablePaths = nil
	request.Checks = []Check{{ID: "native-adr-schema", RepositoryID: "app", Argv: []string{"conductor-design-tools", "adr-lint", "--dir", "docs/adr"}, TimeoutSeconds: 30}}
	result, err := (Runner{Image: image, Profile: Profile{Adapter: "command/v1", Command: []string{"true"}}}).Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Checks) != 1 || result.Checks[0].State != "failed" || result.Checks[0].ExitCode == nil || *result.Checks[0].ExitCode == 0 {
		t.Fatalf("native failure looked passing: %+v", result)
	}
}
