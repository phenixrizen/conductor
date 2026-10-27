package cli

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/rocksolidlabs/conductor/internal/agents"
	"github.com/rocksolidlabs/conductor/internal/catalog"
)

// aider's notification command, as its launch sets it, run as aider runs
// it (the shell reads its words), reports aider waiting for its next line
// as done, with no mark (#193): keys typed in the terminal answer no
// question, so a wait reported would stay until aider's next report.
func TestAidersNotificationReportsNoMark(t *testing.T) {
	ns := startNotifyServer(t)
	a, ok := agents.Get("aider")
	if !ok {
		t.Fatal("no aider adapter")
	}
	_, env := a.Inject(t.TempDir(), catalog.Signal{})
	cmd := env["AIDER_NOTIFICATIONS_COMMAND"]
	_, rest, found := strings.Cut(cmd, " notify ")
	if !found {
		t.Fatalf("aider's command %q", cmd)
	}
	// The shell's words, as aider's shell reads them.
	out, err := exec.Command("/bin/sh", "-c", `printf '%s\n' `+rest).Output()
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if code, stderr, err := runNotifyWith(t, "", args...); code != 0 || err != nil || ns.calls != 1 {
		t.Fatalf("%q: exit %d %v %q", args, code, err, stderr)
	}
	if ns.body["state"] != "done" || ns.body["message"] != "aider is waiting" {
		t.Fatalf("body %v", ns.body)
	}
	if _, marked := ns.body["ask"]; marked {
		t.Fatalf("aider's wait is marked as the agent's own question: %v", ns.body)
	}
}
