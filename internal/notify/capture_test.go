package notify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The payloads of a live run of each agent (Codex 0.161.0, Copilot CLI
// 1.0.91, Antigravity 1.2.14, captured 2026-10-09 in a scratch repository
// at /r, redacted; round 13, G2b), each tool call mapped to the files it
// touched. The run: cat README.md, create hello.txt with "hi", append
// "there" to it, make main.go print hello.
func TestMappersReadTheCapturedPayloads(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"README.md", "hello.txt", "main.go"} {
		os.WriteFile(filepath.Join(dir, f), []byte("x\n"), 0o644)
	}
	p := func(f string) string { return filepath.Join(dir, f) }
	read, write, edit := func(f string) FileRef { return FileRef{"read", p(f)} }, func(f string) FileRef { return FileRef{"write", p(f)} }, func(f string) FileRef { return FileRef{"edit", p(f)} }
	cases := []struct {
		file   string
		event  string
		mapper func([]byte) (Request, bool)
		want   [][]FileRef
	}{
		{"codex-hooks.json", "PostToolUse", MapCodexHook, [][]FileRef{
			{read("README.md")}, {write("hello.txt")}, {edit("hello.txt")}, {read("main.go")}, {edit("main.go")},
		}},
		{"copilot-hooks.json", "postToolUse", MapCopilotHook, [][]FileRef{
			{read("README.md")}, nil, {write("hello.txt"), edit("hello.txt"), read("hello.txt")}, {read("main.go")}, {edit("main.go")},
		}},
		{"agy-hooks.json", "PostToolUse", MapAgyHook, [][]FileRef{
			nil, {read("README.md")}, {read("main.go")}, {write("hello.txt")}, {edit("hello.txt")}, {edit("main.go")}, {read("README.md")},
		}},
	}
	for _, c := range cases {
		raw, err := os.ReadFile(filepath.Join("testdata", c.file))
		if err != nil {
			t.Fatal(err)
		}
		var hooks []struct {
			Event   string          `json:"event"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(raw, &hooks); err != nil {
			t.Fatal(err)
		}
		var got [][]FileRef
		for _, h := range hooks {
			if h.Event != c.event {
				continue
			}
			// The scratch repository's paths, quoted or inside a patch's text, made the test's directory.
			payload := strings.NewReplacer(`"/r/`, `"`+dir+`/`, `"/r"`, `"`+dir+`"`, ` /r/`, ` `+dir+`/`).Replace(string(h.Payload))
			req, ok := c.mapper([]byte(payload))
			if !ok || req.Event != "tool_use" || req.Tool == "" {
				t.Fatalf("%s: %s mapped to %+v %v", c.file, payload, req, ok)
			}
			got = append(got, req.Files)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got  %v\n want %v", c.file, got, c.want)
		}
	}
}
