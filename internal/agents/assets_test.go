package agents

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/catalog"
)

// Every adapter's assets land under the hooks dir with the binary path in
// place of the placeholder: files 0600, directories 0700, JSON indented with
// two spaces.
func TestWriteAssetsWritesEveryAsset(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data", "hooks")
	if err := WriteAssets(dir, "/opt/conductor"); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("hooks dir: %v %v", fi.Mode(), err)
	}
	count := 0
	for _, a := range All() {
		for rel := range a.Assets {
			count++
			p := filepath.Join(dir, filepath.FromSlash(rel))
			fi, err := os.Lstat(p)
			if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o600 {
				t.Fatalf("%s: %v %v", rel, fi.Mode(), err)
			}
			for d := filepath.Dir(p); d != dir; d = filepath.Dir(d) {
				if fi, err := os.Stat(d); err != nil || fi.Mode().Perm() != 0o700 {
					t.Fatalf("directory %s: %v %v", d, fi.Mode(), err)
				}
			}
			b, _ := os.ReadFile(p)
			if strings.Contains(string(b), "{{BIN}}") || !strings.Contains(string(b), "/opt/conductor") {
				t.Fatalf("%s:\n%s", rel, b)
			}
			if path.Ext(rel) == ".json" {
				var indented bytes.Buffer
				if err := json.Indent(&indented, b, "", "  "); err != nil {
					t.Fatalf("%s is not JSON: %v", rel, err)
				}
				var compact bytes.Buffer
				json.Compact(&compact, b)
				indented.Reset()
				json.Indent(&indented, compact.Bytes(), "", "  ")
				if indented.String()+"\n" != string(b) {
					t.Fatalf("%s is not indented with two spaces:\n%s", rel, b)
				}
			}
		}
	}
	if count < 12 {
		t.Fatalf("only %d assets", count)
	}
}

// Starting again with the binary somewhere else rewrites the assets.
func TestWriteAssetsFollowsTheBinary(t *testing.T) {
	dir := t.TempDir()
	for _, bin := range []string{"/opt/conductor", "/usr/local/bin/conductor"} {
		if err := WriteAssets(dir, bin); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(dir, "claude.json"))
	if strings.Contains(string(b), "/opt/conductor") || !strings.Contains(string(b), "/usr/local/bin/conductor notify --claude-hook") {
		t.Fatalf("claude.json:\n%s", b)
	}
}

// An asset replaced by a link is not written through; the error is the
// server's to report, not a step for the user.
func TestWriteAssetsRefusesALink(t *testing.T) {
	dir, outside := t.TempDir(), filepath.Join(t.TempDir(), "elsewhere.json")
	os.WriteFile(outside, []byte("{}"), 0o600)
	if err := os.Symlink(outside, filepath.Join(dir, "claude.json")); err != nil {
		t.Fatal(err)
	}
	err := WriteAssets(dir, "/opt/conductor")
	if err == nil || errors.Is(err, ErrByHand) || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("WriteAssets: %v", err)
	}
	if b, _ := os.ReadFile(outside); string(b) != "{}" {
		t.Fatalf("wrote through the link: %s", b)
	}
}

