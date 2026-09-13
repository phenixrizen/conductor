package domain

import (
	"errors"
	"testing"
)

func TestRepositoryHostCanonicalization(t *testing.T) {
	for input, want := range map[string]string{"GitHub.Example.Test": "github.example.test", "github.example.test:00443": "github.example.test", "gitlab.example.test:08443": "gitlab.example.test:8443"} {
		got, err := NormalizeRepositoryHost(input)
		if err != nil || got != want {
			t.Errorf("%q: got %q %v, want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"https://github.example.test", "user@github.example.test", "github.example.test/path", "github.example.test:invalid", "github.example.test:0", "github.example.test:65536", "-invalid.example.test", "invalid..example.test", "invalid.example.test.", "github.example.test\x1b"} {
		if _, err := NormalizeRepositoryHost(input); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("accepted invalid installation host %q: %v", input, err)
		}
	}
}
func TestRepositoryApprovalRequiresHumanAuthority(t *testing.T) {
	for _, kind := range []string{"agent", "", "unknown"} {
		if err := ValidateRepositoryAction(Principal{ID: "synthetic", Kind: kind}, true, true, true, "approve"); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s approval accepted: %v", kind, err)
		}
	}
	if err := ValidateRepositoryAction(Principal{Kind: "human"}, true, false, true, "approve"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRepositoryAction(Principal{Kind: "human"}, false, true, true, "approve"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("approval leaked inaccessible repository: %v", err)
	}
}
