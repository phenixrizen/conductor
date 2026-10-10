package session

import (
	"path/filepath"
	"slices"
)

// DenyList builds a deny list (Options.FileDeny, ResolvePath) for
// Conductor's own files: each of dirs, a directory denied with everything in
// it, and each of files, denied on its own and with its name entry
// (NameEntry): beside it, and beside its target when the path is a symbolic
// link, every name that contains its name, ignoring case, such as the
// conductor.json.bak, conductor.json~, .conductor.json.swp or
// #conductor.json# an editor leaves. Empty paths and repeats are left out.
// The server builds its own from its configuration, conductor host one for
// the server on its machine (config.Config.FileDeny, config.LocalFileDeny).
func DenyList(dirs, files []string) []string {
	var deny []string
	add := func(p string) {
		if p != "" && !slices.Contains(deny, p) {
			deny = append(deny, p)
		}
	}
	for _, d := range dirs {
		add(d)
	}
	for _, file := range files {
		if file == "" {
			continue
		}
		add(file)
		add(NameEntry(file))
		if real, err := filepath.EvalSymlinks(file); err == nil {
			add(NameEntry(real))
		}
	}
	return deny
}

// NameEntry is the deny entry for the copies beside file: "*" and "*" around
// its name, in its directory (see insideAny).
func NameEntry(file string) string {
	return filepath.Join(filepath.Dir(file), "*"+filepath.Base(file)+"*")
}
