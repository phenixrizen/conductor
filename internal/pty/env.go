package pty

import (
	"os"
	"sort"
	"strings"
)

// baseAllow lists environment variables always copied from the parent.
var baseAllow = []string{"PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "TZ", "TMPDIR", "XDG_RUNTIME_DIR"}

// blocked lists variables that are never forwarded even when allowlisted.
var blocked = []string{"LD_PRELOAD", "LD_LIBRARY_PATH", "DYLD_INSERT_LIBRARIES"}

// BuildEnv returns the environment for a child process. It copies a fixed
// allowlist plus LC_* from the parent, adds `extraAllow` names, applies the
// catalog `set` values, and always sets terminal identification variables.
// CONDUCTOR_* variables from the parent or catalog never reach the child so
// server tokens cannot leak; `inject` is applied last and unfiltered for the
// per-session values Conductor itself provides (see Inject).
func BuildEnv(parent []string, extraAllow []string, set map[string]string, inject map[string]string) []string {
	allow := map[string]bool{}
	for _, k := range baseAllow {
		allow[k] = true
	}
	for _, k := range extraAllow {
		if k != "" {
			allow[k] = true
		}
	}
	block := map[string]bool{}
	for _, k := range blocked {
		block[k] = true
	}
	out := map[string]string{}
	for _, kv := range parent {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || block[k] || strings.HasPrefix(k, "CONDUCTOR_") {
			continue
		}
		if allow[k] || strings.HasPrefix(k, "LC_") {
			out[k] = v
		}
	}
	for k, v := range set {
		if k == "" || block[k] || strings.HasPrefix(k, "CONDUCTOR_") {
			continue
		}
		out[k] = v
	}
	for k, v := range inject {
		if k != "" && !strings.ContainsAny(k, "=\x00") {
			out[k] = v
		}
	}
	out["TERM"] = "xterm-256color"
	out["COLORTERM"] = "truecolor"
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, k := range keys {
		env = append(env, k+"="+out[k])
	}
	return env
}

// Inject returns the per-session variables agents use to talk back to
// Conductor: the session ID, the attention endpoint and its token.
func Inject(sessionID, notifyURL, token string) map[string]string {
	return map[string]string{
		"CONDUCTOR_SESSION_ID":   sessionID,
		"CONDUCTOR_NOTIFY_URL":   notifyURL,
		"CONDUCTOR_NOTIFY_TOKEN": token,
	}
}

// ParentEnv returns the current process environment.
func ParentEnv() []string { return os.Environ() }
