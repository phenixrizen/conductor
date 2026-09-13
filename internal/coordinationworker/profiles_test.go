package coordinationworker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/execution"
)

func TestPrivateProfileCatalogCannotExpandCredentialBoundary(t *testing.T) {
	profile := ProfileConfig{WorkspaceID: "team", ID: "synthetic", Image: "sha256:" + strings.Repeat("a", 64), Profile: execution.Profile{Adapter: "command/v1", Command: []string{"true"}}}
	data, err := json.Marshal(map[string]any{"profiles": []ProfileConfig{profile}})
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(t.TempDir(), "profiles.json")
	if err = os.WriteFile(name, data, 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadProfiles(name); err != nil || len(got) != 1 {
		t.Fatalf("private catalog: %v", err)
	}
	if err = os.Chmod(name, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadProfiles(name); err == nil {
		t.Fatal("publicly readable credential references accepted")
	}
	link := filepath.Join(t.TempDir(), "profiles.json")
	if err = os.Symlink(name, link); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadProfiles(link); err == nil {
		t.Fatal("symlink catalog accepted")
	}
	for _, mutate := range []func(*ProfileConfig){func(p *ProfileConfig) { p.AllowProviderNetwork = true }, func(p *ProfileConfig) { p.CredentialFile = "/synthetic/key" }, func(p *ProfileConfig) { p.Image = "worker:latest" }, func(p *ProfileConfig) { p.Profile = execution.Profile{Adapter: "codex/0.154.0"} }} {
		p := profile
		mutate(&p)
		if ValidateProfiles([]ProfileConfig{p}) == nil {
			t.Fatal("unsafe profile accepted")
		}
	}
	if ValidateProfiles([]ProfileConfig{profile, profile}) == nil {
		t.Fatal("ambiguous profile identity accepted")
	}
}
