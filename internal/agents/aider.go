package agents

import (
	"github.com/phenixrizen/conductor/internal/catalog"
	"regexp"
)

// aider runs a notifications command, through the shell, whenever it waits
// for the user, and reads the setting from the environment. That is its only
// route: there is nothing to install. Its yes/no confirmations are not
// notifications; a screen pattern on "(Y)es/(N)o" catches those. It waits
// for the user each time it has answered a line, which is a turn's end:
// the command reports done (a wait reported as needs_input would stay
// until aider's next report, since keys typed in the terminal answer no
// question: docs/plans/codex-needs-you.md, the owner's decisions).

// aiderCommand is the notifications command, a shell command line.
func aiderCommand(bin string) string {
	return shellQuote(bin) + ` notify --state done --message "aider is waiting"`
}

func aiderAdapter() Adapter {
	return Adapter{
		ID:   "aider",
		Name: "aider",
		// aider prints "aider 0.86.1" (from its docs; verify).
		Probe: &Probe{Args: []string{"--version"}, Match: regexp.MustCompile(`(?m)^\s*aider\s+v?(\d+\.\d+\.\d+)`)},
		Inject: func(hooksDir string, sig catalog.Signal) ([]string, map[string]string) {
			bin, err := binPath()
			if err != nil {
				return nil, nil
			}
			return nil, map[string]string{
				"AIDER_NOTIFICATIONS":         "true",
				"AIDER_NOTIFICATIONS_COMMAND": aiderCommand(bin),
			}
		},
		Snippet: func(hooksDir string) string {
			bin, err := binPath()
			if err != nil {
				bin = "conductor"
			}
			return "export AIDER_NOTIFICATIONS=true\nexport AIDER_NOTIFICATIONS_COMMAND=" + shellQuote(aiderCommand(bin)) + "\n"
		},
		Events: []string{"done"},
	}
}
