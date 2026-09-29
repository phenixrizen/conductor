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
// where its path really lies inside home, and never through a link. A dry run
// writes nothing and reports what a write would change: that is how Status
// tells whether Install would change anything.
type homeDir struct {
	dir    string // as given: the paths Install reports are under it
	real   string // with its links resolved, for the checks
	dryRun bool
}

func openHome(dir string, dryRun bool) (*homeDir, error) {
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("home %q is not an absolute path", dir)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	return &homeDir{dir: dir, real: real, dryRun: dryRun}, nil
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
	fi, err := existing(p, h.path(rel))
	if err != nil || fi == nil {
		return nil, false, linkByHand(err)
	}
	b, err := os.ReadFile(p)
	return b, err == nil, err
}

// write makes the file rel hold data, and reports whether it had to change it;
// a dry run only reports it. A link is left to the user (ErrByHand).
func (h *homeDir) write(rel string, data []byte) (bool, error) {
	p, err := h.resolve(rel)
	if err != nil {
		return false, err
	}
	if h.dryRun {
		fi, err := existing(p, h.path(rel))
		if err != nil {
			return false, linkByHand(err)
		}
		return fi == nil || !holds(p, data), nil
	}
	changed, err := replaceFile(p, h.path(rel), data, 0)
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

// existing returns the file at p, or nil when there is none; name is how
// errors call it. A link, or anything else that is not a regular file, is
// refused.
func existing(p, name string) (fs.FileInfo, error) {
	fi, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s %w", name, errLink)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", name)
	}
	return fi, nil
}

// holds reports whether the file at p holds exactly data.
func holds(p string, data []byte) bool {
	cur, err := os.ReadFile(p)
	return err == nil && bytes.Equal(cur, data)
}

