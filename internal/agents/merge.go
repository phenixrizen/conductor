package agents

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// homeDir is a user's home directory as Install writes to it: a file goes only
// where its path really lies inside home, and never through a link.
type homeDir struct {
	dir  string // as given: the paths Install reports are under it
	real string // with its links resolved, for the checks
}

func openHome(dir string) (*homeDir, error) {
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("home %q is not an absolute path", dir)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	return &homeDir{dir: dir, real: real}, nil
}

// path is the file rel under home, as Install reports it.
func (h *homeDir) path(rel string) string {
	return filepath.Join(h.dir, filepath.FromSlash(rel))
}

// resolve returns where the file rel really lies. The directories on the way
// may be links (dotfiles kept elsewhere in home), as long as they lead to a
// place inside home; the ones missing will be created there. The file itself
// is not resolved: read and write refuse it when it is a link.
func (h *homeDir) resolve(rel string) (string, error) {
	rel = filepath.FromSlash(rel)
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("%s is not a path inside home", rel)
	}
	existing, missing := filepath.Dir(filepath.Join(h.real, rel)), ""
	for {
		_, err := os.Lstat(existing)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		missing = filepath.Join(filepath.Base(existing), missing)
		existing = filepath.Dir(existing)
	}
	dir, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	if r, err := filepath.Rel(h.real, dir); err != nil || !filepath.IsLocal(r) {
		return "", byHand("%s: a link on the way leads out of %s, and Conductor writes only inside it", h.path(rel), h.dir)
	}
	return filepath.Join(dir, missing, filepath.Base(rel)), nil
}

// read returns the content of the file rel, and false when there is none. A
// link is left to the user (ErrByHand).
func (h *homeDir) read(rel string) ([]byte, bool, error) {
	p, err := h.resolve(rel)
	if err != nil {
		return nil, false, err
	}
	fi, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := regularFile(h.path(rel), fi); err != nil {
		return nil, false, linkByHand(err)
	}
	b, err := os.ReadFile(p)
	return b, err == nil, err
}

// write makes the file rel hold data, and reports whether it had to change it.
// A link is left to the user (ErrByHand).
func (h *homeDir) write(rel string, data []byte) (bool, error) {
	p, err := h.resolve(rel)
	if err != nil {
		return false, err
	}
	changed, err := replaceFile(p, h.path(rel), data)
	return changed, linkByHand(err)
}

// errLink is the refusal to write through a symbolic link.
var errLink = errors.New("is a symbolic link, and Conductor does not write through links")

// linkByHand marks a refused link as a step for the user.
func linkByHand(err error) error {
	if errors.Is(err, errLink) {
		return fmt.Errorf("%w: %w", ErrByHand, err)
	}
	return err
}

// regularFile refuses a link, and anything else that is not a regular file.
func regularFile(name string, fi fs.FileInfo) error {
	if fi.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%s %w", name, errLink)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", name)
	}
	return nil
}

// replaceFile makes the file at p hold data; name is how errors call it. When
// the content differs it writes a temporary file next to it and renames that
// over p, so a reader sees the old content or the new, never half of it. A new
// file is 0600, in directories made 0700; a file that was there keeps its
// mode. A link or anything else that is not a regular file is refused.
func replaceFile(p, name string, data []byte) (bool, error) {
	fi, err := os.Lstat(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		fi = nil
	case err != nil:
		return false, err
	default:
		if err := regularFile(name, fi); err != nil {
			return false, err
		}
		if cur, err := os.ReadFile(p); err == nil && bytes.Equal(cur, data) {
			return false, nil
		}
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(p)+".conductor-*")
	if err != nil {
		return false, err
	}
	renamed := false
	defer func() {
		if !renamed {
			f.Close()
			os.Remove(f.Name())
		}
	}()
	if fi != nil {
		if err := f.Chmod(fi.Mode().Perm()); err != nil {
			return false, err
		}
	}
	if _, err := f.Write(data); err != nil {
		return false, err
	}
	if err := f.Sync(); err != nil {
		return false, err
	}
	if err := f.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(f.Name(), p); err != nil {
		return false, err
	}
	renamed = true
	return true, nil
}

// step changes one file under home and reports whether it did.
type step struct {
	rel string
	do  func(h *homeDir) (bool, error)
}

