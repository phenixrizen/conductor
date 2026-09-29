// Package agents knows how each supported coding agent reports to Conductor:
// the hook files Conductor generates for it, the flags and environment that
// wire them in at launch, and how to install them into the agent's own
// configuration when an admin asks. The hooks run `conductor notify`, whose
// payload mappers live in internal/notify.
package agents

import (
	"errors"
	"fmt"

	"github.com/phenixrizen/conductor/internal/catalog"
)

// Adapter knows how one agent reports to Conductor.
type Adapter struct {
	ID, Name string
	// Assets written under hooksDir at startup: relative path → content. {{BIN}} is
	// replaced by the absolute conductor binary path.
	Assets map[string]string
	// Inject returns extra argv and env for a launch, given the hooks dir. Empty
	// when the agent has no launch-time route.
	Inject func(hooksDir string, sig catalog.Signal) (argv []string, env map[string]string)
	// Install merges Conductor's hooks into the agent's own config under home.
	// Idempotent. Returns the files it touched. nil when the agent has no file route.
	Install func(home, hooksDir string) ([]string, error)
	// Status reports whether the install exists under home.
	Status func(home string) (installed bool, where string)
	// Snippet is what a host user pastes when Install is not available to them.
	Snippet func(hooksDir string) string
	// Events documents what this adapter can report (for the Events page).
	Events []string // e.g. "needs_input", "done", "tool_use"
	// Experimental marks an adapter for an agent whose hook interface is still
	// changing (a developer preview); the Events page says so.
	Experimental bool
}

// ErrByHand wraps what Install leaves to the user: a file it would have to
// overwrite or cannot merge into, or an agent it does not edit at all. The
// message says what to do instead, and the files Install did change are still
// returned with it.
var ErrByHand = errors.New("install by hand")

func byHand(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrByHand, fmt.Sprintf(format, args...))
}