// replaceFile makes the file at p hold data; name is how errors call it. When
// the content differs it writes a temporary file next to it and renames that
// over p, so a reader sees the old content or the new, never half of it. With
// mode zero a new file is 0600 and a file that was there keeps its mode;
// otherwise the file gets mode, even when its content was right. Directories
// it makes are 0700. A link or anything else that is not a regular file is
// refused.
func replaceFile(p, name string, data []byte, mode fs.FileMode) (bool, error) {
	fi, err := existing(p, name)
	if err != nil {
		return false, err
	}
	if fi != nil && holds(p, data) {
		if mode != 0 && fi.Mode().Perm() != mode {
			return false, os.Chmod(p, mode)
		}
		return false, nil
	}
	perm := mode
	if perm == 0 {
		perm = 0o600
		if fi != nil {
			perm = fi.Mode().Perm()
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
	if err := f.Chmod(perm); err != nil {
		return false, err
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

// step changes one file under home and reports whether it did (or, in a dry
// run, would).
type step struct {
	rel string
	do  func(h *homeDir) (bool, error)
}

// run runs steps in order on home, for real or as a dry run, and returns the
// files they changed or would change. A step that leaves its file to the user
// (ErrByHand) does not stop the steps after it, and what each such step says
// comes back joined; any other error stops there.
func run(home string, dryRun bool, steps ...step) ([]string, error) {
	h, err := openHome(home, dryRun)
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

// install is Install: it runs the steps on home and returns the files they
// changed.
func install(home string, steps ...step) ([]string, error) {
	return run(home, false, steps...)
}

// statusOf is Status: whether Install would change nothing under home and
// leave nothing to the user, so that an install that is partial or names an
// older binary reads as not installed, and where the install's main file,
// rel, is.
func statusOf(home, rel string, steps ...step) (bool, string) {
	touched, err := run(home, true, steps...)
	return err == nil && len(touched) == 0, filepath.Join(home, filepath.FromSlash(rel))
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
	found := false
	rewriteStrings(raw, func(s string) (string, bool) {
		found = found || strings.Contains(s, marker)
		return "", false
	})
	return found
}

// rewriteStrings returns raw with every string value that f replaces
// replaced, and whether f replaced any. Everything else, the order of keys
// and the text of other values included, is kept as it was.
func rewriteStrings(raw json.RawMessage, f func(string) (string, bool)) (json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return raw, false
	}
	switch trimmed[0] {
	case '{':
		o, err := parseObject(trimmed)
		if err != nil {
			return raw, false
		}
		changed := false
		for _, k := range o.keys {
			if v, ok := rewriteStrings(o.vals[k], f); ok {
				o.vals[k], changed = v, true
			}
		}
		if !changed {
			return raw, false
		}
		return o.encode(), true
	case '[':
		var items []json.RawMessage
		if json.Unmarshal(trimmed, &items) != nil {
			return raw, false
		}
		changed := false
		for i := range items {
			if v, ok := rewriteStrings(items[i], f); ok {
				items[i], changed = v, true
			}
		}
		if !changed {
			return raw, false
		}
		return encodeList(items), true
	case '"':
		var s string
		if json.Unmarshal(trimmed, &s) != nil {
			return raw, false
		}
		if r, ok := f(s); ok {
			return json.RawMessage(`"` + jsonEscape(r) + `"`), true
		}
	}
	return raw, false
}

// ourCommand returns the command the asset's hooks run for marker: its first
// string that ends in " "+marker.
func ourCommand(asset []byte, marker string) (string, error) {
	var cmd string
	rewriteStrings(asset, func(s string) (string, bool) {
		if cmd == "" && strings.HasSuffix(s, " "+marker) {
			cmd = s
		}
		return "", false
	})
	if cmd == "" {
		return "", fmt.Errorf("the asset runs no %q", marker)
	}
	return cmd, nil
}

// staleCommand reports whether s is a hook command Conductor wrote for
// marker, `<absolute path> notify --x-hook` with the path quoted the way
// Conductor quotes it, naming another binary than ours does. A bare
// `conductor notify --x-hook` is the user's own, and anything longer or
// shaped otherwise is not Conductor's to rewrite.
func staleCommand(s, marker, ours string) bool {
	if s == ours {
		return false
	}
	quoted, ok := strings.CutSuffix(s, " "+marker)
	if !ok {
		return false
	}
	bin, ok := shellUnquote(quoted)
	return ok && filepath.IsAbs(bin)
}

// shellUnquote undoes shellQuote: ok is false for a word shellQuote would not
// have written.
func shellUnquote(q string) (string, bool) {
	if !strings.HasPrefix(q, "'") {
		return q, q != "" && shellQuote(q) == q
	}
	var b strings.Builder
	for rest := q; rest != ""; {
		switch {
		case strings.HasPrefix(rest, `\'`):
			b.WriteByte('\'')
			rest = rest[2:]
		case strings.HasPrefix(rest, "'"):
			end := strings.IndexByte(rest[1:], '\'')
			if end < 0 {
				return "", false
			}
			b.WriteString(rest[1 : 1+end])
			rest = rest[2+end:]
		default:
			return "", false
		}
	}
	return b.String(), shellQuote(b.String()) == q
}

// staleRewriter is the rewriteStrings function that gives Conductor's stale
// commands for marker the command ours.
func staleRewriter(marker, ours string) func(string) (string, bool) {
	return func(s string) (string, bool) {
		return ours, staleCommand(s, marker, ours)
	}
}

// hooksOf returns the "hooks" object of a JSON document, empty when there is
// none or it is null. ok is false when it is not an object.
func hooksOf(doc *object) (*object, bool) {
	raw, found := doc.get("hooks")
	if !found || isNull(raw) {
		return &object{vals: map[string]json.RawMessage{}}, true
	}
	hooks, err := parseObject(raw)
	return hooks, err == nil
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
// unless an entry there already mentions marker. Conductor's own entries that
// name an older binary get this one, where they are; bare `conductor` entries
// are the user's and stay. A file that is missing or empty becomes the asset.
// Everything else in the file is kept as it was, in its order, and the file is
// written with two-space indentation. It reports whether the file changed. A
// file that is not a JSON object with an object of lists under "hooks" is left
// alone: ErrByHand.
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
	add, ok := hooksOf(ours)
	if !ok {
		return false, fmt.Errorf("asset for %s: hooks is not an object", rel)
	}
	command, err := ourCommand(asset, marker)
	if err != nil {
		return false, fmt.Errorf("asset for %s: %w", rel, err)
	}
	hooks, ok := hooksOf(doc)
	if !ok {
		return false, byHand("in %s, \"hooks\" is not an object, so Conductor cannot merge its hooks into it; add them from the snippet", h.path(rel))
	}
	changed := false
	for _, event := range hooks.keys {
		if v, ok := rewriteStrings(hooks.vals[event], staleRewriter(marker, command)); ok {
			hooks.vals[event], changed = v, true
		}
	}
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

// createJSON writes asset as the JSON file rel when there is none. A file that
// is there is never overwritten: Conductor's own commands for marker in it
// that name an older binary get this one, where they are, and unless the file
// then holds every hook list of the asset, the rest is left to the user
// (ErrByHand, saying hint).
func createJSON(h *homeDir, rel string, asset []byte, marker, hint string) (bool, error) {
	cur, ok, err := h.read(rel)
	if err != nil {
		return false, err
	}
	if !ok || len(bytes.TrimSpace(cur)) == 0 {
		return writeJSON(h, rel, asset)
	}
	doc, err := parseObject(cur)
	if err != nil {
		return false, byHand("%s: %v; %s", h.path(rel), err, hint)
	}
	command, err := ourCommand(asset, marker)
	if err != nil {
		return false, fmt.Errorf("asset for %s: %w", rel, err)
	}
	changed := false
	if next, ok := rewriteStrings(cur, staleRewriter(marker, command)); ok {
		if changed, err = writeJSON(h, rel, next); err != nil {
			return false, err
		}
		if doc, err = parseObject(next); err != nil {
			return false, err
		}
	}
	ours, _ := parseObject(asset)
	want, _ := hooksOf(ours)
	have, isObject := hooksOf(doc)
	complete := isObject
	for _, event := range want.keys {
		raw, ok := have.get(event)
		var list []json.RawMessage
		complete = complete && ok && json.Unmarshal(raw, &list) == nil &&
			slices.ContainsFunc(list, func(e json.RawMessage) bool { return mentions(e, marker) })
	}
	if complete {
		return changed, nil
	}
	return changed, byHand("%s exists and Conductor does not overwrite it; %s", h.path(rel), hint)
}

// tomlDoc is a TOML document read line by line: enough to tell its root keys
// and a marked block from the text of its multi-line strings and arrays, and
// to put a block at its top.
type tomlDoc struct {
	bom   string   // a leading byte order mark, kept first
	lines []string // each with its "\n"
	top   []bool   // the line starts outside every multi-line string and array
}

// readTOML reads content. It refuses a document that ends inside a string or
// an array, or has a string that does not end on its line: this reader cannot
// tell where its root keys are.
func readTOML(content string) (*tomlDoc, error) {
	d := &tomlDoc{}
	if rest, ok := strings.CutPrefix(content, "\ufeff"); ok {
		d.bom, content = "\ufeff", rest
	}
	d.lines = strings.SplitAfter(content, "\n")
	d.top = make([]bool, len(d.lines))
	basic, literal, depth := false, false, 0 // inside """ or ''', open [ and {
	for i, l := range d.lines {
		d.top[i] = !basic && !literal && depth == 0
		for j := 0; j < len(l); j++ {
			switch {
			case basic:
				if l[j] == '\\' {
					j++
				} else if strings.HasPrefix(l[j:], `"""`) {
					basic, j = false, j+2
				}
			case literal:
				if strings.HasPrefix(l[j:], "'''") {
					literal, j = false, j+2
				}
			case l[j] == '#':
				j = len(l)
			case strings.HasPrefix(l[j:], `"""`):
				basic, j = true, j+2
			case strings.HasPrefix(l[j:], "'''"):
				literal, j = true, j+2
			case l[j] == '"':
				k := j + 1
				for k < len(l) && l[k] != '"' && l[k] != '\n' {
					if l[k] == '\\' {
						k++
					}
					k++
				}
				if k >= len(l) || l[k] != '"' {
					return nil, fmt.Errorf("the string on line %d does not end on it", i+1)
				}
				j = k
			case l[j] == '\'':
				k := strings.IndexAny(l[j+1:], "'\n")
				if k < 0 || l[j+1+k] != '\'' {
					return nil, fmt.Errorf("the string on line %d does not end on it", i+1)
				}
				j += k + 1
			case l[j] == '[' || l[j] == '{':
				depth++
			case l[j] == ']' || l[j] == '}':
				depth--
			}
		}
	}
	if basic || literal || depth != 0 {
		return nil, errors.New("it ends inside a string or an array")
	}
	return d, nil
}

// setsRootKey reports whether the document sets key at its root anywhere but
// in the block begin…end: on a line of its own above the first table header.
func (d *tomlDoc) setsRootKey(key, begin, end string) bool {
	inBlock := false
	for i, l := range d.lines {
		if !d.top[i] {
			continue
		}
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

// withBlock returns the document with body between the lines begin and end.
// A block that is there already is replaced where it is. A new one goes at the
// top, after a byte order mark: a key belongs to the table whose header comes
// before it, so a block appended below a [table] would not set a root key.
func (d *tomlDoc) withBlock(begin, end, body string) (string, error) {
	block := begin + "\n" + body + "\n" + end + "\n"
	for i, l := range d.lines {
		if !d.top[i] || strings.TrimSpace(l) != begin {
			continue
		}
		for j := i + 1; j < len(d.lines); j++ {
			if d.top[j] && strings.TrimSpace(d.lines[j]) == end {
				return d.bom + strings.Join(d.lines[:i], "") + block + strings.Join(d.lines[j+1:], ""), nil
			}
		}
		return "", fmt.Errorf("the line %q has no %q line after it", begin, end)
	}
	rest := strings.Join(d.lines, "")
	if strings.TrimSpace(rest) == "" {
		return d.bom + block, nil
	}
	return d.bom + block + "\n" + rest, nil
}

// withYAMLListItem returns content, a YAML document, with item added to the
// block list under the root key key: after the list's last entry, at its
// indentation, under a marker comment. Without the key, the list is added at
// the end. Content that mentions match already (match is item as written) is
// returned as it is. It reads lines, not YAML, and leaves to the user
// (ErrByHand) what it cannot edit that way: a key that is quoted, a value
// written inline or that is not a list of single entries, and a file of more
// than one document, where the entry could land in the wrong one.
func withYAMLListItem(content, key, marker, item, match string) (string, error) {
	if strings.Contains(content, match) {
		return content, nil
	}
	lines := strings.Split(content, "\n")
	started := false // past a leading "---" and the comments before it
	for _, l := range lines {
		t := strings.TrimRight(l, "\r")
		if t == "---" || strings.HasPrefix(t, "--- ") || t == "..." {
			if !started && t == "---" {
				started = true
				continue
			}
			return "", byHand("the file holds more than one YAML document; add %s to %s by hand", item, key)
		}
		if s := strings.TrimSpace(t); s != "" && !strings.HasPrefix(s, "#") {
			started = true
		}
	}
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
		lead := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
		entry := t == "-" || strings.HasPrefix(t, "- ")
		if lead == "" && !entry {
			break // the next root key, or a comment at the root
		}
		if strings.HasPrefix(t, "#") {
			continue
		}
		if !entry || !yamlScalarEntry(t) || (seen && lead != indent) {
			return "", byHand("%s is not a list of single entries; add %s to it by hand", key, item)
		}
		indent, last, seen = lead, i, true
	}
	lines = slices.Insert(lines, last+1, indent+marker, indent+"- "+item)
	return strings.Join(lines, "\n"), nil
}

// yamlScalarEntry reports whether the list entry line t ("- …") holds a plain
// or quoted scalar, not a mapping, a nested list or a flow collection.
func yamlScalarEntry(t string) bool {
	v := strings.TrimSpace(strings.TrimPrefix(t, "-"))
	if v == "" {
		return false
	}
	if v[0] == '"' || v[0] == '\'' {
		return true
	}
	if strings.ContainsRune("[{-&*!|>", rune(v[0])) {
		return false
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	return !strings.Contains(v, ": ") && !strings.HasSuffix(v, ":")
}
