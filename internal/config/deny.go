package config

import "github.com/phenixrizen/conductor/internal/session"

// FileDeny returns what no file read or write of a session may reach, even
// inside its working directory (session.Options.FileDeny): the data
// directory, whose catalog.json holds the agents' env secrets, and dirs, such
// as the directory the store writes to should it ever differ; the config file
// (Path), which holds the workbench token and host tokens; and the catalog
// file (CatalogPath), which can hold env secrets. Each of the two files comes
// with its name entries (session.DenyList), so an editor's or a backup's
// copy beside it is refused too. A directory is denied with everything in
// it, a file on its own.
func (c *Config) FileDeny(dirs ...string) []string {
	return session.DenyList(append([]string{c.DataDir}, dirs...), []string{c.Path, c.CatalogPath})
}
