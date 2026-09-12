package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContentFileBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		valid       bool
	}{
		{"unknown structured fields", `{"intent":"synthetic","future":{"items":[true,"retained"]}}`, true},
		{"multiple values", `{"intent":"synthetic"} {}`, false},
		{"null", `null`, false},
		{"array", `[]`, false},
		{"over limit with whitespace", `{"intent":"synthetic"}` + strings.Repeat(" ", 1<<20), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "package.json")
			if err := os.WriteFile(path, []byte(tc.input), 0600); err != nil {
				t.Fatal(err)
			}
			content, err := contentFrom(path, "")
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, got content=%v, err=%v", tc.valid, content, err)
			}
			if tc.valid && content["future"] == nil {
				t.Fatal("unknown content was lost")
			}
		})
	}
}
