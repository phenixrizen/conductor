package agents

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"os/user"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/phenixrizen/conductor/internal/version"
)

// binPlaceholder stands for the conductor binary in an asset. It is always
// inside a string: a shell command in a JSON asset, a string literal in a
// script asset.
const binPlaceholder = "{{BIN}}"

// executable finds the conductor binary the hooks run when an adapter renders
// something itself: its launch flags and environment, an asset missing from
// the hooks dir, and what Status compares an install with. Tests replace it.
var executable = assetBinary

var (
	assetBinMu sync.Mutex
	// assetBin is the binary WriteAssets last wrote the assets for.
	assetBin string
	// lookedUp is BinaryPath, asked once.
	lookedUp = sync.OnceValues(BinaryPath)
)

// assetBinary is the binary the hooks dir names: the one WriteAssets last
// wrote the assets for or AdoptBinary adopted or, in a process that has done
// neither, BinaryPath as it answered the first time. It never follows a
// later change to PATH, so a running server's Status compares an install
// with what its Install copies from the hooks dir, and a launch names the
// same binary as the assets, even after an upgrade has changed the conductor
// on PATH.
func assetBinary() (string, error) {
	assetBinMu.Lock()
	bin := assetBin
	assetBinMu.Unlock()
	if bin != "" {
		return bin, nil
	}
	return lookedUp()
}

// remember makes bin the binary the adapters name; "" leaves it to
// BinaryPath.
func remember(bin string) {
	assetBinMu.Lock()
	assetBin = bin
	assetBinMu.Unlock()
}

// Binary is the conductor binary this process's hooks run, as the adapters
// name it (see assetBinary). Sessions get it as CONDUCTOR_BIN, so that a
// command an agent runs itself names the binary its hooks do.
func Binary() (string, error) {
	return binPath()
}

// binRecord is the file in the hooks dir that names the binary its assets
// were written for, so that another conductor process can name the same one
// (AdoptBinary).
const binRecord = ".bin"

// maxBinRecord bounds what AdoptBinary reads: a path of at most PATH_MAX.
const maxBinRecord = 4096

// versionRecord is the file in the hooks dir that names the version of the
// conductor that wrote its assets, for conductor hooks to compare with its own.
const versionRecord = ".version"

// RecordedVersion returns the version of the conductor that wrote the assets
// in hooksDir; fs.ErrNotExist when there is no record, as an older conductor
// wrote none.
func RecordedVersion(hooksDir string) (string, error) {
	p := filepath.Join(hooksDir, versionRecord)
	fi, err := os.Lstat(p)
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() || fi.Size() > 256 {
		return "", fmt.Errorf("%s is not a version record: not a regular file of at most 256 bytes", p)
	}
	b, err := os.ReadFile(p)
	return strings.TrimSpace(string(b)), err
}

// AdoptBinary makes the binary the hook assets in hooksDir were written for
// the one this process's adapters name, as they are in the process that
// wrote them, and returns it. A conductor that installs or checks hooks from
// a server's hooks dir adopts the server's binary this way, so what it
// installs and what it checks agree. The error is fs.ErrNotExist when no
// assets record a binary there; on any error the binary stays as it was.
func AdoptBinary(hooksDir string) (string, error) {
	if !filepath.IsAbs(hooksDir) {
		return "", fmt.Errorf("the hooks directory %q is not an absolute path", hooksDir)
	}
	p := filepath.Join(hooksDir, binRecord)
	fi, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%s: %w", p, fs.ErrNotExist)
	}
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() || fi.Size() > maxBinRecord {
		return "", fmt.Errorf("%s is not the record of a binary: not a regular file of at most %d bytes", p, maxBinRecord)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	bin := string(b)
	if err := checkBin(bin); err != nil {
		return "", fmt.Errorf("%s: %w", p, err)
	}
	remember(bin)
	return bin, nil
}

// CheckHooksDir refuses a hooks dir that is not as conductor serve writes it:
// owned by the user running conductor and 0700, with no permission for its
// group or others. conductor hooks copies the hooks it finds there into the
// agents' own configs and adopts the binary it names (AdoptBinary), so a
// hooks dir from elsewhere, such as the ./conductor.d/hooks of a checkout,
// which is the user's own and 0755, would otherwise choose the commands the
// user's agents run. A hooks dir that does not exist passes: there is nothing
// in it to take. It takes a system that says who owns a file, as Unix
// systems do; elsewhere it passes.
func CheckHooksDir(hooksDir string) error {
	fi, err := os.Stat(hooksDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	owner, ok := fileOwner(fi)
	if !ok {
		return nil
	}
	const why = "conductor hooks takes the hooks it installs, and the binary they run, only from a directory of yours that is 0700, as conductor serve writes it"
	if uid := geteuid(); owner != uid {
		who := strconv.Itoa(owner)
		if u, err := user.LookupId(who); err == nil && u.Username != "" {
			who = u.Username
		}
		return fmt.Errorf("%s belongs to %s: %s", hooksDir, who, why)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("%s is open to its group or others (mode %04o): %s; chmod 700 it if it is yours, or let conductor serve write it again", hooksDir, perm, why)
	}
	return nil
}

// ForgetBinary forgets the binary WriteAssets or AdoptBinary recorded, as in
// a process that recorded none, and returns a function that puts back what
// it forgot. Both record the binary for the whole process: a test that calls
// them restores it with this.
func ForgetBinary() (restore func()) {
	assetBinMu.Lock()
	old := assetBin
	assetBin = ""
	assetBinMu.Unlock()
	return func() { remember(old) }
}

// BinaryPath is the conductor binary for hooks to run: the conductor on PATH
// when that is this very binary, and this binary's own path otherwise.
// Package managers (Homebrew, Nix, a /usr/local/bin link to a versioned
// directory) install conductor as a link to a path that changes with every
// version: hooks that name the link survive an upgrade, hooks that name the
// versioned path stop working when the old version is removed.
func BinaryPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	// LookPath fails for a match in a relative PATH entry, which is no path
	// to write into a config file.
	if onPath, err := exec.LookPath("conductor"); err == nil && filepath.IsAbs(onPath) {
		a, errA := os.Stat(onPath)
		b, errB := os.Stat(exe)
		if errA == nil && errB == nil && os.SameFile(a, b) {
			return onPath, nil
		}
	}
	return exe, nil
}

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