// install runs steps in order on home and returns the files they changed. A
// step that leaves its file to the user (ErrByHand) does not stop the steps
// after it, and what each such step says comes back joined; any other error
// stops there.
func install(home string, steps ...step) ([]string, error) {
	h, err := openHome(home)
	if err != nil {
		return nil, err
	}
	var touched []string
	var manual []error
	for _, s := range steps {
		changed, err := s.do(h)
		if changed {
			touched = append(touched, h.path(s.rel))
		}
		if errors.Is(err, ErrByHand) {
			manual = append(manual, err)
			continue
		}
		if err != nil {
			return touched, err
		}
	}
	return touched, errors.Join(manual...)
}

// copyAsset is the step that makes the file rel under home a copy of the
// asset, a file Conductor owns.
func copyAsset(assets map[string]string, hooksDir, asset, rel string) step {
	return step{rel, func(h *homeDir) (bool, error) {
		b, err := assetFor(assets, hooksDir, asset)
		if err != nil {
			return false, err
		}
		return h.write(rel, b)
	}}
}

// copyAssetDir is the steps that copy every asset under the directory prefix
// of the assets into the directory dir under home, a directory Conductor owns.
func copyAssetDir(assets map[string]string, hooksDir, prefix, dir string) []step {
	var steps []step
	for _, rel := range slices.Sorted(maps.Keys(assets)) {
		if name, ok := strings.CutPrefix(rel, prefix); ok {
			steps = append(steps, copyAsset(assets, hooksDir, rel, dir+name))
		}
	}
	return steps
}

// fileExists reports whether the regular file rel exists under home, and
// where it is.
func fileExists(home, rel string) (bool, string) {
	p := filepath.Join(home, filepath.FromSlash(rel))
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular(), p
}

// fileHasLine reports whether the file rel under home has a line that is line
// once trimmed, and where the file is.
func fileHasLine(home, rel, line string) (bool, string) {
	p := filepath.Join(home, filepath.FromSlash(rel))
	b, err := os.ReadFile(p)
	if err != nil {
		return false, p
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) == line {
			return true, p
		}
	}
	return false, p
}

// fileMentions reports whether the file rel under home contains s, and where
// the file is.
func fileMentions(home, rel, s string) (bool, string) {
	p := filepath.Join(home, filepath.FromSlash(rel))
	b, err := os.ReadFile(p)
	return err == nil && strings.Contains(string(b), s), p
}

// hasJSONKey reports whether the JSON object in the file rel under home has
// the member key, and where the file is.
func hasJSONKey(home, rel, key string) (bool, string) {
	p := filepath.Join(home, filepath.FromSlash(rel))
	b, err := os.ReadFile(p)
	if err != nil {
		return false, p
	}
	doc, err := parseObject(b)
	if err != nil {
		return false, p
	}
	_, ok := doc.get(key)
	return ok, p
}

// hooksMention reports whether the JSON file rel under home has an entry that
// mentions marker under "hooks", and where the file is.
func hooksMention(home, rel, marker string) (bool, string) {
	p := filepath.Join(home, filepath.FromSlash(rel))
	b, err := os.ReadFile(p)
	if err != nil {
		return false, p
	}
	doc, err := parseObject(b)
	if err != nil {
		return false, p
	}
	raw, ok := doc.get("hooks")
	return ok && mentions(raw, marker), p
}

// object is a JSON object read for editing: its members in their order, each
// value kept as the text it was, so that what Conductor does not own is
// written back as it was found.
type object struct {
	keys []string
	vals map[string]json.RawMessage
}

// parseObject reads a JSON object. A key that appears twice keeps its first
// place and its last value, the value JSON readers take.
func parseObject(data []byte) (*object, error) {
	if !json.Valid(data) {
		return nil, errors.New("not valid JSON")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	o := &object{vals: map[string]json.RawMessage{}}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		o.set(key, v)
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("more than one JSON value")
	}
	return o, nil
}

func (o *object) get(key string) (json.RawMessage, bool) {
	v, ok := o.vals[key]
	return v, ok
}

// set replaces the value of key where it is, or adds key at the end.
func (o *object) set(key string, v json.RawMessage) {
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = v
}

func (o *object) encode() []byte {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`"` + jsonEscape(k) + `":`)
		b.Write(o.vals[k])
	}
	b.WriteByte('}')
	return b.Bytes()
}

func encodeList(items []json.RawMessage) json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(item)
	}
	b.WriteByte(']')
	return b.Bytes()
}

