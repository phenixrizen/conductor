package domain

import (
	"errors"
	"strings"
	"testing"
)

func coordinationPlan() CoordinationPlan {
	return CoordinationPlan{SchemaVersion: 1, GraphID: strings.Repeat("a", 32), GraphDigest: strings.Repeat("b", 64), MaxParallel: 2,
		Repositories: []CoordinationRepository{{RepositoryID: "application", Commit: strings.Repeat("a", 40), CollectionID: strings.Repeat("c", 32), ReceiptDigest: strings.Repeat("d", 64)}},
		Packages:     []PackagePin{{ChangeID: "CHG-example", RepositoryID: "application", Revision: 1, Digest: strings.Repeat("e", 64)}},
		Tasks: []CoordinationTask{{ID: "implement", Profile: "codex", Perspective: "developer", Prompt: "Synthetic implementation", Scopes: []TaskScope{{RepositoryID: "application", WritablePaths: []string{"src"}}}, TimeoutSeconds: 120, Checks: []VerificationCommand{{ID: "unit", RepositoryID: "application", Argv: []string{"go", "test", "./..."}, TimeoutSeconds: 60}}},
			{ID: "review", Profile: "claude", Perspective: "qc", Prompt: "Review implementation", DependsOn: []string{"implement"}, Scopes: []TaskScope{{RepositoryID: "application", WritablePaths: []string{"src/tests"}}}, TimeoutSeconds: 120}}}
}

func TestCoordinationPlanDependencyAndScope(t *testing.T) {
	if err := ValidateCoordinationPlan(coordinationPlan()); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*CoordinationPlan)
	}{
		{"cycle", func(p *CoordinationPlan) { p.Tasks[0].DependsOn = []string{"review"} }},
		{"missing dependency", func(p *CoordinationPlan) { p.Tasks[1].DependsOn = []string{"unknown"} }},
		{"unordered overlapping writes", func(p *CoordinationPlan) { p.Tasks[1].DependsOn = nil }},
		{"outside repository", func(p *CoordinationPlan) { p.Tasks[1].Scopes[0].RepositoryID = "private" }},
		{"traversal", func(p *CoordinationPlan) { p.Tasks[0].Scopes[0].WritablePaths = []string{"src/../secrets"} }},
		{"git metadata", func(p *CoordinationPlan) { p.Tasks[0].Scopes[0].WritablePaths = []string{".git/config"} }},
		{"unbounded concurrency", func(p *CoordinationPlan) { p.MaxParallel = 100 }},
		{"shell argument NUL", func(p *CoordinationPlan) { p.Tasks[0].Checks[0].Argv = []string{"go\x00"} }},
		{"duplicate dependency", func(p *CoordinationPlan) { p.Tasks[1].DependsOn = []string{"implement", "implement"} }},
		{"unbound package", func(p *CoordinationPlan) { p.Packages[0].RepositoryID = "private" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := coordinationPlan()
			test.change(&plan)
			if !errors.Is(ValidateCoordinationPlan(plan), ErrInvalidInput) {
				t.Fatal("invalid coordinated plan accepted")
			}
		})
	}
	plan := coordinationPlan()
	plan.Tasks[1].DependsOn = nil
	plan.Tasks[1].Scopes[0].WritablePaths = []string{"docs"}
	if err := ValidateCoordinationPlan(plan); err != nil {
		t.Fatal("independent tasks blocked", err)
	}
}

func TestPathsOverlapUsesSegments(t *testing.T) {
	for _, test := range []struct {
		a, b    string
		overlap bool
	}{{"src", "src/test", true}, {"src/test", "src", true}, {"src", "src", true}, {"src", "src-other", false}} {
		if PathsOverlap(test.a, test.b) != test.overlap {
			t.Fatalf("path overlap %q/%q", test.a, test.b)
		}
	}
}

func TestExecutionImagePin(t *testing.T) {
	if ExecutionImagePinned("worker:latest") || ExecutionImagePinned("sha256:bad") {
		t.Fatal("mutable image accepted")
	}
	if !ExecutionImagePinned("sha256:"+strings.Repeat("a", 64)) || !ExecutionImagePinned("registry.invalid/worker@sha256:"+strings.Repeat("b", 64)) {
		t.Fatal("immutable image rejected")
	}
}
