package cli

import (
	"bytes"
	"cmp"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/phenixrizen/conductor/internal/agents"
	"github.com/phenixrizen/conductor/internal/version"
)

const completionUsage = `Usage:
  conductor completion zsh|bash
      print a completion script for the shell: subcommands, flags and their
      values, and for conductor up the crew ids, from conductor crews --ids
      as you type (CONDUCTOR_SERVER and CONDUCTOR_ADMIN_TOKEN from the
      environment; nothing without them or without the server)
  conductor completion install [--shell zsh|bash] [--rc FILE]
      add one marked line that loads the script to ~/.zshrc or ~/.bashrc,
      the shell from $SHELL unless --shell says; run again, it changes
      nothing; a file that is not yours is refused
`

// completionMark marks the line completion install writes, so that a second
// install finds it and writes nothing.
const completionMark = "# conductor completion"

// How a flag's value is completed.
type flagKind int

const (
	flagBool  flagKind = iota // takes no value
	flagValue                 // takes a value nothing can guess: completes nothing
	flagFile                  // takes a path
	flagEnum                  // takes one of values
)

type flagSpec struct {
	name   string
	kind   flagKind
	values []string
}

// commandSpec is a subcommand as the scripts know it: its flags (those of
// the FlagSet its run function makes; TestCompletionSpecMatchesEveryFlag
// holds the two together), the words its first argument may be, the words its
// second may be after each first, and whether its argument is a crew id.
type commandSpec struct {
	name   string
	flags  []flagSpec
	words  []string
	after  map[string][]string
	crewID bool
}

// completionSpec lists the subcommands of root.go with the flags their run
// functions define. The notify payload flags come from hookPayloads and the
// adapters from agents.All, so those follow on their own.
func completionSpec() []commandSpec {
	levels := []string{"debug", "info", "warn", "error"}
	api := func(more ...flagSpec) []flagSpec {
		return append([]flagSpec{{name: "--server", kind: flagValue}, {name: "--token", kind: flagValue}}, more...)
	}
	notifyFlags := []flagSpec{
		{name: "--state", kind: flagEnum, values: []string{"needs_input", "working", "done", "clear"}},
		{name: "--message", kind: flagValue},
		{name: "--event", kind: flagEnum, values: []string{"progress", "artifact", "handoff", "tool_use", "tool_denied", "error"}},
		{name: "--url", kind: flagValue},
		{name: "--to", kind: flagValue},
		{name: "--tool", kind: flagValue},
		{name: "--codex"},
	}
	for _, h := range hookPayloads {
		notifyFlags = append(notifyFlags, flagSpec{name: "--" + h.name})
	}
	notifyFlags = append(notifyFlags, flagSpec{name: "--quiet"})
	adapters := []string{"all"}
	for _, a := range agents.All() {
		adapters = append(adapters, a.ID)
	}
	return []commandSpec{
		// Every flag serve takes, and no other: TestCompletionSpecMatchesEveryFlag checks it.
		{name: "serve", flags: []flagSpec{{name: "--config", kind: flagFile}, {name: "--listen", kind: flagValue}, {name: "--dev"}, {name: "--log-level", kind: flagEnum, values: levels}}},
		{name: "host", flags: []flagSpec{{name: "--server", kind: flagValue}, {name: "--token", kind: flagValue}, {name: "--name", kind: flagValue}, {name: "--host-name", kind: flagValue}, {name: "--agent", kind: flagValue}, {name: "--cwd", kind: flagFile}, {name: "--relay-only"}, {name: "--no-local"}, {name: "--stun", kind: flagValue}, {name: "--scrollback", kind: flagValue}, {name: "--file-view", kind: flagEnum, values: []string{"view", "control", "off"}}, {name: "--signal-pattern", kind: flagValue}, {name: "--log-level", kind: flagEnum, values: levels}}},
		{name: "notify", flags: notifyFlags},
		{name: "up", flags: api(flagSpec{name: "--open"}), crewID: true},
		{name: "crews", flags: api(flagSpec{name: "--ids"})},
		{name: "hooks", flags: []flagSpec{{name: "--home", kind: flagFile}, {name: "--data-dir", kind: flagFile}}, words: []string{"install", "status"}, after: map[string][]string{"install": adapters}},
		{name: "skill"},
		{name: "completion", flags: []flagSpec{{name: "--shell", kind: flagEnum, values: []string{"zsh", "bash"}}, {name: "--rc", kind: flagFile}}, words: []string{"zsh", "bash", "install"}},
		{name: "version"},
	}
}

func commandNames(spec []commandSpec) string {
	out := make([]string, len(spec))
	for i, c := range spec {
		out[i] = c.name
	}
	return strings.Join(out, " ")
}

func flagNames(flags []flagSpec) string {
	out := make([]string, len(flags))
	for i, f := range flags {
		out[i] = f.name
	}
	return strings.Join(out, " ")
}