// indentJSON indents a JSON document with two spaces, the way Conductor
// writes every JSON file, ending in a newline. Only whitespace changes.
func indentJSON(data []byte) ([]byte, error) {
	var b bytes.Buffer
	if err := json.Indent(&b, bytes.TrimSpace(data), "", "  "); err != nil {
		return nil, err
	}
	b.WriteByte('\n')
	return b.Bytes(), nil
}

func sameJSON(a, b []byte) bool {
	var ca, cb bytes.Buffer
	return json.Compact(&ca, a) == nil && json.Compact(&cb, b) == nil && bytes.Equal(ca.Bytes(), cb.Bytes())
}

func isNull(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}

// mentions reports whether a string anywhere in the JSON value contains
// marker.
func mentions(raw json.RawMessage, marker string) bool {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	var walk func(v any) bool
	walk = func(v any) bool {
		switch v := v.(type) {
		case string:
			return strings.Contains(v, marker)
		case []any:
			return slices.ContainsFunc(v, walk)
		case map[string]any:
			for _, e := range v {
				if walk(e) {
					return true
				}
			}
		}
		return false
	}
	return walk(v)
}

func writeJSON(h *homeDir, rel string, data []byte) (bool, error) {
	out, err := indentJSON(data)
	if err != nil {
		return false, err
	}
	return h.write(rel, out)
}

// mergeJSONHooks adds the hook entries of asset to the JSON file rel: every
// list under the asset's "hooks" is added to the file's list of the same name,
// unless an entry there already mentions marker. A file that is missing or
// empty becomes the asset. Everything else in the file is kept as it was, in
// its order, and the file is written with two-space indentation. It reports
// whether the file changed. A file that is not a JSON object with an object of
// lists under "hooks" is left alone: ErrByHand.
func mergeJSONHooks(h *homeDir, rel string, asset []byte, marker string) (bool, error) {
	cur, ok, err := h.read(rel)
	if err != nil {
		return false, err
	}
	if !ok || len(bytes.TrimSpace(cur)) == 0 {
		return writeJSON(h, rel, asset)
	}
	doc, err := parseObject(cur)
	if err != nil {
		return false, byHand("%s: %v, so Conductor cannot merge its hooks into it; add them from the snippet", h.path(rel), err)
	}
	ours, err := parseObject(asset)
	if err != nil {
		return false, fmt.Errorf("asset for %s: %w", rel, err)
	}
	ourHooks, _ := ours.get("hooks")
	add, err := parseObject(ourHooks)
	if err != nil {
		return false, fmt.Errorf("asset for %s: hooks: %w", rel, err)
	}
	hooks := &object{vals: map[string]json.RawMessage{}}
	if raw, ok := doc.get("hooks"); ok && !isNull(raw) {
		if hooks, err = parseObject(raw); err != nil {
			return false, byHand("in %s, \"hooks\" is not an object, so Conductor cannot merge its hooks into it; add them from the snippet", h.path(rel))
		}
	}
	changed := false
	for _, event := range add.keys {
		var list []json.RawMessage
		if raw, ok := hooks.get(event); ok && !isNull(raw) {
			if json.Unmarshal(raw, &list) != nil {
				return false, byHand("in %s, hooks.%s is not a list, so Conductor cannot merge its hooks into it; add them from the snippet", h.path(rel), event)
			}
		}
		if slices.ContainsFunc(list, func(e json.RawMessage) bool { return mentions(e, marker) }) {
			continue
		}
		var entries []json.RawMessage
		if err := json.Unmarshal(add.vals[event], &entries); err != nil {
			return false, fmt.Errorf("asset for %s: hooks.%s: %w", rel, event, err)
		}
		hooks.set(event, encodeList(append(list, entries...)))
		changed = true
	}
	if !changed {
		return false, nil
	}
	doc.set("hooks", hooks.encode())
	return writeJSON(h, rel, doc.encode())
}

