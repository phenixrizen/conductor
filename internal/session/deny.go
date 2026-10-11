package session

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// DenyList builds a deny list (Options.FileDeny, ResolvePath) for
// Conductor's own files: each of dirs, a directory denied with everything in
// it, and each of files, denied on its own and with its name entry
// (NameEntry): beside it, and beside its target when the path is a symbolic
// link (Leads: one to nothing yet too), every name that contains its name,
// ignoring case, such as the
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
		// Beside where it leads, a link to nothing yet included: its copies
		// are made beside its target.
		if real := Leads(file); real != "" {
			add(NameEntry(real))
		}
	}
	return deny
}

// Leads returns the path p leads to, walked as the system walks it: each
// element from the directory reached so far, a link followed to where it
// leads before a ".." after it is taken, a link to nothing yet followed to
// the path its target would be, and the rest of p, below the first element
// that is not there, taken lexically. For a p that is there it is where
// filepath.EvalSymlinks takes it. A relative p is taken from the working
// directory. It is "" when that cannot be told: more than maxMissingLinks
// links, or no working directory for a relative p.
func Leads(p string) string {
	if !filepath.IsAbs(p) {
		wd, err := os.Getwd()
		if err != nil {
			return ""
		}
		p = wd + string(filepath.Separator) + p
	}
	links := 0
	return leads(p, &links)
}

func leads(p string, links *int) string {
	sep := string(filepath.Separator)
	parts := strings.Split(p, sep)
	cur := sep
	for i, el := range parts {
		switch el {
		case "", ".":
			continue
		case "..":
			cur = filepath.Dir(cur)
			continue
		}
		next := filepath.Join(cur, el)
		fi, err := os.Lstat(next)
		if err != nil {
			return filepath.Join(append([]string{cur}, parts[i:]...)...)
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			cur = next
			continue
		}
		if real, err := filepath.EvalSymlinks(next); err == nil {
			cur = real
			continue
		}
		if *links >= maxMissingLinks {
			return ""
		}
		*links++
		target, err := os.Readlink(next)
		if err != nil {
			return ""
		}
		if !filepath.IsAbs(target) {
			target = cur + sep + target
		}
		return leads(target+sep+strings.Join(parts[i+1:], sep), links)
	}
	return cur
}

// NameEntry is the deny entry for the copies beside file: "*" and "*" around
// its name, in its directory (see insideAny).
func NameEntry(file string) string {
	return filepath.Join(filepath.Dir(file), "*"+filepath.Base(file)+"*")
}
