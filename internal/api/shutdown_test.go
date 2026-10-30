package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rocksolidlabs/conductor/internal/crew"
	"github.com/rocksolidlabs/conductor/internal/crew/ledger"
)

// Every run's record is written before the server exits, however its
// shutdown budget stands (#128): the ends the stops hand the records are
// waited for, within a grace of the records' own, before the records close.
// Twenty runs stopped under a budget already spent all have their records,
// each stopped.
func TestShutdownWritesEveryRunsRecordWhenItsBudgetIsSpent(t *testing.T) {
	e := newTestEnv(t, nil)
	e.stopEverything(t)
	const n = 20
	for i := range n {
		e.launchCrew(t, fmt.Sprintf("Crew %d", i), catMember("core", "immediately"))
	}
	// A budget already spent: every stop is cut short at once.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e.srv.Shutdown(ctx)
	got, err := e.srv.records.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != n {
		t.Fatalf("%d of %d records written", len(got), n)
	}
	for _, r := range got {
		if r.StoppedAt == nil {
			t.Fatalf("record %s is not stopped: %s", r.ID, r.State)
		}
	}
}

// A run whose tree has a service that ignores SIGTERM: Server.Shutdown,
// under the CLI's budget of 10 s, ends it (SIGTERM, then SIGKILL after the
// grace, or at the teardown's cut-off armed at the shutdown's entry), and
// its pid is gone when Shutdown returns; the service's end and the tree's
// are in the ledger, written before it closed.
func TestShutdownEndsAServiceThatIgnoresSIGTERM(t *testing.T) {
	e := readyEnv(t, nil)
	e.gitRepo(t)
	pidFile := filepath.Join(t.TempDir(), "pid")
	c := crew.Crew{ID: "served", Name: "Served", Goal: "build it", Cwd: e.root, Where: crew.WhereServer, Isolation: crew.IsolationWorktree,
		Members:  []crew.Member{{Name: "lead", AgentID: "ready-cat", Start: crew.Start{When: crew.StartTask}}},
		Tasks:    []crew.Task{{ID: "build", Member: "lead", Kind: crew.TaskWork, Instructions: "Build it."}},
		Manifest: &crew.Manifest{Services: []crew.Check{{ID: "web", Argv: []string{"sh", "-c", `trap "" TERM; echo $$ > "` + pidFile + `"; while :; do sleep 0.05; done`}}}}}
	run, release, err := e.srv.runs.LaunchNamed(t.Context(), c, "")
	release()
	if err != nil {
		t.Fatal(err)
	}
	runningAttempt(t, e, run.ID, "build", 1)
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || syscall.Kill(pid, 0) != nil {
		t.Fatalf("the service is not running: %q %v", b, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	e.srv.Shutdown(ctx)
	if syscall.Kill(pid, 0) == nil {
		t.Fatalf("the service's process %d outlived the shutdown", pid)
	}
	ld, err := ledger.Open(filepath.Join(e.srv.cfg.DataDir, "runs"))
	if err != nil {
		t.Fatal(err)
	}
	defer ld.Close()
	rows, err := ld.Checks(context.Background(), run.ID, "build", 1, false)
	if err != nil || len(rows) != 1 || rows[0].Phase != ledger.PhaseService || rows[0].State != ledger.CheckFinished || rows[0].Signal != "killed" {
		t.Fatalf("the service's row after the shutdown: %+v %v", rows, err)
	}
	trees, err := ld.Trees(context.Background(), run.ID)
	if err != nil || len(trees) != 1 || trees[0].State != ledger.TreeEnded || trees[0].Interrupted {
		t.Fatalf("the tree after the shutdown: %+v %v", trees, err)
	}
}

// A run whose sole task was cancelled while a prerequisite was held at its
// gate is finished, the prerequisites' generation ended by that commit;
// the shutdown's stop loop stops it as any other (its StoppedAt is nil),
// and the step's process is gone and its row interrupted before the
// ledger closes.
func TestShutdownEndsAFinishedRunsPrerequisitesBeforeTheLedgerCloses(t *testing.T) {
	e := readyEnv(t, nil)
	e.gitRepo(t)
	dir := t.TempDir()
	gate, pidFile := filepath.Join(dir, "gate"), filepath.Join(dir, "pid")
	c := crew.Crew{ID: "gated", Name: "Gated", Goal: "build it", Cwd: e.root, Where: crew.WhereServer, Isolation: crew.IsolationWorktree,
		Members:  []crew.Member{{Name: "lead", AgentID: "ready-cat", Start: crew.Start{When: crew.StartTask}}},
		Tasks:    []crew.Task{{ID: "build", Member: "lead", Kind: crew.TaskWork, Instructions: "Build it."}},
		Manifest: &crew.Manifest{Prerequisites: []crew.Check{{ID: "tools", Argv: []string{"sh", "-c", `echo $$ > "` + pidFile + `"; while [ ! -e "` + gate + `" ]; do sleep 0.05; done`}}}}}
	run, release, err := e.srv.runs.LaunchNamed(t.Context(), c, "")
	release()
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	deadline := time.Now().Add(20 * time.Second)
	for pid == 0 {
		if b, err := os.ReadFile(pidFile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		if pid == 0 && time.Now().After(deadline) {
			t.Fatal("the prerequisite did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if resp, out := e.do("POST", "/api/runs/"+run.ID+"/tasks/build/cancel", adminToken, map[string]any{"commandId": "c1"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel: %d %v", resp.StatusCode, out)
	}
	if r, _ := e.srv.runs.Get(run.ID); r.Outcome != "cancelled" || r.StoppedAt != nil {
		t.Fatalf("the run after the cancel: outcome %q, stopped %v", r.Outcome, r.StoppedAt)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	e.srv.Shutdown(ctx)
	if syscall.Kill(pid, 0) == nil {
		t.Fatalf("the prerequisite's process %d outlived the shutdown", pid)
	}
	ld, err := ledger.Open(filepath.Join(e.srv.cfg.DataDir, "runs"))
	if err != nil {
		t.Fatal(err)
	}
	defer ld.Close()
	rows, err := ld.Prerequisites(context.Background(), run.ID, false)
	if err != nil || len(rows) != 1 || rows[0].State != ledger.CheckInterrupted || !strings.Contains(rows[0].Error, "tools was interrupted") {
		t.Fatalf("the prerequisite's row after the shutdown: %+v %v", rows, err)
	}
}
