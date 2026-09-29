package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
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
	codex := fs.Bool("codex", false, "read a Codex notify payload from the last argument and map it")
	// Each agent's hooks write their payload to stdin; the flag says whose it is.
	hooks := []struct {
		flag    string
		set     *bool
		mapHook func([]byte) (notify.Request, bool)
	}{
		{"--claude-hook", fs.Bool("claude-hook", false, "read a Claude Code hook payload from stdin and map it"), notify.MapClaudeHook},
		{"--codex-hook", fs.Bool("codex-hook", false, "read a Codex hooks.json payload from stdin and map it"), notify.MapCodexHook},
		{"--copilot-hook", fs.Bool("copilot-hook", false, "read a GitHub Copilot CLI hook payload from stdin and map it"), notify.MapCopilotHook},
		{"--cursor-hook", fs.Bool("cursor-hook", false, "read a Cursor CLI hook payload from stdin and map it"), notify.MapCursorHook},
		{"--agy-hook", fs.Bool("agy-hook", false, "read an Antigravity hook payload from stdin and map it"), notify.MapAgyHook},
		{"--goose-hook", fs.Bool("goose-hook", false, "read a Goose hook payload from stdin and map it"), notify.MapGooseHook},
	}
	quiet := fs.Bool("quiet", true, "exit 0 silently when not running inside a conductor session")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: conductor notify [--state S] [--message M] | --event E [--message M] [--url U] [--to T] [--tool N] | --<agent>-hook | --codex <json>")
		fs.PrintDefaults()
	}
	// Agents' hooks run this command, and a hook runner reads exit 2 as
	// "block the agent": a mistake in a hook's command line exits 1.
	misuse := 2
	if hookMode(args) {
		misuse = 1
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		return misuse, err
	}
	// The payload flags given, in the order of the usage: at most one may be.
	var payload []string
	for _, h := range hooks {
		if *h.set {
			payload = append(payload, h.flag)
		}
	}
	if *codex {
		payload = append(payload, "--codex")
	}
	if len(payload) > 1 {
		return misuse, fmt.Errorf("%s cannot be combined: a payload is read one way", strings.Join(payload, " and "))
	}
	if *event != "" {
		// --state has a default, so only Visit can tell that it was given.
		stateGiven := false
		fs.Visit(func(f *flag.Flag) { stateGiven = stateGiven || f.Name == "state" })
		with := payload
		if stateGiven {
			with = append([]string{"--state"}, payload...)
		}
		if len(with) > 0 {
			return misuse, fmt.Errorf("--event cannot be combined with %s", strings.Join(with, " or "))
		}
	}
	url, token, err := notify.FromEnv(os.Getenv)
	if err != nil {
		// A hook runner writes the payload whether or not there is a session
		// to report to: read it, so the runner never writes into a closed
		// pipe. (--codex has its payload in an argument, and its stdin is
		// Codex's own.)
		if len(payload) == 1 && payload[0] != "--codex" {
			_, _ = readPayload(stdin)
		}
		if *quiet {
			return 0, nil
		}
		return 1, err
	}
	req := notify.Request{State: *state, Message: *message}
	if *event != "" {
		req = notify.Request{Event: *event, Message: *message, URL: *link, To: *to, Tool: *tool}
	}
	for _, h := range hooks {
		if !*h.set {
			continue
		}
		raw, err := readPayload(stdin)
		if err != nil {
			return 1, err
		}
		mapped, ok := h.mapHook(raw)
		if !ok {
			return 0, nil
		}
		req = mapped
	}
	if *codex {
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

// payloadFlags are the flags that make the command map an agent's payload.
var payloadFlags = []string{"claude-hook", "codex-hook", "copilot-hook", "cursor-hook", "agy-hook", "goose-hook", "codex"}

// hookMode reports whether args ask for an agent's payload to be mapped, which
// is how agents' hooks run the command, whether or not the rest of args parses.
func hookMode(args []string) bool {
	for _, a := range args {
		if a == "--" {
			break
		}
		name, value, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !strings.HasPrefix(a, "-") || !slices.Contains(payloadFlags, name) {
			continue
		}
		if on, err := strconv.ParseBool(value); !hasValue || err != nil || on {
			return true
		}
	}
	return false
}

// readPayload reads a hook's payload from stdin, at most 1 MiB. A terminal is
// not read: nothing writes a payload to one, and what is typed there is the
// agent's.
func readPayload(stdin io.Reader) ([]byte, error) {
	if f, ok := stdin.(*os.File); ok {
		if fi, err := f.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
			return nil, nil
		}
	}
	return notify.ReadAllBounded(stdin)
}
