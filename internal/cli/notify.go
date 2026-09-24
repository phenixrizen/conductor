package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/phenixrizen/conductor/internal/notify"
)

// runNotify reports an attention state from inside a Conductor session. It is
// designed for agent hooks: outside a session it exits 0 without output.
func runNotify(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("notify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	state := fs.String("state", "needs_input", "needs_input, working, done or clear")
	message := fs.String("message", "", "short message shown next to the badge")
	claudeHook := fs.Bool("claude-hook", false, "read a Claude Code hook payload from stdin and map it")
	codex := fs.Bool("codex", false, "read a Codex notify payload from the last argument and map it")
	quiet := fs.Bool("quiet", true, "exit 0 silently when not running inside a conductor session")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: conductor notify [--state S] [--message M] | --claude-hook | --codex <json>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		return 2, err
	}
	url, token, err := notify.FromEnv(os.Getenv)
	if err != nil {
		if *quiet {
			return 0, nil
		}
		return 1, err
	}
	req := notify.Request{State: *state, Message: *message}
	switch {
	case *claudeHook:
		raw, err := notify.ReadAllBounded(stdin)
		if err != nil {
			return 1, err
		}
		mapped, ok := notify.MapClaudeHook(raw)
		if !ok {
			return 0, nil
		}
		req = mapped
	case *codex:
		rest := fs.Args()
		if len(rest) == 0 {
			return 0, nil
		}
		mapped, ok := notify.MapCodex([]byte(rest[len(rest)-1]))
		if !ok {
			return 0, nil
		}
		req = mapped
	}
	if err := notify.Send(ctx, url, token, req); err != nil {
		if *quiet {
			// Hooks must never break the agent because Conductor is unreachable.
			fmt.Fprintln(stderr, "conductor notify:", err)
			return 0, nil
		}
		return 1, err
	}
	return 0, nil
}
