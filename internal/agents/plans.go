package agents

import (
	"os"
	"os/user"
	"path/filepath"
)

// PlansDir is where the agent of adapter agentID keeps the plans it writes
// outside a project (Adapter.PlansDir, #289), given the environment its
// process runs with (getenv) and the session's working directory (a
// relative setting is taken from it): absolute and cleaned, or "" for an
// adapter without one, an unknown adapter, or when no home is known. The
// session opens it as a second folder beside its working directory, for
// the plans the agent reports (session.Options.Plans).
func PlansDir(agentID string, getenv func(string) string, cwd string) string {
	a, ok := Get(agentID)
	if !ok || a.PlansDir == nil {
		return ""
	}
	dir := a.PlansDir(getenv)
	if dir == "" {
		return ""
	}
	if !filepath.IsAbs(dir) {
		if cwd == "" {
			return ""
		}
		dir = filepath.Join(cwd, dir)
	}
	return filepath.Clean(dir)
}

// homeOf is the home directory the environment names (HOME, else Windows's
// USERPROFILE), else the one the system knows for this user: the process's
// own environment, then the account database (os/user), which a service
// started with no HOME still has, as Node's os.homedir() does for Claude
// Code; "" for none.
func homeOf(getenv func(string) string) string {
	if h := getenv("HOME"); h != "" {
		return h
	}
	if h := getenv("USERPROFILE"); h != "" {
		return h
	}
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	if u, err := user.Current(); err == nil {
		return u.HomeDir
	}
	return ""
}

// claudePlansDir is Claude Code's plans directory: `plans` under
// CLAUDE_CONFIG_DIR when the environment sets it, else under ~/.claude.
// Verified against 2.1.306: a plan is written to <config dir>/plans/<slug>.md
// (its plansDirectory setting puts plans inside the project instead, where
// the session's folder reaches them already).
func claudePlansDir(getenv func(string) string) string {
	if dir := getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "plans")
	}
	home := homeOf(getenv)
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".claude", "plans")
}
