package api

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/config"
	"github.com/phenixrizen/conductor/internal/crew/ledger"
)

// A command's receipt is read by the admin alone, by its run and id: 200
// with the receipt, 404 for a run or id unknown or another actor's, 400 for
// an id that is not one. The route never acts.
func TestACommandsReceiptIsReadBackWithoutActing(t *testing.T) {
	e := newTestEnv(t, nil)
	if e.srv.ledger == nil {
		t.Fatal("no ledger")
	}
	if ro, why := e.srv.ledger.ReadOnly(); ro {
		t.Fatalf("the ledger is read-only: %s", why)
	}
	ctx := context.Background()
	if _, err := e.srv.ledger.Transition(ctx, "team-00000001", func(tx *ledger.Tx) error {
		if err := tx.PutRun(ledger.RunRow{ID: "team-00000001", CrewID: "team", State: ledger.RunRunning, CreatedAt: tx.Now()}); err != nil {
			return err
		}
		if _, _, err := tx.Command(adminActor, "c1", "task.retry", "fp"); err != nil {
			return err
		}
		if err := tx.Settle(adminActor, "c1", ledger.CommandDone, map[string]string{"task": "build"}); err != nil {
			return err
		}
		_, _, err := tx.Command("agent:s1", "c2", "task.result", "fp")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	resp, out := e.do("GET", "/api/runs/team-00000001/commands/c1", adminToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %v", resp.StatusCode, out)
	}
	c, _ := out["command"].(map[string]any)
	if c["status"] != ledger.CommandDone || c["intent"] != "task.retry" || c["receipt"].(map[string]any)["task"] != "build" {
		t.Fatalf("%v", out)
	}
	for _, path := range []string{"/api/runs/team-00000001/commands/c2", "/api/runs/team-00000001/commands/c9", "/api/runs/team-00000009/commands/c1"} {
		if resp, out := e.do("GET", path, adminToken, nil); resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s: %d %v", path, resp.StatusCode, out)
		}
	}
	if resp, out := e.do("GET", "/api/runs/team-00000001/commands/c%201", adminToken, nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a bad id: %d %v", resp.StatusCode, out)
	}
	if resp, _ := e.do("GET", "/api/runs/team-00000001/commands/c1", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
	if runs, err := e.srv.ledger.Runs(ctx); err != nil || len(runs) != 1 || runs[0].Revision != 1 {
		t.Fatalf("the read acted: %v %v", runs, err)
	}
}

// A damaged ledger in the data directory never keeps the server from
// starting: the legacy crews run, the receipts answer 503.
func TestADamagedLedgerDoesNotStopTheServer(t *testing.T) {
	e := newTestEnv(t, func(cfg *config.Config) {
		cfg.DataDir = filepath.Join(t.TempDir(), "data")
		runs := filepath.Join(cfg.DataDir, "runs")
		if err := os.MkdirAll(runs, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(runs, ledger.File), []byte(strings.Repeat("\xff", 4096)), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	if e.srv.ledger == nil {
		t.Fatal("no ledger")
	}
	if ro, _ := e.srv.ledger.ReadOnly(); !ro {
		t.Fatal("a damaged ledger opened for writing")
	}
	if resp, out := e.do("GET", "/api/runs/team-00000001/commands/c1", adminToken, nil); resp.StatusCode != http.StatusServiceUnavailable {
		// Nothing can be read from it: unavailable, not absent.
		t.Fatalf("%d %v", resp.StatusCode, out)
	}
	if resp, _ := e.do("GET", "/api/crews", adminToken, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("the legacy API: %d", resp.StatusCode)
	}
}

// A ledger that cannot be opened at all (its file a symbolic link) leaves
// the server running with none; a receipt is then unavailable, not absent.
func TestALedgerThatCannotOpenAnswersUnavailable(t *testing.T) {
	e := newTestEnv(t, func(cfg *config.Config) {
		cfg.DataDir = filepath.Join(t.TempDir(), "data")
		runs := filepath.Join(cfg.DataDir, "runs")
		if err := os.MkdirAll(runs, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(runs, "elsewhere.sqlite"), filepath.Join(runs, ledger.File)); err != nil {
			t.Fatal(err)
		}
	})
	if e.srv.ledger != nil {
		t.Fatal("a ledger opened on a symbolic link")
	}
	resp, out := e.do("GET", "/api/runs/team-00000001/commands/c1", adminToken, nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("%d %v", resp.StatusCode, out)
	}
	if resp, _ := e.do("GET", "/api/crews", adminToken, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("the legacy API: %d", resp.StatusCode)
	}
}
