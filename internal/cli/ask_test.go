package cli

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/agents"
	"github.com/phenixrizen/conductor/internal/catalog"
)

// aider's notification command, as its launch sets it, run as aider runs
// it (the shell reads its words), reports aider's wait without the mark
// (#193): a program's wait gives way to the turn's done, as before.
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
	if ns.body["state"] != "needs_input" || ns.body["message"] != "aider is waiting" {
		t.Fatalf("body %v", ns.body)
	}
	if _, marked := ns.body["ask"]; marked {
		t.Fatalf("aider's wait is marked as the agent's own question: %v", ns.body)
	}
}
