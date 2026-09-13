package designtools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestNativeDesignInputBoundsAndExclusiveWrites(t *testing.T) {
	for _, in := range []Request{{Command: "adr-check", Directory: "docs/adr"}, {Command: "adr-new", Directory: "../escape", Title: "Synthetic"}, {Command: "adr-new", Directory: "docs/adr", Title: "--status accepted"}, {Command: "spec-template", Feature: "specs/../escape", Kind: "spec"}, {Command: "spec-template", Feature: "specs/001-example", Kind: "../../accepted"}, {Command: "adr-lint", Directory: "docs/adr", Paths: []string{"source.go"}}} {
		if Validate(in) == nil {
			t.Fatalf("accepted unsafe command %+v", in)
		}
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "existing.md"), []byte("preserved"), 0600); err != nil {
		t.Fatal(err)
	}
	generated := newSnapshot()
	generated.files["new.md"] = []byte("new")
	generated.files["existing.md"] = []byte("replace")
	if _, err := publishGenerated(root, generated); err == nil {
		t.Fatal("replaced existing artifact")
	}
	if _, err := os.Stat(filepath.Join(root, "new.md")); !os.IsNotExist(err) {
		t.Fatal("partially wrote conflicting scaffold")
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := newSnapshot().tree(root, "linked", false, false); err == nil {
		t.Fatal("followed directory symlink")
	}
	if err := syscall.Mkfifo(filepath.Join(root, "fifo"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := newSnapshot().add(root, "fifo", false); err == nil {
		t.Fatal("read FIFO")
	}
	if err := os.WriteFile(filepath.Join(root, "large.md"), []byte(strings.Repeat("x", 65537)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := newSnapshot().add(root, "large.md", false); err == nil {
		t.Fatal("read oversized artifact")
	}
}

func TestPinnedNativeDesignTools(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_DESIGN_TOOLS") != "1" {
		t.Skip("set CONDUCTOR_TEST_DESIGN_TOOLS=1 and explicit pinned native paths")
	}
	rt := Runtime{Node: os.Getenv("CONDUCTOR_TEST_DESIGN_NODE"), Specify: os.Getenv("CONDUCTOR_TEST_SPECIFY"), ADR: os.Getenv("CONDUCTOR_TEST_ADRKIT"), CoreRoot: os.Getenv("CONDUCTOR_TEST_DESIGN_CORE")}
	root := t.TempDir()
	execute := func(in Request, want string) Report {
		t.Helper()
		r, err := rt.Execute(context.Background(), root, in)
		if err != nil || r.State != want {
			t.Fatalf("native %s: %+v %v", in.Command, r, err)
		}
		if len(r.InputDigest) != 64 || len(r.OutputDigest) != 64 {
			t.Fatal("unbound report")
		}
		return r
	}
	execute(Request{Command: "spec-init"}, "produced")
	script, err := os.Stat(filepath.Join(root, ".specify/scripts/bash/check-prerequisites.sh"))
	if err != nil || script.Mode()&0111 == 0 {
		t.Fatal("native scaffold lost executable scripts", err)
	}
	if _, err := rt.Execute(context.Background(), root, Request{Command: "spec-init"}); err == nil {
		t.Fatal("overwrote existing constitution")
	}
	for _, kind := range []string{"spec", "plan", "tasks"} {
		execute(Request{Command: "spec-template", Feature: "specs/001-synthetic", Kind: kind}, "produced")
	}
	good := execute(Request{Command: "spec-check", Feature: "specs/001-synthetic"}, "passed")
	if !strings.Contains(good.Output, "tasks.md") {
		t.Fatal("native prerequisite list absent")
	}
	if _, err := os.Stat(filepath.Join(root, ".specify/feature.json")); !os.IsNotExist(err) {
		t.Fatal("read check mutated project feature state")
	}
	if err := os.Remove(filepath.Join(root, "specs/001-synthetic/tasks.md")); err != nil {
		t.Fatal(err)
	}
	execute(Request{Command: "spec-check", Feature: "specs/001-synthetic"}, "failed")
	if err := os.MkdirAll(filepath.Join(root, "empty-adr"), 0700); err != nil {
		t.Fatal(err)
	}
	execute(Request{Command: "adr-lint", Directory: "empty-adr"}, "unavailable")
	proposal := execute(Request{Command: "adr-new", Directory: "docs/adr", Title: "Synthetic source-bound decision"}, "produced")
	if len(proposal.Created) != 1 {
		t.Fatal("native ADR did not produce one file")
	}
	data, err := os.ReadFile(filepath.Join(root, proposal.Created[0].Path))
	if err != nil || !strings.Contains(string(data), "status: proposed") {
		t.Fatal("native ADR was not Proposed", err)
	}
	first := execute(Request{Command: "adr-lint", Directory: "docs/adr"}, "passed")
	second := execute(Request{Command: "adr-lint", Directory: "docs/adr"}, "passed")
	if first.InputDigest != second.InputDigest || first.OutputDigest != second.OutputDigest {
		t.Fatal("deterministic lint drift")
	}
	for _, cmd := range []string{"adr-check", "adr-explain"} {
		execute(Request{Command: cmd, Directory: "docs/adr", Paths: []string{"src/absent.go"}}, "passed")
	}
	graph := execute(Request{Command: "adr-graph", Directory: "docs/adr"}, "passed")
	if !json.Valid([]byte(graph.Output)) {
		t.Fatalf("native graph JSON unavailable: %s", graph.Output)
	}
	if err := os.WriteFile(filepath.Join(root, proposal.Created[0].Path), []byte("malformed source ADR"), 0600); err != nil {
		t.Fatal(err)
	}
	execute(Request{Command: "adr-lint", Directory: "docs/adr"}, "failed")
}
