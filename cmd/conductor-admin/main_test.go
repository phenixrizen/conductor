package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigRejectsUnknownFieldsAndOversizedInput(t *testing.T) {
	for _, body := range []string{`null`, `{"grantMeAdmin":true}`, `{} {}`, strings.Repeat(" ", (1<<20)+1)} {
		path := filepath.Join(t.TempDir(), "access.json")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readConfig(path); err == nil {
			t.Fatal("accepted invalid access configuration")
		}
	}
}

func TestCheckDoesNotRequireDatabaseAndDoesNotClaimApplied(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.json")
	if err := os.WriteFile(path, []byte(`{"workspaces":[{"id":"synthetic","name":"Synthetic team"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := run([]string{"apply", "--file", path, "--operator", "synthetic-operator", "--check"}, "invalid-database-url", &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "have not been checked") || strings.Contains(output.String(), "committed") {
		t.Fatalf("syntax check overstated verification: %s", output.String())
	}
}
