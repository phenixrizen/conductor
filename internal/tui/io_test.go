package tui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadContentBoundsAndExtensions(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		valid       bool
	}{
		{"extension object", `{"future":{"opaque":[true,"kept"]}}`, true},
		{"null", `null`, false},
		{"array", `[]`, false},
		{"empty", `{}`, false},
		{"multiple values", `{"intent":"synthetic"}{}`, false},
		{"too large", `{"intent":"synthetic"}` + strings.Repeat(" ", 1<<20), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "synthetic.json")
			if err := os.WriteFile(path, []byte(tc.input), 0600); err != nil {
				t.Fatal(err)
			}
			content, err := readContent(context.Background(), path)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t; got %v", tc.valid, err)
			}
			if tc.valid && content["future"] == nil {
				t.Fatal("extension lost")
			}
		})
	}
	for _, path := range []string{"", "-", t.TempDir()} {
		if _, err := readContent(context.Background(), path); err == nil {
			t.Fatalf("accepted non-file %q", path)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readContent(ctx, "missing.json"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled read attempted file I/O")
	}
}

func TestPrettyJSONBoundsDeepIndentationAndRetainsValidJSON(t *testing.T) {
	input := []byte(`{"intent":` + strings.Repeat("[", 3000) + `"quote: \" slash: \\"` + strings.Repeat("]", 3000) + `}`)
	output := prettyJSON(input)
	if !json.Valid([]byte(output)) {
		t.Fatal("formatter changed JSON semantics")
	}
	if len(output) > len(input)*20 {
		t.Fatalf("unbounded indentation expansion: %d", len(output))
	}
	var before, after any
	if err := json.Unmarshal(input, &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(output), &after); err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatal("formatter changed content")
	}
}
