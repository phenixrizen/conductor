package acceptance_test

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestTerminalHarnessLifecycle(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_TERMINAL") != "1" {
		t.Skip("set CONDUCTOR_TEST_TERMINAL=1 for terminal harness regressions")
	}
	python := os.Getenv("CONDUCTOR_TERMINAL_PYTHON")
	if python == "" {
		python = "python3"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, python, "../terminal/harness_test.py")
	command.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("terminal harness lifecycle regression: %v\n%s", err, output)
	}
}
