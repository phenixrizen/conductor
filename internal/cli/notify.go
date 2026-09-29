package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/phenixrizen/conductor/internal/notify"
)

// runNotify reports an attention state, or with --event something the agent
// did, from inside a Conductor session. It is designed for agent hooks:
// outside a session it exits 0 without output.
func runNotify(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("notify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	state := fs.String("state", "needs_input", "needs_input, working, done or clear")
	message := fs.String("message", "", "short message shown next to the badge")
	event := fs.String("event", "", "report an event instead of a state: progress, artifact, handoff, tool_use, tool_denied or error")
	link := fs.String("url", "", "with --event artifact: where the result lives")
	to := fs.String("to", "", "with --event handoff: who the work goes to")
	tool := fs.String("tool", "", "with --event tool_use, tool_denied or error: the tool involved")
	claudeHook := fs.Bool("claude-hook", false, "read a Claude Code hook payload from stdin and map it")
	codex := fs.Bool("codex", false, "read a Codex notify payload from the last argument and map it")
	quiet := fs.Bool("quiet", true, "exit 0 silently when not running inside a conductor session")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: conductor notify [--state S] [--message M] | --event E [--message M] [--url U] [--to T] [--tool N] | --claude-hook | --codex <json>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		return 2, err
	}
	if *event != "" {
		// --state has a default, so only Visit can tell that it was given.
		stateGiven := false
		fs.Visit(func(f *flag.Flag) { stateGiven = stateGiven || f.Name == "state" })
		var with []string
		for _, c := range []struct {
			flag string
			set  bool
		}{{"--state", stateGiven}, {"--claude-hook", *claudeHook}, {"--codex", *codex}} {
			if c.set {
				with = append(with, c.flag)
			}
		}
		if len(with) > 0 {
			return 2, fmt.Errorf("--event cannot be combined with %s", strings.Join(with, " or "))
		}
	}
	url, token, err := notify.FromEnv(os.Getenv)
	if err != nil {
		if *quiet {
			return 0, nil
		}
		return 1, err
	}
	req := notify.Request{State: *state, Message: *message}
	if *event != "" {
		req = notify.Request{Event: *event, Message: *message, URL: *link, To: *to, Tool: *tool}
	}
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