// chmod is os.Chmod: a test replaces it to make a mode fail.
var chmod = os.Chmod

// ModeError is what WriteAssets returns when it wrote every asset but could
// not set the mode of some of them, or of a hooks dir of the process's own
// (a file system that keeps no modes, say). The assets are in place and name
// the binary; conductor serve and conductor host warn and go on with them.
// Errs says which.
type ModeError struct{ Errs []error }

func (e *ModeError) Error() string {
	msgs := make([]string, len(e.Errs))
	for i, err := range e.Errs {
		msgs[i] = err.Error()
	}
	return "the hook assets are in place, but their modes (0600 files in a 0700 directory) could not all be set, and Conductor goes on with them: " + strings.Join(msgs, "; ")
}

func (e *ModeError) Unwrap() []error { return e.Errs }

// ownHooksDir returns nil when the user the process runs as owns the hooks
// directory dir, whose mode could not be set (why), and an error naming the
// directory and its owner otherwise: only its owner can set a directory's
// mode, and another user's directory is no place to take the commands agents
// run from. Where the system does not say who owns a file, it passes.
func ownHooksDir(dir string, why error) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	owner, ok := fileOwner(fi)
	if uid := geteuid(); ok && owner != uid {
		return fmt.Errorf("the hooks directory %s belongs to uid %d, not to uid %d, which runs conductor, so its mode cannot be made 0700 (%w); its hooks would choose the commands agents run: make it yours, or use another data directory", dir, owner, uid, why)
	}
	return nil
}

// WriteAssets renders every adapter's assets for bin, the absolute path of the
// conductor binary, and writes them under hooksDir with the Conductor skill,
// and bin itself as .bin for AdoptBinary. Conductor owns the directory: it is
// made 0700 and every asset 0600, whatever they were. Each file is replaced
// whole, so an agent reading one never sees half of it, and one that already
// holds the right content is not rewritten. From then on the adapters name
// bin wherever they render the binary themselves. A mode it cannot set stops
// nothing: the assets are all written, and the error is a *ModeError. The one
// exception is the hooks dir itself when another user owns it: that is an
// error, and nothing is written. It also records version.Version as .version.
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
	var modes []error
	if err := chmod(hooksDir, 0o700); err != nil {
		if err := ownHooksDir(hooksDir, err); err != nil {
			return err
		}
		modes = append(modes, err)
	}
	put := func(p string, data []byte) error {
		_, err := replaceFile(p, p, data, 0o600)
		var me *modeError
		if errors.As(err, &me) {
			modes = append(modes, me.err)
			return nil
		}
		return err
	}
	sets := []map[string]string{skillAssets}
	for _, a := range registry {
		sets = append(sets, a.Assets)
	}
	for _, assets := range sets {
		for _, rel := range slices.Sorted(maps.Keys(assets)) {
			content, err := render(rel, assets[rel], bin)
			if err != nil {
				return err
			}
			if err := put(filepath.Join(hooksDir, filepath.FromSlash(rel)), []byte(content)); err != nil {
				return err
			}
		}
	}
	if err := put(filepath.Join(hooksDir, binRecord), []byte(bin)); err != nil {
		return err
	}
	if err := put(filepath.Join(hooksDir, versionRecord), []byte(version.Version)); err != nil {
		return err
	}
	remember(bin)
	if len(modes) > 0 {
		return &ModeError{Errs: modes}
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

// HostHooksDir is where conductor host keeps the hook assets it injects:
// hooks in ~/.conductor, the data directory conductor serve uses by default.
// An older Conductor kept them in $XDG_STATE_HOME/conductor/hooks, or in
// ~/.local/state/conductor/hooks when XDG_STATE_HOME is unset or, against the
// XDG spec, not absolute. While ~/.conductor/hooks does not exist and that
// directory does, it is kept, and legacy is true. conductor serve does not
// count a ~/.conductor that holds only hooks/ as its data
// (config.ResolveDataDir), so what the host writes here never moves a
// server's data directory.
func HostHooksDir() (dir string, legacy bool, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, err
	}
	if !filepath.IsAbs(home) {
		return "", false, fmt.Errorf("the home directory %q is not an absolute path", home)
	}
	def := filepath.Join(home, ".conductor", "hooks")
	if dirExists(def) {
		return def, false, nil
	}
	old := filepath.Join(home, ".local", "state", "conductor", "hooks")
	if state := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(state) {
		old = filepath.Join(state, "conductor", "hooks")
	}
	if dirExists(old) {
		return old, true, nil
	}
	return def, false, nil
}

// dirExists reports whether p is a directory, following links.
func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
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
