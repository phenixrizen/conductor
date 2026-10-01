package hostagent

import (
	"errors"
	"slices"

	"github.com/phenixrizen/conductor/internal/agents"
	"github.com/phenixrizen/conductor/internal/catalog"
)

// writeAssets is agents.WriteAssets: a test replaces it to make a mode fail.
var writeAssets = agents.WriteAssets

// injectHooks returns the command to start and the environment to add for the
// adapter opts.Adapter names, after writing the hook assets its flags point
// at. The host has no catalog, so the adapter is taken to report through
// hooks, without tool events. A name that is no adapter's leaves the command
// as it is and writes nothing, and so does an adapter without a launch route,
// whose agent reads Conductor's hooks only from its own config: nothing the
// host starts would read the assets, so the host says so at warn level, so the
// line shows with the local terminal attached (`conductor host` logs at warn
// then), and names the command that puts the hooks where the agent reads them.
// Hooks are a convenience: when they cannot be written, the host says so and
// runs the command as it is.
func injectHooks(opts Options) ([]string, map[string]string) {
	a, ok := agents.Get(opts.Adapter)
	if !ok {
		return opts.Argv, nil
	}
	if a.Inject == nil {
		opts.Log.Warn("hosting without Conductor's hooks at launch: this agent reads them only from its own config", "adapter", a.ID, "install", "conductor hooks install "+a.ID)
		return opts.Argv, nil
	}
	dir := opts.HooksDir
	bin, err := agents.BinaryPath()
	if err == nil && dir == "" {
		var legacy bool
		if dir, legacy, err = agents.HostHooksDir(); err == nil && legacy {
			opts.Log.Info("the hook assets stay in the directory an older Conductor used; the default is now ~/.conductor/hooks, where they go once this one is removed", "dir", dir)
		}
	}
	if err == nil {
		err = writeAssets(dir, bin)
		var me *agents.ModeError
		if errors.As(err, &me) {
			opts.Log.Warn("hosting with hook assets whose modes could not be set", "dir", dir, "err", err)
			err = nil
		}
	}
	if err != nil {
		opts.Log.Warn("hosting without Conductor's hooks: they could not be written", "adapter", opts.Adapter, "dir", dir, "err", err)
		return opts.Argv, nil
	}
	extra, env := agents.InjectFor(opts.Adapter, dir, catalog.Signal{Kind: catalog.SignalHook})
	return append(slices.Clone(opts.Argv), extra...), env
}
