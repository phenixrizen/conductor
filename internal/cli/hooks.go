package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/phenixrizen/conductor/internal/agents"
	"github.com/phenixrizen/conductor/internal/config"
)

const hooksUsage = `Usage:
  conductor hooks install <adapter>|all [--home DIR] [--data-dir DIR]
      put Conductor's hooks into the agents' own config under a home
      directory, as the Events page does for the server's user
  conductor hooks status [--home DIR]
      list the adapters and whether their hooks are installed
`

// runHooks installs Conductor's hooks into agents' own configuration for the
// user who runs it, or reports whether they are installed.
func runHooks(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	if len(args) == 0 {
		fmt.Fprint(stderr, hooksUsage)
		return 2, errors.New("hooks: install or status is required")
	}
	switch args[0] {
	case "install":
		return runHooksInstall(args[1:], stdout, stderr)
	case "status":
		return runHooksStatus(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		fmt.Fprint(stderr, hooksUsage)
		return 0, nil
	}
	fmt.Fprint(stderr, hooksUsage)
	return 2, fmt.Errorf("hooks: unknown command %q", args[0])
}

// runHooksInstall runs Install for one adapter or all of them and prints the
// files it wrote. An adapter it cannot install from a file (nothing to
// install, or a step left to the user) is reported; on its own it also
// prints the snippet and exits 1, while with all it does not count as a
// failure. Any other error fails the command, after the other adapters.
func runHooksInstall(args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("hooks install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "home directory whose agent configs to install into (default: yours)")
	dataDir := fs.String("data-dir", "", "data directory of conductor serve: the hooks written to its hooks/ are the ones installed (default: CONDUCTOR_DATA_DIR, else conductor.d in the current directory; without them the hooks name this binary)")
	fs.Usage = func() {
		fmt.Fprint(stderr, hooksUsage)
		fs.PrintDefaults()
	}
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		return 2, err
	}
	if len(rest) != 1 {
		fs.Usage()
		return 2, errors.New("hooks install: name one adapter, or all")
	}
	var list []agents.Adapter
	if rest[0] == "all" {
		list = agents.All()
	} else if a, ok := agents.Get(rest[0]); ok {
		list = []agents.Adapter{a}
	} else {
		return 2, fmt.Errorf("hooks install: unknown adapter %q (one of %s, or all)", rest[0], adapterIDs())
	}
	dir, err := homeOf(*home)
	if err != nil {
		return 1, err
	}
	hooksDir := serveHooksDir(*dataDir)
	one := len(list) == 1
	left := false
	var failed []string
	for _, a := range list {
		if a.Install == nil {
			fmt.Fprintf(stdout, "%s: nothing to install: Conductor sets %s up when it launches it\n", a.ID, a.Name)
			if one {
				fmt.Fprintf(stdout, "Where Conductor does not launch it, set it up with:\n\n%s", a.Snippet(hooksDir))
				left = true
			}
			continue
		}
		touched, err := a.Install(dir, hooksDir)
		for _, p := range touched {
			fmt.Fprintf(stdout, "%s: wrote %s\n", a.ID, p)
		}
		switch {
		case errors.Is(err, agents.ErrByHand):
			fmt.Fprintf(stdout, "%s: %v\n", a.ID, err)
			if !one {
				fmt.Fprintf(stdout, "%s: conductor hooks install %s prints the snippet\n", a.ID, a.ID)
				break
			}
			fmt.Fprintf(stdout, "\n%s", a.Snippet(hooksDir))
			left = true
		case err != nil:
			fmt.Fprintf(stderr, "%s: %v\n", a.ID, err)
			failed = append(failed, a.ID)
		case len(touched) == 0:
			fmt.Fprintf(stdout, "%s: installed already, nothing to change\n", a.ID)
		}
	}
	if len(failed) > 0 {
		return 1, fmt.Errorf("hooks install: failed for %s", strings.Join(failed, ", "))
	}
	if left {
		return 1, nil
	}
	return 0, nil
}

// runHooksStatus lists every adapter: whether its hooks are installed under
// the home directory and where, and whether Conductor wires them in when it
// launches the agent. Checking writes nothing.
func runHooksStatus(args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("hooks status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "home directory whose agent configs to check (default: yours)")
	fs.Usage = func() {
		fmt.Fprint(stderr, hooksUsage)
		fs.PrintDefaults()
	}
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		return 2, err
	}
	if len(rest) != 0 {
		fs.Usage()
		return 2, fmt.Errorf("hooks status takes no arguments, got %q", rest)
	}
	dir, err := homeOf(*home)
	if err != nil {
		return 1, err
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ADAPTER\tHOOKS\tLAUNCH\tWHERE")
	for _, a := range agents.All() {
		hooks, where := "by hand", "-"
		switch {
		case a.Status != nil:
			var ok bool
			ok, where = a.Status(dir)
			hooks = "not installed"
			if ok {
				hooks = "installed"
			}
		case a.Install == nil:
			hooks = "nothing to install"
		}
		if a.Experimental {
			hooks += " (experimental)"
		}
		launch := "-"
		if a.Inject != nil {
			launch = "at launch"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", a.ID, hooks, launch, where)
	}
	return 0, tw.Flush()
}

// runSkill prints the Conductor skill, the SKILL.md that tells an agent how
// to report to Conductor, for adding to an agent's skills by hand.
func runSkill(args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("skill", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: conductor skill\n\nPrints the Conductor skill (SKILL.md), which tells an agent how to report to Conductor.")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		return 2, err
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2, errors.New("skill takes no arguments")
	}
	if _, err := io.WriteString(stdout, agents.Skill); err != nil {
		return 1, err
	}
	return 0, nil
}

// parseInterspersed parses args with fs, taking flags before and after the
// other arguments alike, and returns the other arguments. After "--" every
// argument is one of them.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		// Parse stops at the first argument that is not a flag, or after "--".
		if n := len(args) - len(rest); n > 0 && args[n-1] == "--" {
			return append(positional, rest...), nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

// homeOf is the home directory to work in: dir, made absolute, or the
// user's own.
func homeOf(dir string) (string, error) {
	if dir == "" {
		return os.UserHomeDir()
	}
	return filepath.Abs(dir)
}

// serveHooksDir is the hooks dir of the data directory dataDir or, when it is
// empty, of the one conductor serve uses without a config file:
// CONDUCTOR_DATA_DIR, else conductor.d in the current directory.
func serveHooksDir(dataDir string) string {
	cfg := config.Config{DataDir: dataDir}
	if cfg.DataDir == "" {
		cfg.DataDir = os.Getenv("CONDUCTOR_DATA_DIR")
	}
	cfg.ResolveDataDir("")
	return agents.HooksDir(cfg.DataDir)
}

// adapterIDs lists the adapters' IDs, comma separated.
func adapterIDs() string {
	var ids []string
	for _, a := range agents.All() {
		ids = append(ids, a.ID)
	}
	return strings.Join(ids, ", ")
}
