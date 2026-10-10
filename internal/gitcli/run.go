package gitcli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/phenixrizen/conductor/internal/pty"
)

// How Conductor runs git. A repository's configuration can name programs
// that git starts by itself: hooks, a file system monitor, a pager, an
// external diff, text conversion, clean, smudge and process filters, an
// editor, credential helpers, transports and the commands they run. The
// directories Conductor runs git in (a session's, a crew's) are not ones
// it vouches for, so the git it runs starts none of them:
//
//   - command-line configuration (safeConfig) sets each to nothing. git
//     reads it after the system, global and repository files and whatever
//     those include (include.path, includeIf), so it wins over all of
//     them, and git hands it on to the git processes it starts itself
//     (worktree add's checkout, a submodule's status);
//   - the environment (safeEnv) allows no transport, whatever
//     protocol.<name>.allow the repository sets: nothing fetches, and no
//     ssh command, upload-pack, receive-pack, remote helper or credential
//     helper starts;
//   - each subcommand Conductor runs gets the options (commandOptions) that
//     keep external diffs, text conversion and the submodules' own git out;
//   - the clean, smudge and process filters the configuration names are
//     turned off by name (filterArgs), git having no switch for all of them.
//
// The person's own identity and settings in the global configuration still
// apply; Conductor commits nothing and signs nothing.

// safeConfig is set with -c on every git Conductor runs.
var safeConfig = []string{
	// No file system monitor, neither a hook program nor git's daemon.
	// Empty is off in every git; "false" is understood from 2.36 only.
	"core.fsmonitor=",
	// No hook: no post-checkout or reference-transaction when worktree add
	// checks out and makes the branch, no post-index-change, no other.
	"core.hooksPath=" + os.DevNull,
	// With --no-pager: no pager, whatever core.pager or pager.<command> say.
	"core.pager=cat",
	// With --no-ext-diff on the commands that diff.
	"diff.external=",
	// git never waits on an editor: one asked for fails.
	"core.editor=false",
	"sequence.editor=false",
	// No askpass program and no credential helper (empty resets the list).
	"core.askPass=",
	"credential.helper=",
	// No transport by configuration either (safeEnv's GIT_ALLOW_PROTOCOL
	// overrides the per-protocol settings).
	"protocol.allow=never",
	// No signature check, so no gpg.program.
	"log.showSignature=false",
	// No automatic maintenance or garbage collection.
	"gc.auto=0",
	"maintenance.auto=false",
}

// safeEnv is set in git's environment, which is otherwise the allowlisted
// one every child of Conductor gets (pty.BuildEnv): no GIT_* variable of
// the server's comes through.
var safeEnv = map[string]string{
	"LC_ALL": "C",
	// An empty list: no protocol is allowed, overriding any
	// protocol.allow and protocol.<name>.allow.
	"GIT_ALLOW_PROTOCOL": "",
	// A partial clone's missing object is an error rather than a fetch (git
	// 2.45 and later; before, the fetch stops at GIT_ALLOW_PROTOCOL).
	"GIT_NO_LAZY_FETCH":   "1",
	"GIT_TERMINAL_PROMPT": "0",
	// Reads take no lock and write no index beside the agent's own git.
	"GIT_OPTIONAL_LOCKS": "0",
}

// commandOptions are put right after the subcommand: the ones that diff
// run no external diff and no text conversion; status and diff do not run
// git in a submodule to see whether its working tree is dirty (a change
// of the submodule's commit still shows), since that git would read the
// submodule's own configuration, whose filters filterArgs does not see.
func commandOptions(sub string) []string {
	switch sub {
	case "diff":
		return []string{"--no-ext-diff", "--no-textconv", "--ignore-submodules=dirty"}
	case "show", "log":
		return []string{"--no-ext-diff", "--no-textconv"}
	case "status":
		return []string{"--ignore-submodules=dirty"}
	}
	return nil
}

// noFilters are the subcommands Conductor runs that never filter a file:
// they do not need filterArgs' look at the configuration first.
var noFilters = map[string]bool{"rev-parse": true, "config": true, "show": true, "init": true}

// maxFilters bounds how many filters filterArgs turns off; a configuration
// that names more is refused, as is a name -c cannot carry.
const maxFilters = 200

// Run runs git with args in dir (git -C dir, so it is the same wherever the
// process runs), hardened as above, in the C locale. It is the one way
// Conductor runs git. A failure carries the line that says why;
// ErrNotRepo outside a repository.
func Run(ctx context.Context, dir string, args ...string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("git: no command")
	}
	filters, err := filterArgs(ctx, dir, args)
	if err != nil {
		return "", err
	}
	argv := append(baseArgs(), filters...)
	argv = append(argv, "-C", dir, args[0])
	argv = append(argv, commandOptions(args[0])...)
	argv = append(argv, args[1:]...)
	out, stderr, err := execGit(ctx, argv)
	if err != nil {
		return "", gitError(ctx, args[0], stderr, err)
	}
	return out, nil
}