// WriteAssets owns the hooks dir: the directory is 0700 and every asset 0600,
// whatever they were before, even when the content is already right.
func TestWriteAssetsTightensModes(t *testing.T) {
	dir := t.TempDir()
	if err := WriteAssets(dir, "/opt/conductor"); err != nil {
		t.Fatal(err)
	}
	asset := filepath.Join(dir, "claude.json")
	os.Chmod(asset, 0o644)
	os.Chmod(dir, 0o755)
	if err := WriteAssets(dir, "/opt/conductor"); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Fatalf("hooks dir %v", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(asset); fi.Mode().Perm() != 0o600 {
		t.Fatalf("asset %v", fi.Mode().Perm())
	}
}

// The hooks name the conductor on PATH when that is this binary, as a package
// manager's link to a versioned install is: the link survives an upgrade, the
// versioned path does not. Anything else names this binary.
func TestBinaryPath(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	link := t.TempDir()
	if err := os.Symlink(exe, filepath.Join(link, "conductor")); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	os.WriteFile(filepath.Join(other, "conductor"), []byte("#!/bin/sh\n"), 0o755)
	for _, c := range []struct{ name, path, want string }{
		{"a link to this binary", link, filepath.Join(link, "conductor")},
		{"another conductor", other + string(os.PathListSeparator) + link, exe},
		{"none on PATH", t.TempDir(), exe},
	} {
		t.Setenv("PATH", c.path)
		if got, err := BinaryPath(); err != nil || got != c.want {
			t.Errorf("%s: %q %v, want %q", c.name, got, err, c.want)
		}
	}
	// A relative PATH entry is not a path to name in a config file.
	t.Chdir(link)
	t.Setenv("PATH", ".")
	if got, err := BinaryPath(); err != nil || got != exe {
		t.Errorf("relative PATH: %q %v, want %q", got, err, exe)
	}
}

func TestWriteAssetsNeedsAbsolutePaths(t *testing.T) {
	if err := WriteAssets(t.TempDir(), "conductor"); err == nil {
		t.Fatal("a relative binary path was accepted")
	}
	if err := WriteAssets("hooks", "/opt/conductor"); err == nil {
		t.Fatal("a relative hooks dir was accepted")
	}
	if err := WriteAssets(t.TempDir(), "/opt/\xffconductor"); err == nil {
		t.Fatal("a binary path that is not UTF-8 was accepted")
	}
}

func TestAssetPathsAreLocalAndUnique(t *testing.T) {
	seen := map[string]string{}
	for _, a := range All() {
		for rel := range a.Assets {
			if !filepath.IsLocal(rel) || rel != path.Clean(rel) {
				t.Errorf("%s: asset path %q", a.ID, rel)
			}
			if other, dup := seen[rel]; dup {
				t.Errorf("%s and %s both write %s", other, a.ID, rel)
			}
			seen[rel] = a.ID
		}
	}
}

func TestHooksDirs(t *testing.T) {
	if got := HooksDir("/var/lib/conductor"); got != "/var/lib/conductor/hooks" {
		t.Fatalf("HooksDir = %q", got)
	}
	if got := HooksDir(""); got != "" {
		t.Fatalf("HooksDir without a data dir = %q", got)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "/srv/state")
	if got, err := HostHooksDir(); err != nil || got != "/srv/state/conductor/hooks" {
		t.Fatalf("with XDG_STATE_HOME: %q %v", got, err)
	}
	// The spec says to ignore a relative XDG path; so does an empty one.
	for _, v := range []string{"", "state"} {
		t.Setenv("XDG_STATE_HOME", v)
		if got, err := HostHooksDir(); err != nil || got != filepath.Join(home, ".local", "state", "conductor", "hooks") {
			t.Fatalf("XDG_STATE_HOME=%q: %q %v", v, got, err)
		}
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"/opt/conductor":          "/opt/conductor",
		"/usr/local/bin/cond-2.1": "/usr/local/bin/cond-2.1",
		"/opt/my apps/conductor":  "'/opt/my apps/conductor'",
		`/opt/it's/conductor`:     `'/opt/it'\''s/conductor'`,
		`/opt/$HOME/c`:            `'/opt/$HOME/c'`,
		"":                        "''",
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

// oddBinary makes a directory whose name has a space, both quotes and a
// backslash, with a stand-in for conductor in it that waits $ARGS_DELAY
// seconds, then writes its arguments, one per line, to $ARGS_OUT.
func oddBinary(t *testing.T) (bin, out string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), `my "odd' dir\`)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	bin = filepath.Join(dir, "conductor")
	script := "#!/bin/sh\nsleep \"${ARGS_DELAY:-0}\"\nprintf '%s\\n' \"$@\" > \"$ARGS_OUT.tmp\" && mv \"$ARGS_OUT.tmp\" \"$ARGS_OUT\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin, filepath.Join(t.TempDir(), "args")
}

// waitArgs waits for the stand-in to have written its arguments.
func waitArgs(t *testing.T, out string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if b, err := os.ReadFile(out); err == nil {
			return string(b)
		}
		if time.Now().After(deadline) {
			t.Fatal("the binary did not run")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// runShell runs command the way hook runners do, with sh -c, and returns the
// arguments the stand-in received.
func runShell(t *testing.T, command, out string) string {
	t.Helper()
	os.Remove(out)
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Env = append(os.Environ(), "ARGS_OUT="+out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sh -c %q: %v %s", command, err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("sh -c %q did not run the binary: %v", command, err)
	}
	return string(b)
}

// commands returns every string in a JSON document that runs conductor notify.
func commands(v any) []string {
	var out []string
	switch v := v.(type) {
	case string:
		if strings.Contains(v, " notify --") {
			out = append(out, v)
		}
	case []any:
		for _, e := range v {
			out = append(out, commands(e)...)
		}
	case map[string]any:
		for _, e := range v {
			out = append(out, commands(e)...)
		}
	}
	return out
}

var jsConst = regexp.MustCompile(`(?m)^const CONDUCTOR = (".*");$`)

// The binary path is the one thing in an asset that is not fixed text. A path
// with a space, both quotes and a backslash leaves every asset well formed,
// and every hook, launch flag and environment value still runs that binary.
func TestAssetsEscapeTheBinaryPath(t *testing.T) {
	bin, out := oddBinary(t)
	hooks := t.TempDir()
	if err := WriteAssets(hooks, bin); err != nil {
		t.Fatal(err)
	}
	for _, a := range All() {
		for rel := range a.Assets {
			b, err := os.ReadFile(filepath.Join(hooks, filepath.FromSlash(rel)))
			if err != nil || strings.Contains(string(b), "{{BIN}}") {
				t.Fatalf("%s: %v\n%s", rel, err, b)
			}
			switch path.Ext(rel) {
			case ".json":
				var doc any
				if err := json.Unmarshal(b, &doc); err != nil {
					t.Fatalf("%s: %v\n%s", rel, err, b)
				}
				cmds := commands(doc)
				if len(cmds) == 0 {
					t.Fatalf("%s runs no command", rel)
				}
				for _, c := range cmds {
					_, flag, _ := strings.Cut(c, " notify ")
					if got := runShell(t, c, out); got != "notify\n"+flag+"\n" {
						t.Fatalf("%s: %q ran with %q", rel, c, got)
					}
				}
			case ".ts", ".js":
				m := jsConst.FindSubmatch(b)
				var got string
				if m == nil || json.Unmarshal(m[1], &got) != nil || got != bin {
					t.Fatalf("%s: CONDUCTOR is %q, want %q\n%s", rel, got, bin, b)
				}
			default:
				t.Fatalf("%s: no check for this kind of asset", rel)
			}
		}
	}

	useBin(t, bin)
	hook := catalog.Signal{Kind: "hook"}
	argv, _ := InjectFor("codex", hooks, hook)
	var notify []string
	value, ok := strings.CutPrefix(argv[1], "notify=")
	if !ok || json.Unmarshal([]byte(value), &notify) != nil || !slices.Equal(notify, []string{bin, "notify", "--codex"}) {
		t.Fatalf("codex notify %q", argv)
	}
	_, env := InjectFor("aider", hooks, hook)
	if got := runShell(t, env["AIDER_NOTIFICATIONS_COMMAND"], out); got != "notify\n--state\nneeds_input\n--message\naider is waiting\n" {
		t.Fatalf("aider command ran with %q", got)
	}
	// The snippet a user pastes into a shell sets the same command.
	a, _ := Get("aider")
	if got := runShell(t, a.Snippet(hooks)+"\n/bin/sh -c \"$AIDER_NOTIFICATIONS_COMMAND\"", out); got != "notify\n--state\nneeds_input\n--message\naider is waiting\n" {
		t.Fatalf("aider snippet ran with %q", got)
	}

	home := t.TempDir()
	codex, _ := Get("codex")
	if _, err := codex.Install(home, hooks); err != nil {
		t.Fatal(err)
	}
	config, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	line := regexp.MustCompile(`(?m)^notify = (\[.*\])$`).FindSubmatch(config)
	if line == nil || json.Unmarshal(line[1], &notify) != nil || !slices.Equal(notify, []string{bin, "notify", "--codex"}) {
		t.Fatalf("config.toml:\n%s", config)
	}
}

// With node at hand, every script asset loads as a module with an odd binary
// path, registers its handlers with the agent and, when one fires, runs that
// binary without waiting for it: a slow or unreachable server must never hold
// up the agent.
func TestScriptAssetsRunInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	bin, out := oddBinary(t)
	hooks := t.TempDir()
	if err := WriteAssets(hooks, bin); err != nil {
		t.Fatal(err)
	}
	// The harness hands a plugin a fake agent: a default export gets an
	// object with on(), and its first handler is called; OpenCode's plugin gets
	// a fake Bun shell, and its event handler is told the session is idle.
	const harness = `
const mod = await import(process.argv[1]);
if (typeof mod.default === "function") {
  const handlers = [];
  mod.default({ on: (name, fn) => handlers.push([name, fn]) });
  const start = Date.now();
  await handlers[0][1]({});
  console.log(JSON.stringify({ on: handlers.map(([name]) => name), ms: Date.now() - start }));
} else {
  const calls = [];
  const $ = (strings, ...values) => { calls.push(values); const r = { quiet: () => r, nothrow: () => r, then: (ok) => ok() }; return r; };
  const plugin = Object.values(mod).find((v) => typeof v === "function");
  const hooks = await plugin({ $ });
  await hooks.event({ event: { type: "session.idle" } });
  console.log(JSON.stringify({ calls }));
}
`
	firstHandler := map[string]string{
		"pi-conductor.ts":        "agent_end",
		"omp-conductor.ts":       "agent_end",
		"amp/conductor/index.ts": "agent.end",
		"dsh-conductor.js":       "session-completed",
	}
	for _, a := range All() {
		for rel := range a.Assets {
			if ext := path.Ext(rel); ext != ".ts" && ext != ".js" {
				continue
			}
			src, _ := os.ReadFile(filepath.Join(hooks, filepath.FromSlash(rel)))
			mod := filepath.Join(t.TempDir(), "plugin.mjs")
			os.WriteFile(mod, src, 0o600)
			os.Remove(out)
			cmd := exec.Command(node, "--input-type=module", "-e", harness, (&url.URL{Scheme: "file", Path: mod}).String())
			// The stand-in takes a second: the handler must not wait for it.
			cmd.Env = append(os.Environ(), "ARGS_OUT="+out, "ARGS_DELAY=1")
			stdout, err := cmd.Output()
			if err != nil {
				t.Fatalf("%s: %v\n%s", rel, err, stdout)
			}
			var res struct {
				On    []string `json:"on"`
				MS    int      `json:"ms"`
				Calls [][]any  `json:"calls"`
			}
			if err := json.Unmarshal(stdout, &res); err != nil {
				t.Fatalf("%s: %v %s", rel, err, stdout)
			}
			if first, ok := firstHandler[rel]; ok {
				if len(res.On) == 0 || res.On[0] != first || res.MS > 500 {
					t.Fatalf("%s: handlers %v, the first took %d ms", rel, res.On, res.MS)
				}
				if args := waitArgs(t, out); args != "notify\n--state\ndone\n" {
					t.Fatalf("%s: the binary ran with %q", rel, args)
				}
				if strings.Contains(string(src), "spawnSync") {
					t.Fatalf("%s still waits for the binary", rel)
				}
				continue
			}
			// OpenCode: the command is the binary, then notify's arguments.
			if len(res.Calls) != 1 || len(res.Calls[0]) != 2 || res.Calls[0][0] != bin {
				t.Fatalf("%s: shell calls %v", rel, res.Calls)
			}
			if args, _ := json.Marshal(res.Calls[0][1]); string(args) != `["--state","done"]` {
				t.Fatalf("%s: notify arguments %s", rel, args)
			}
		}
	}
}