// setJSONKey makes the member key of the JSON file rel what it is in asset,
// and keeps every other member as it was. A file that is missing or empty
// becomes the asset.
func setJSONKey(h *homeDir, rel string, asset []byte, key string) (bool, error) {
	cur, ok, err := h.read(rel)
	if err != nil {
		return false, err
	}
	if !ok || len(bytes.TrimSpace(cur)) == 0 {
		return writeJSON(h, rel, asset)
	}
	doc, err := parseObject(cur)
	if err != nil {
		return false, byHand("%s: %v, so Conductor cannot add its %q key; add it from the snippet", h.path(rel), err, key)
	}
	ours, err := parseObject(asset)
	if err != nil {
		return false, fmt.Errorf("asset for %s: %w", rel, err)
	}
	val, _ := ours.get(key)
	if old, ok := doc.get(key); ok && sameJSON(old, val) {
		return false, nil
	}
	doc.set(key, val)
	return writeJSON(h, rel, doc.encode())
}

// createJSON writes asset as the JSON file rel when there is none. A file
// with other content is never overwritten: ErrByHand.
func createJSON(h *homeDir, rel string, asset []byte) (bool, error) {
	cur, ok, err := h.read(rel)
	if err != nil {
		return false, err
	}
	if ok && len(bytes.TrimSpace(cur)) > 0 {
		if sameJSON(cur, asset) {
			return false, nil
		}
		return false, byHand("%s exists and Conductor does not overwrite it; add its hooks from the snippet", h.path(rel))
	}
	return writeJSON(h, rel, asset)
}

// withMarkedBlock returns content with body between the lines begin and end.
// A block that is there already is replaced where it is. A new one goes at the
// top: in TOML a key belongs to the table whose header comes before it, so a
// block appended below a [table] would not set a root key.
func withMarkedBlock(content, begin, end, body string) (string, error) {
	block := begin + "\n" + body + "\n" + end + "\n"
	lines := strings.SplitAfter(content, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) != begin {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == end {
				return strings.Join(lines[:i], "") + block + strings.Join(lines[j+1:], ""), nil
			}
		}
		return "", byHand("the line %q has no %q line after it", begin, end)
	}
	if strings.TrimSpace(content) == "" {
		return block, nil
	}
	return block + "\n" + content, nil
}

// tomlSetsRootKey reports whether content, a TOML document, sets key at its
// root anywhere but between the lines begin and end: on a line above the first
// table header. It reads lines, not TOML.
func tomlSetsRootKey(content, key, begin, end string) bool {
	inBlock := false
	for _, l := range strings.Split(content, "\n") {
		t := strings.TrimSpace(l)
		switch {
		case t == begin:
			inBlock = true
		case t == end:
			inBlock = false
		case inBlock || t == "" || strings.HasPrefix(t, "#"):
		case strings.HasPrefix(t, "["):
			return false
		default:
			k, _, ok := strings.Cut(t, "=")
			if k = strings.TrimSpace(k); ok && (k == key || k == `"`+key+`"` || k == "'"+key+"'") {
				return true
			}
		}
	}
	return false
}

// withYAMLListItem returns content, a YAML document, with item added to the
// block list under the root key key: after the list's last entry, at its
// indentation, under a marker comment. Without the key, the list is added at
// the end. Content that mentions match already is returned as it is. It reads
// lines, not YAML: a key that is quoted or whose value is written inline is
// left to the user (ErrByHand).
func withYAMLListItem(content, key, marker, item, match string) (string, error) {
	if strings.Contains(content, match) {
		return content, nil
	}
	lines := strings.Split(content, "\n")
	at := -1
	for i, l := range lines {
		l = strings.TrimRight(l, "\r")
		if strings.HasPrefix(l, `"`+key+`"`) || strings.HasPrefix(l, "'"+key+"'") {
			return "", byHand("the key %s is quoted; add %s to its list by hand", key, item)
		}
		k, rest, ok := strings.Cut(l, ":")
		if !ok || k != key {
			continue
		}
		if rest = strings.TrimSpace(rest); rest != "" && !strings.HasPrefix(rest, "#") {
			return "", byHand("%s is not a list with one entry per line; add %s to it by hand", key, item)
		}
		at = i
		break
	}
	if at < 0 {
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		return content + key + ":\n  " + marker + "\n  - " + item + "\n", nil
	}
	indent, last, seen := "  ", at, false
	for i := at + 1; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], "\r")
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if l[0] != ' ' && l[0] != '\t' && l[0] != '-' {
			break // the next root key, or a comment at the root
		}
		if !seen && strings.HasPrefix(t, "-") {
			indent, seen = l[:len(l)-len(strings.TrimLeft(l, " \t"))], true
		}
		last = i
	}
	lines = slices.Insert(lines, last+1, indent+marker, indent+"- "+item)
	return strings.Join(lines, "\n"), nil
}
