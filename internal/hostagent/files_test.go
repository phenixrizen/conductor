package hostagent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/config"
	"github.com/phenixrizen/conductor/internal/gitcli"
	"github.com/phenixrizen/conductor/internal/proto"
)

// expectFile waits for the FILE frame answering reqID.
func (v *viewer) expectFile(reqID string) (proto.FileHeader, []byte) {
	v.t.Helper()
	for {
		f, err := v.read()
		if err != nil {
			v.t.Fatalf("waiting for the file reply %s: %v", reqID, err)
		}
		if f.Type != proto.TypeFile {
			continue
		}
		h, body, err := proto.DecodeFile(f.Payload)
		if err != nil {
			v.t.Fatalf("file reply %s: %v", reqID, err)
		}
		if h.ReqID == reqID {
			return h, body
		}
	}
}

// homeWithConductor is a home directory holding what a conductor serve keeps
// on this machine, every file carrying secret, all committed to a git
// repository there, and an ordinary notes.txt and package.json beside them.
// It returns the home and the config file's path.
func homeWithConductor(t *testing.T, secret string) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	home := t.TempDir()
	cfg := filepath.Join(home, "conductor.json")
	files := map[string]string{
		".conductor/catalog.json":    `{"agents":[{"id":"x","env":{"TOKEN":"` + secret + `"}}]}`,
		".conductor/workbench-token": secret + "\n",
		".conductor/crews/c1.json":   `{"note":"` + secret + `"}`,
		"conductor.json":             `{"workbenchToken":"` + secret + `","catalogPath":"` + filepath.Join(home, "agents.json") + `"}`,
		"conductor.json.bak":         secret,
		".conductor.json.swp":        secret,
		"#conductor.json#":           secret,
		"agents.json":                `{"agents":[{"id":"y","env":{"KEY":"` + secret + `"}}]}`,
		"agents.json~":               secret,
		// The desktop app's: its own directory under either name, the data
		// directory its settings name and, inside WSL, its server's data.
		".config/conductor-desktop/settings.json":          `{"switchyardToken":"` + secret + `","dataDir":"` + filepath.Join(home, "code", "conductor-state") + `"}`,
		".config/conductor-desktop/conductor/catalog.json": secret,
		"code/conductor-state/catalog.json":                secret,
		"code/app/main.go":                                 "package main\n",
		".config/Conductor/conductor/catalog.json":         secret,
		// Its settings under the other name are a link to dotfiles.
		"dotfiles/desktop.json":                     `{"switchyardToken":"` + secret + `"}`,
		"dotfiles/desktop.json.bak":                 secret,
		"dotfiles/zshrc":                            "ordinary\n",
		".local/share/conductor/data/catalog.json":  secret,
		".local/share/conductor/data/crews/c2.json": secret,
		".config/other-app/settings.json":           "ordinary\n",
		".local/share/other-app/data.json":          "ordinary\n",
		"notes.txt":                                 "ordinary\n",
		"package.json":                              `{"name":"ordinary"}`,
		"project/src/conductor-ui.md":               "ordinary too\n",
	}
	for rel, body := range files {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(home, "dotfiles", "desktop.json"), filepath.Join(home, ".config", "Conductor", "settings.json")); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "t"}, {"config", "user.email", "t@t"}, {"config", "commit.gpgsign", "false"}, {"add", "-A"}, {"commit", "-q", "-m", "home"}} {
		if _, err := gitcli.Run(context.Background(), home, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	return home, cfg
}

// A hosted session whose folder holds what a conductor serve on the same
// machine keeps (its data directory, ~/.conductor, its config file and its
// catalog file, the copies an editor leaves beside the two files, and the
// desktop app's directories) refuses them to a view link as a server session
// refuses them: a read, a stat, a find and a git show, under every spelling
// of the path. The rest of the folder is served as before.
func TestHostRefusesTheServersFilesOnItsMachine(t *testing.T) {
	const secret = "s3cret-value-9f2"
	t.Setenv("CONDUCTOR_DATA_DIR", "")
	t.Setenv("CONDUCTOR_CATALOG_PATH", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	home, cfgPath := homeWithConductor(t, secret)
	t.Setenv("HOME", home)
	deny, err := config.LocalFileDeny(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	srv, hs := startServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	registered := make(chan string, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := Run(ctx, Options{
			ServerURL: hs.URL,
			Token:     hostToken,
			Name:      "home",
			Argv:      []string{"/bin/cat"},
			Dir:       home,
			RelayOnly: true,
			FileView:  "view",
			FileDeny:  deny,
			Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
			Registered: func(id, base string) {
				registered <- id
			},
		})
		if err != nil {
			t.Errorf("run: %v", err)
		}
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("host did not stop")
		}
	})
	var sessionID string
	select {
	case sessionID = <-registered:
	case <-time.After(10 * time.Second):
		t.Fatal("host did not register")
	}

	_, tok, err := srv.Links().Create(sessionID, "view", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	v := dialViewer(t, hs.URL, sessionID, tok)
	v.expectJSON(proto.TypeControl, proto.CtlWelcome)
	relay, _ := proto.EncodeJSON(proto.TypeSignal, proto.RelayRequest{T: proto.SigRelay, Reason: "forced"})
	v.send(relay)
	v.expectJSON(proto.TypeSignal, proto.SigRelayOK)
	v.send(proto.MustControl(proto.Hello{T: proto.CtlHello, Proto: 1, Cols: 80, Rows: 24}))
	if w := v.expectJSON(proto.TypeControl, proto.CtlWelcome); w["role"] != "view" || w["fileView"] != true {
		t.Fatalf("welcome %v", w)
	}
	v.expectJSON(proto.TypeControl, proto.CtlReady)

	n := 0
	ask := func(req proto.FileGet) (proto.FileHeader, []byte) {
		t.Helper()
		n++
		req.T, req.ReqID = proto.CtlFileGet, "r"+strconv.Itoa(n)
		v.send(proto.MustControl(req))
		return v.expectFile(req.ReqID)
	}
	refused := []string{
		".conductor", ".conductor/", ".conductor/catalog.json", ".conductor/workbench-token", ".conductor/crews/c1.json",
		"~/.conductor/catalog.json", filepath.Join(home, ".conductor", "workbench-token"), "project/../.conductor/catalog.json",
		"conductor.json", "~/conductor.json", "CONDUCTOR.JSON.bak", "conductor.json.bak", ".conductor.json.swp", "#conductor.json#",
		"agents.json", "agents.json~",
		".config/conductor-desktop", ".config/conductor-desktop/settings.json", ".config/conductor-desktop/conductor/catalog.json",
		"code/conductor-state", "code/conductor-state/catalog.json",
		".config/Conductor", ".config/Conductor/settings.json", ".config/Conductor/conductor/catalog.json",
		"dotfiles/desktop.json", "dotfiles/desktop.json.bak", "dotfiles/.desktop.json.swp",
		".local/share/conductor/data/catalog.json", ".local/share/conductor/data/crews/c2.json",
	}
	for _, p := range refused {
		for _, req := range []proto.FileGet{{Path: p}, {Path: p, Stat: true}, {Path: p, Op: proto.FileOpShow, Rev: "HEAD"}} {
			h, body := ask(req)
			if h.Kind != "error" || h.Error == nil || h.Error.Code != "denied" || len(body) != 0 || strings.Contains(string(body), secret) {
				t.Errorf("%+v: %+v %q, want it refused", req, h, body)
			}
		}
	}
	for _, q := range []string{"catalog", "token", "conductor", "json", "agents", "crews", "c1", "c2", "settings", "desktop"} {
		h, _ := ask(proto.FileGet{Op: proto.FileOpFind, Path: q})
		if h.Kind != "find" {
			t.Fatalf("find %q: %+v", q, h)
		}
		for _, m := range h.Matches {
			if strings.Contains(m, ".conductor") || strings.Contains(strings.ToLower(filepath.Base(m)), "conductor.json") || strings.HasPrefix(filepath.Base(m), "agents.json") ||
				strings.HasPrefix(m, ".config/Conductor") || strings.HasPrefix(m, ".config/conductor-desktop") || strings.HasPrefix(m, ".local/share/conductor") || strings.HasPrefix(m, "code/conductor-state") || strings.HasPrefix(m, "dotfiles/desktop.json") {
				t.Errorf("find %q found %s", q, m)
			}
		}
		if q == "json" && !slices.Contains(h.Matches, "package.json") {
			t.Errorf("find json: %v, want package.json among them", h.Matches)
		}
		if q == "settings" && !slices.Contains(h.Matches, ".config/other-app/settings.json") {
			t.Errorf("find settings: %v, want .config/other-app/settings.json among them", h.Matches)
		}
		if q == "conductor" && !slices.Contains(h.Matches, "project/src/conductor-ui.md") {
			t.Errorf("find conductor: %v, want project/src/conductor-ui.md among them", h.Matches)
		}
	}

	// The rest of the folder: read, stat, show, and the listing.
	if h, body := ask(proto.FileGet{Path: "notes.txt"}); h.Kind != "file" || string(body) != "ordinary\n" {
		t.Errorf("notes.txt: %+v %q", h, body)
	}
	for p, want := range map[string]string{"code/app/main.go": "package main\n", "dotfiles/zshrc": "ordinary\n"} {
		if h, body := ask(proto.FileGet{Path: p}); h.Kind != "file" || string(body) != want {
			t.Errorf("%s: %+v %q", p, h, body)
		}
	}
	if h, _ := ask(proto.FileGet{Path: "package.json", Stat: true}); h.Kind != "file" || !h.Exists {
		t.Errorf("stat package.json: %+v", h)
	}
	if h, body := ask(proto.FileGet{Path: "notes.txt", Op: proto.FileOpShow, Rev: "HEAD"}); h.Kind != "show" || string(body) != "ordinary\n" {
		t.Errorf("show notes.txt: %+v %q", h, body)
	}
	h, body := ask(proto.FileGet{Path: "."})
	if h.Kind != "dir" || strings.Contains(string(body), secret) {
		t.Fatalf("the folder: %+v", h)
	}
	raw, _ := json.Marshal(h.Entries)
	if !strings.Contains(string(raw), `"notes.txt"`) {
		t.Errorf("the folder's entries: %s", raw)
	}
}
