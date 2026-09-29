package agents

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

// binPlaceholder stands for the conductor binary in an asset. It is always
// inside a string: a shell command in a JSON asset, a string literal in a
// script asset.
const binPlaceholder = "{{BIN}}"

// executable finds the conductor binary the hooks run when an adapter renders
// something itself: its launch flags and environment, and an asset missing
// from the hooks dir. Tests replace it.
var executable = os.Executable

// binPath returns the running conductor binary, checked the way WriteAssets
// checks the one it is given.
func binPath() (string, error) {
	bin, err := executable()
	if err != nil {
		return "", fmt.Errorf("locate the conductor binary: %w", err)
	}
	return bin, checkBin(bin)
}

func checkBin(bin string) error {
	if !filepath.IsAbs(bin) {
		return fmt.Errorf("the conductor binary %q is not an absolute path", bin)
	}
	// JSON, TOML and JavaScript strings hold text: a path that is not UTF-8
	// cannot be written into them faithfully.
	if !utf8.ValidString(bin) {
		return fmt.Errorf("the conductor binary path %q is not UTF-8", bin)
	}
	return nil
}

// WriteAssets renders every adapter's assets for bin, the absolute path of the
// conductor binary, and writes them under hooksDir: directories 0700, files
// 0600. Each file is replaced whole, so an agent reading one never sees half
// of it, and one that already holds the right content is left alone.
func WriteAssets(hooksDir, bin string) error {
	if !filepath.IsAbs(hooksDir) {
		return fmt.Errorf("the hooks directory %q is not an absolute path", hooksDir)
	}
	if err := checkBin(bin); err != nil {
		return err
	}
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		return err
	}
	for _, a := range registry {
		for _, rel := range slices.Sorted(maps.Keys(a.Assets)) {
			content, err := render(rel, a.Assets[rel], bin)
			if err != nil {
				return err
			}
			p := filepath.Join(hooksDir, filepath.FromSlash(rel))
			if _, err := replaceFile(p, p, []byte(content)); err != nil {
				return err
			}
		}
	}
	return nil
}

// HooksDir is where `conductor serve` keeps the hook assets: hooks/ in its
// data directory. It is empty when there is no data directory.
func HooksDir(dataDir string) string {
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, "hooks")
}

// HostHooksDir is where `conductor host` keeps the hook assets it injects:
// $XDG_STATE_HOME/conductor/hooks, or ~/.local/state/conductor/hooks when
// XDG_STATE_HOME is unset or, against the XDG spec, not absolute.
func HostHooksDir() (string, error) {
	if state := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(state) {
		return filepath.Join(state, "conductor", "hooks"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "conductor", "hooks"), nil
}

// render puts bin in place of the placeholder in the asset rel, escaped for
// where the placeholder sits: in a JSON asset a hook runner hands the command
// to a shell, so the path is quoted for the shell and then for JSON; in a
// script asset it is the content of a double-quoted string literal.
func render(rel, tmpl, bin string) (string, error) {
	var with string
	switch path.Ext(rel) {
	case ".json":
		with = jsonEscape(shellQuote(bin))
	case ".ts", ".js":
		with = jsonEscape(bin)
	default:
		if strings.Contains(tmpl, binPlaceholder) {
			return "", fmt.Errorf("asset %s: no escaping known for the binary path in this kind of file", rel)
		}
	}
	return strings.ReplaceAll(tmpl, binPlaceholder, with), nil
}

// assetFor returns the asset rel as Conductor wrote it under hooksDir or, when
// it is not there (no server has written the assets yet), rendered for the
// running binary.
func assetFor(assets map[string]string, hooksDir, rel string) ([]byte, error) {
	tmpl, ok := assets[rel]
	if !ok {
		return nil, fmt.Errorf("no asset %s", rel)
	}
	if filepath.IsAbs(hooksDir) {
		b, err := os.ReadFile(filepath.Join(hooksDir, filepath.FromSlash(rel)))
		if err == nil {
			return b, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	bin, err := binPath()
	if err != nil {
		return nil, err
	}
	s, err := render(rel, tmpl, bin)
	return []byte(s), err
}

// snippetOf is the asset rel as text to paste, or a note of why there is none.
func snippetOf(assets map[string]string, hooksDir, rel string) string {
	b, err := assetFor(assets, hooksDir, rel)
	if err != nil {
		return "# no snippet: " + err.Error() + "\n"
	}
	return string(b)
}

// shellQuote quotes s as one word for a POSIX shell. A word made only of
// characters no shell treats specially is left as it is.
func shellQuote(s string) string {
	safe := s != ""
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._-+,:@%=", r)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// jsonEscape returns s escaped for the inside of a JSON string, which is also
// the inside of a double-quoted JavaScript string.
func jsonEscape(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // a string always encodes
	out := bytes.TrimSuffix(b.Bytes(), []byte("\n"))
	return string(out[1 : len(out)-1])
}

// tomlString returns s as a TOML basic string. JSON's escapes are TOML's,
// except that TOML also wants DEL escaped.
func tomlString(s string) string {
	return `"` + strings.ReplaceAll(jsonEscape(s), "\x7f", `\u007f`) + `"`
}

// hookLists returns `"event": [entry]` for each event, joined for a JSON
// object: the same single entry for every event.
func hookLists(entry string, events ...string) string {
	parts := make([]string, 0, len(events))
	for _, e := range events {
		parts = append(parts, `"`+e+`":[`+entry+`]`)
	}
	return strings.Join(parts, ",")
}

// jsonAsset returns the fixed JSON text of an asset indented with two spaces,
// as every JSON file Conductor writes is. The text is part of the program, so
// an error is a bug the tests catch at once.
func jsonAsset(compact string) string {
	b, err := indentJSON([]byte(compact))
	if err != nil {
		panic(fmt.Sprintf("agents: asset is not JSON: %v\n%s", err, compact))
	}
	return string(b)
}
