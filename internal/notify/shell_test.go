package notify

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The shell reading names only what it is sure of: the reads of the plain
// readers, the writes and edits of redirects, tee and sed -i, a file that
// exists now; never a flag, a pattern, a variable, a substitution, a
// heredoc's body, a descriptor or /dev/null.
func TestShellFiles(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"README.md", "main.go", "out.txt", "log.txt", "my file.txt", "sub/x.go"} {
		os.MkdirAll(filepath.Join(dir, filepath.Dir(f)), 0o755)
		os.WriteFile(filepath.Join(dir, f), []byte("x\n"), 0o644)
	}
	p := func(f string) string { return filepath.Join(dir, f) }
	cases := []struct {
		cmd  string
		want []FileRef
	}{
		{"cat README.md", []FileRef{{"read", p("README.md")}}},
		{"cat README.md main.go | head -n 5", []FileRef{{"read", p("README.md")}, {"read", p("main.go")}}},
		{"head -n 20 main.go && tail -c 10 README.md", []FileRef{{"read", p("main.go")}, {"read", p("README.md")}}},
		{"sed -n '1,40p' main.go", []FileRef{{"read", p("main.go")}}},
		{"sed -i 's/a/b/' main.go", []FileRef{{"edit", p("main.go")}}},
		{"sed 's/a/b/' main.go", nil},
		{"printf 'hi\\n' > out.txt", []FileRef{{"write", p("out.txt")}}},
		{"echo there >> log.txt", []FileRef{{"edit", p("log.txt")}}},
		{"go test ./... 2>&1 | tee out.txt", []FileRef{{"write", p("out.txt")}}},
		{"echo x | tee -a log.txt >/dev/null", []FileRef{{"edit", p("log.txt")}}},
		{"cat 'my file.txt'", []FileRef{{"read", p("my file.txt")}}},
		{"cat \"$FILE\"", nil},
		{"cat $(ls)", nil},
		{"cat `ls`", nil},
		{"cat > out.txt <<'EOF'\nREADME.md\nEOF", []FileRef{{"write", p("out.txt")}}},
		{"cat missing.txt sub", nil},
		{"cd sub && cat x.go", nil}, // cat's file is relative to sub, which a plain reading does not follow: not found here, so nothing
		{"cat sub/x.go; wc -l < main.go", []FileRef{{"read", p("sub/x.go")}, {"read", p("main.go")}}},
		{"grep -rn foo .", nil},
		{"FOO=1 cat README.md", []FileRef{{"read", p("README.md")}}},
		{"cat README.md README.md", []FileRef{{"read", p("README.md")}}},
		{"ls -la", nil},
		{"", nil},
	}
	for _, c := range cases {
		if got := shellFiles(c.cmd, dir); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: %v, want %v", c.cmd, got, c.want)
		}
	}
	// Without the command's directory a relative path names nothing; an absolute one still does.
	if got := shellFiles("cat README.md "+p("main.go"), ""); !reflect.DeepEqual(got, []FileRef{{"read", p("main.go")}}) {
		t.Errorf("no cwd: %v", got)
	}
}