// zshScript is the zsh completion script for spec: a function on words and
// CURRENT that hands compadd what fits, bound with compdef once compinit has
// run. The crew ids come from `crews --ids` run as the command typed
// (words[1]), so a conductor not on PATH still completes; they reach compadd
// as array elements, never as shell text.
func zshScript(spec []commandSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "#compdef conductor\n# conductor completion for zsh, generated by conductor %s. Load it after compinit with: source <(conductor completion zsh)\n", version.String())
	b.WriteString("_conductor() {\n  local cur=${words[CURRENT]} cmd=${words[2]} sub=${words[3]} prev=${words[CURRENT-1]}\n")
	fmt.Fprintf(&b, "  if (( CURRENT == 2 )); then compadd -- %s; return; fi\n", commandNames(spec))
	b.WriteString("  if [[ $cur == -* ]]; then\n    case $cmd in\n")
	for _, c := range spec {
		if len(c.flags) > 0 {
			fmt.Fprintf(&b, "      %s) compadd -- %s ;;\n", c.name, flagNames(c.flags))
		}
	}
	b.WriteString("    esac\n    return\n  fi\n  case \"$cmd $prev\" in\n")
	for _, c := range spec {
		for _, f := range c.flags {
			switch f.kind {
			case flagEnum:
				fmt.Fprintf(&b, "    %q) compadd -- %s; return ;;\n", c.name+" "+f.name, strings.Join(f.values, " "))
			case flagFile:
				fmt.Fprintf(&b, "    %q) _files; return ;;\n", c.name+" "+f.name)
			case flagValue:
				fmt.Fprintf(&b, "    %q) return ;;\n", c.name+" "+f.name)
			}
		}
	}
	b.WriteString("  esac\n  case $cmd in\n")
	for _, c := range spec {
		switch {
		case c.crewID:
			fmt.Fprintf(&b, "    %s) local -a ids; ids=(${(f)\"$(\"${words[1]}\" crews --ids 2>/dev/null)\"}); (( ${#ids} )) && compadd -- \"${ids[@]}\" ;;\n", c.name)
		case len(c.words) > 0:
			fmt.Fprintf(&b, "    %s)\n      if (( CURRENT == 3 )); then compadd -- %s\n", c.name, strings.Join(c.words, " "))
			for _, w := range c.words {
				if more := c.after[w]; len(more) > 0 {
					fmt.Fprintf(&b, "      elif [[ $sub == %s ]] && (( CURRENT == 4 )); then compadd -- %s\n", w, strings.Join(more, " "))
				}
			}
			b.WriteString("      fi ;;\n")
		}
	}
	b.WriteString("  esac\n}\nif (( $+functions[compdef] )); then compdef _conductor conductor; fi\n")
	return b.String()
}

// bashScript is the bash completion script for spec, the same shape on
// COMP_WORDS, COMP_CWORD and COMPREPLY; compgen -W filters the script's own
// words by the typed prefix. The crew ids come from `crews --ids` run as
// COMP_WORDS[0] and are never expanded: a read loop compares each with the
// typed prefix as a string and adds it to COMPREPLY as it is.
func bashScript(spec []commandSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# conductor completion for bash, generated by conductor %s. Load it with: source <(conductor completion bash)\n", version.String())
	b.WriteString("_conductor() {\n  local cur=${COMP_WORDS[COMP_CWORD]} cmd=${COMP_WORDS[1]} sub=${COMP_WORDS[2]} prev=${COMP_WORDS[COMP_CWORD-1]}\n  COMPREPLY=()\n")
	fmt.Fprintf(&b, "  if (( COMP_CWORD == 1 )); then COMPREPLY=($(compgen -W %q -- \"$cur\")); return; fi\n", commandNames(spec))
	b.WriteString("  if [[ $cur == -* ]]; then\n    case $cmd in\n")
	for _, c := range spec {
		if len(c.flags) > 0 {
			fmt.Fprintf(&b, "      %s) COMPREPLY=($(compgen -W %q -- \"$cur\")) ;;\n", c.name, flagNames(c.flags))
		}
	}
	b.WriteString("    esac\n    return\n  fi\n  case \"$cmd $prev\" in\n")
	for _, c := range spec {
		for _, f := range c.flags {
			switch f.kind {
			case flagEnum:
				fmt.Fprintf(&b, "    %q) COMPREPLY=($(compgen -W %q -- \"$cur\")); return ;;\n", c.name+" "+f.name, strings.Join(f.values, " "))
			case flagFile:
				fmt.Fprintf(&b, "    %q) COMPREPLY=($(compgen -f -- \"$cur\")); return ;;\n", c.name+" "+f.name)
			case flagValue:
				fmt.Fprintf(&b, "    %q) return ;;\n", c.name+" "+f.name)
			}
		}
	}
	b.WriteString("  esac\n  case $cmd in\n")
	for _, c := range spec {
		switch {
		case c.crewID:
			fmt.Fprintf(&b, "    %s)\n      local id\n      while IFS= read -r id; do [[ $id == \"$cur\"* ]] && COMPREPLY+=(\"$id\"); done < <(\"${COMP_WORDS[0]}\" crews --ids 2>/dev/null) ;;\n", c.name)
		case len(c.words) > 0:
			fmt.Fprintf(&b, "    %s)\n      if (( COMP_CWORD == 2 )); then COMPREPLY=($(compgen -W %q -- \"$cur\"))\n", c.name, strings.Join(c.words, " "))
			for _, w := range c.words {
				if more := c.after[w]; len(more) > 0 {
					fmt.Fprintf(&b, "      elif [[ $sub == %s ]] && (( COMP_CWORD == 3 )); then COMPREPLY=($(compgen -W %q -- \"$cur\"))\n", w, strings.Join(more, " "))
				}
			}
			b.WriteString("      fi ;;\n")
		}
	}
	b.WriteString("  esac\n}\ncomplete -F _conductor conductor\n")
	return b.String()
}