// baseArgs are the options every git Conductor runs starts with.
func baseArgs() []string {
	argv := make([]string, 0, 1+2*len(safeConfig))
	argv = append(argv, "--no-pager")
	for _, kv := range safeConfig {
		argv = append(argv, "-c", kv)
	}
	return argv
}

// execGit runs git with argv in safeEnv, no input.
func execGit(ctx context.Context, argv []string) (stdout, stderr string, err error) {
	cmd := exec.CommandContext(ctx, "git", argv...)
	cmd.Env = pty.BuildEnv(pty.ParentEnv(), nil, safeEnv, nil)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err = cmd.Run()
	return o.String(), e.String(), err
}

// gitError is the error of a git that failed: ctx's when it ended,
// ErrNotRepo, or the line of its standard error that says why.
func gitError(ctx context.Context, sub, stderr string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if msg := Message(stderr); msg != "" {
		if strings.Contains(msg, "not a git repository") {
			return ErrNotRepo
		}
		return fmt.Errorf("git %s: %s", sub, msg)
	}
	return fmt.Errorf("git %s: %w", sub, err)
}

// filterArgs are the -c options that turn off the clean, smudge and process
// filters the configuration for dir names (and that no filter is
// required), read just before the command. A command that only reads
// turns off every one, wherever it is defined. worktree add, which checks
// files out, turns off those the repository's own configuration defines
// (its local and worktree files and what they include) and keeps the ones
// configured outside it, the person's system or global configuration (Git
// LFS's, for one), so that the files come out as a checkout makes them.
func filterArgs(ctx context.Context, dir string, args []string) ([]string, error) {
	if noFilters[args[0]] {
		return nil, nil
	}
	checkout := args[0] == "worktree" && len(args) > 1 && args[1] == "add"
	names, err := filterNames(ctx, dir, checkout)
	if err != nil {
		return nil, err
	}
	if len(names) > maxFilters {
		return nil, fmt.Errorf("git: the configuration names more than %d filters", maxFilters)
	}
	out := make([]string, 0, 8*len(names))
	for _, n := range names {
		if strings.ContainsAny(n, "=\n") {
			return nil, fmt.Errorf("git: the configuration names a filter %q that cannot be turned off", n)
		}
		out = append(out,
			"-c", "filter."+n+".clean=",
			"-c", "filter."+n+".smudge=",
			"-c", "filter."+n+".process=",
			"-c", "filter."+n+".required=false")
	}
	return out, nil
}

// filterKeys matches the keys that give a filter a program.
const filterKeys = `^filter\..*\.(clean|smudge|process)$`

// filterNames lists the names of the filters the configuration for dir
// gives a program, each once: only those in the repository's own files
// when repoOnly (all of them with a git older than 2.26, which cannot say
// where a value comes from), else all of them.
func filterNames(ctx context.Context, dir string, repoOnly bool) ([]string, error) {
	var names []string
	seen := map[string]bool{}
	add := func(key string) {
		name, ok := strings.CutPrefix(key, "filter.")
		if i := strings.LastIndexByte(name, '.'); ok && i > 0 {
			if name = name[:i]; !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	if repoOnly {
		out, ok, err := configList(ctx, dir, "--show-scope")
		if err != nil {
			return nil, err
		}
		if ok {
			// Pairs of a scope and a key, each ended by a NUL.
			f := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
			for i := 0; i+1 < len(f); i += 2 {
				if f[i] == "local" || f[i] == "worktree" {
					add(f[i+1])
				}
			}
			return names, nil
		}
	}
	out, _, err := configList(ctx, dir)
	if err != nil {
		return nil, err
	}
	for _, key := range strings.Split(strings.TrimSuffix(out, "\x00"), "\x00") {
		add(key)
	}
	return names, nil
}

// configList is git config --get-regexp filterKeys for dir, with extra
// options before it: the keys each ended by a NUL, "" when there is none.
// ok is false when git does not know an option of extra.
func configList(ctx context.Context, dir string, extra ...string) (out string, ok bool, err error) {
	argv := append(baseArgs(), "-C", dir, "config")
	argv = append(argv, extra...)
	argv = append(argv, "-z", "--name-only", "--get-regexp", filterKeys)
	out, stderr, err := execGit(ctx, argv)
	var exit *exec.ExitError
	switch {
	case err == nil:
		return out, true, nil
	case ctx.Err() != nil:
		return "", false, ctx.Err()
	case errors.As(err, &exit) && exit.ExitCode() == 1 && strings.TrimSpace(stderr) == "":
		// No key matches.
		return "", true, nil
	case errors.As(err, &exit) && exit.ExitCode() == 129 && len(extra) > 0:
		// An option this git does not know.
		return "", false, nil
	}
	return "", false, gitError(ctx, "config", stderr, err)
}
