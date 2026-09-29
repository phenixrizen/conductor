package agents

import (
	"github.com/phenixrizen/conductor/internal/catalog"
)

// aider runs a notifications command, through the shell, whenever it waits
// for the user, and reads the setting from the environment. That is its only
// route: there is nothing to install. Its yes/no confirmations are not
// notifications; a screen pattern on "(Y)es/(N)o" catches those.

// aiderCommand is the notifications command, a shell command line.
func aiderCommand(bin string) string {
	return shellQuote(bin) + ` notify --state needs_input --message "aider is waiting"`
}

func aiderAdapter() Adapter {
	return Adapter{
		ID:   "aider",
		Name: "aider",
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
		Events: []string{"needs_input"},
	}
}