// runCompletion prints a script or installs the line that loads it.
func runCompletion(args []string, stdout, stderr io.Writer) (int, error) {
	if len(args) == 0 {
		fmt.Fprint(stderr, completionUsage)
		return 2, errors.New("completion: zsh, bash or install is required")
	}
	switch args[0] {
	case "zsh", "bash":
		if len(args) != 1 {
			fmt.Fprint(stderr, completionUsage)
			return 2, fmt.Errorf("completion %s takes no arguments", args[0])
		}
		script := zshScript(completionSpec())
		if args[0] == "bash" {
			script = bashScript(completionSpec())
		}
		_, err := io.WriteString(stdout, script)
		return 0, err
	case "install":
		return runCompletionInstall(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		fmt.Fprint(stderr, completionUsage)
		return 0, nil
	}
	fmt.Fprint(stderr, completionUsage)
	return 2, fmt.Errorf("completion: unknown shell %q (zsh or bash)", args[0])
}

// runCompletionInstall appends the line to the rc file of the shell.
func runCompletionInstall(args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("completion install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	shell := fs.String("shell", "", "zsh or bash (default: the name of $SHELL)")
	rc := fs.String("rc", "", "the file to add the line to (default: ~/.zshrc or ~/.bashrc)")
	fs.Usage = func() {
		fmt.Fprint(stderr, completionUsage)
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
		return 2, errors.New("completion install takes no arguments")
	}
	sh := cmp.Or(*shell, filepath.Base(os.Getenv("SHELL")))
	if sh != "zsh" && sh != "bash" {
		return 2, fmt.Errorf("completion install: say which shell with --shell zsh or --shell bash (SHELL is %q)", os.Getenv("SHELL"))
	}
	path := *rc
	if path == "" {
		home, err := homeOf("")
		if err != nil {
			return 1, err
		}
		path = filepath.Join(home, "."+sh+"rc")
	}
	changed, err := installCompletionLine(path, sh)
	if err != nil {
		return 1, fmt.Errorf("completion install: %w", err)
	}
	if !changed {
		fmt.Fprintf(stdout, "%s has the line already; nothing to change\n", path)
		return 0, nil
	}
	fmt.Fprintf(stdout, "wrote %s:\n  %s\nopen a new shell, or run that line, to complete conductor\n", path, completionLine(sh))
	return 0, nil
}

// completionLine is the line installed: it loads the script this conductor
// prints when conductor is on PATH, and is marked so install finds it again.
func completionLine(shell string) string {
	return "command -v conductor >/dev/null 2>&1 && source <(conductor completion " + shell + ") " + completionMark
}

// installCompletionLine appends completionLine(shell) to path unless a line
// of it carries completionMark already, and reports whether it wrote. The
// file must be the caller's own (agents.CheckOwner), and a missing file is
// made, 0600, only in a directory of the caller's own: under sudo, another
// user's rc file or home is refused.
func installCompletionLine(path, shell string) (bool, error) {
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := agents.CheckOwner(filepath.Dir(path)); err != nil {
			return false, err
		}
	case err != nil:
		return false, err
	default:
		if err := agents.CheckOwner(path); err != nil {
			return false, err
		}
		for line := range strings.SplitSeq(string(b), "\n") {
			if strings.Contains(line, completionMark) {
				return false, nil
			}
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return false, err
	}
	add := completionLine(shell) + "\n"
	if len(b) > 0 && !bytes.HasSuffix(b, []byte("\n")) {
		add = "\n" + add
	}
	if _, err := f.WriteString(add); err != nil {
		f.Close()
		return false, err
	}
	return true, f.Close()
}
