// Package config loads the server configuration from an optional JSON file,
// applies CONDUCTOR_* environment overrides, fills defaults and validates.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/agents"
	"github.com/phenixrizen/conductor/internal/catalog"
	"github.com/phenixrizen/conductor/internal/store"
)

// FileView controls which share-link roles may read files from a session's
// working directory through the terminal viewer.
type FileView string

const (
	FileViewView    FileView = "view"    // both view and control roles may read files
	FileViewControl FileView = "control" // only control role may read files
	FileViewOff     FileView = "off"     // file reads disabled
)

// ICEServer mirrors the WebRTC ICE server description sent to browsers and hosts.
type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// Duration is a time.Duration that unmarshals from a JSON string such as "10m".
type Duration time.Duration

// UnmarshalJSON accepts a duration string or a number of nanoseconds.
func (d *Duration) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		v, err := time.ParseDuration(s)
		if err != nil {
			return err
		}
		*d = Duration(v)
		return nil
	}
	var n int64
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*d = Duration(n)
	return nil
}

// MarshalJSON renders the duration as a string.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// Config is the complete `conductor serve` configuration.
type Config struct {
	// Listen is the TCP address the HTTP server binds to.
	Listen string `json:"listen"`
	// PublicURL is the externally reachable base URL used to build share links.
	PublicURL string `json:"publicUrl"`
	// AdminToken protects launch and management routes. Generated when empty.
	AdminToken string `json:"adminToken"`
	// HostTokens authorize `conductor host` registrations. Empty means the admin token only.
	HostTokens []string `json:"hostTokens"`
	// AllowedOrigins lists WebSocket origin patterns beyond same-host.
	AllowedOrigins []string `json:"allowedOrigins"`
	// AllowedRoots are directories under which server-hosted sessions may run.
	AllowedRoots []string `json:"allowedRoots"`
	// DefaultCwd is used when a launch request omits cwd.
	DefaultCwd string `json:"defaultCwd"`
	// ScrollbackBytes is the per-session replay buffer size.
	ScrollbackBytes int `json:"scrollbackBytes"`
	// MaxSessions caps concurrent sessions (server-hosted and hosted).
	MaxSessions int `json:"maxSessions"`
	// MaxViewersPerSession caps attached clients per session.
	MaxViewersPerSession int `json:"maxViewersPerSession"`
	// ExitedRetention is how long exited sessions stay listed.
	ExitedRetention Duration `json:"exitedRetention"`
	// ICEServers are handed to browsers and hosts for WebRTC.
	ICEServers []ICEServer `json:"iceServers"`
	// RelayTimeoutMs is how long a viewer waits for a data channel before relaying.
	RelayTimeoutMs int `json:"relayTimeoutMs"`
	// EnvPassthrough lists extra environment variables copied into PTYs.
	EnvPassthrough []string `json:"envPassthrough"`
	// FileView controls which roles may read session files.
	FileView FileView `json:"fileView"`
	// Catalog holds inline agent definitions.
	Catalog catalog.File `json:"catalog"`
	// CatalogPath points at a separate catalog JSON file. Validate makes it
	// absolute, relative to the current directory.
	CatalogPath string `json:"catalogPath"`
	// DataDir is the writable directory for UI-managed state: catalog overlay, crews, generated hook assets.
	DataDir string `json:"dataDir"`
	// Webhooks are URLs the server POSTs activity entries to, by event type.
	// Their secrets never appear in logs or API responses.
	Webhooks []Webhook `json:"webhooks"`
	// Dev relaxes origin checks for the Nuxt dev server on localhost:3000.
	Dev bool `json:"dev"`
	// Yolo launches every agent with its yolo recipe (catalog.Agent.Yolo),
	// unless a launch or a crew says otherwise: CONDUCTOR_YOLO, conductor
	// serve --yolo.
	Yolo bool `json:"yolo"`

	// GeneratedAdminToken is true when AdminToken was created at startup.
	GeneratedAdminToken bool `json:"-"`
	// Examples seeds the example crews once at startup: CONDUCTOR_EXAMPLES=1
	// (or true), or conductor serve --examples. Not a config-file key: the
	// decoder ignores it, so an "examples" key in the file is rejected.
	Examples bool `json:"-"`
	// Path is the absolute path of the config file Load read, or "" when there
	// was none. It is not a config key: the decoder ignores it, so a "path" key
	// in the file is rejected as unknown.
	Path string `json:"-"`

	// warnings are what the last Validate found worth saying but not wrong.
	warnings []string
}

// Defaults returns the configuration used when nothing is specified.
func Defaults() *Config {
	cwd, _ := os.Getwd()
	return &Config{
		Listen:               ":8080",
		PublicURL:            "http://localhost:8080",
		AllowedRoots:         []string{cwd},
		DefaultCwd:           cwd,
		ScrollbackBytes:      256 << 10,
		MaxSessions:          32,
		MaxViewersPerSession: 32,
		ExitedRetention:      Duration(10 * time.Minute),
		ICEServers:           []ICEServer{{URLs: []string{"stun:stun.l.google.com:19302"}}},
		RelayTimeoutMs:       8000,
		FileView:             FileViewView,
	}
}

// Load reads path (optional), applies environment overrides and validates.
// The file it read is recorded in Path.
func Load(path string) (*Config, error) {
	cfg := Defaults()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read config: %w", err)
		}
		if err := store.DecodeStrict(b, cfg); err != nil {
			return nil, fmt.Errorf("parse config %s: %w", path, err)
		}
		cfg.Path = path
		if abs, err := filepath.Abs(path); err == nil {
			cfg.Path = abs
		}
	}
	if err := applyEnv(cfg, os.Getenv); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func applyEnv(cfg *Config, getenv func(string) string) error {
	str := func(key string, dst *string) {
		if v := getenv(key); v != "" {
			*dst = v
		}
	}
	list := func(key string, dst *[]string) {
		if v := getenv(key); v != "" {
			var out []string
			for _, p := range strings.Split(v, ",") {
				if p = strings.TrimSpace(p); p != "" {
					out = append(out, p)
				}
			}
			*dst = out
		}
	}
	num := func(key string, dst *int) error {
		if v := getenv(key); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			*dst = n
		}
		return nil
	}
	str("CONDUCTOR_LISTEN", &cfg.Listen)
	str("CONDUCTOR_PUBLIC_URL", &cfg.PublicURL)
	str("CONDUCTOR_ADMIN_TOKEN", &cfg.AdminToken)
	list("CONDUCTOR_HOST_TOKENS", &cfg.HostTokens)
	list("CONDUCTOR_ALLOWED_ORIGINS", &cfg.AllowedOrigins)
	list("CONDUCTOR_ALLOWED_ROOTS", &cfg.AllowedRoots)
	str("CONDUCTOR_DEFAULT_CWD", &cfg.DefaultCwd)
	list("CONDUCTOR_ENV_PASSTHROUGH", &cfg.EnvPassthrough)
	str("CONDUCTOR_CATALOG_PATH", &cfg.CatalogPath)
	str("CONDUCTOR_DATA_DIR", &cfg.DataDir)
	if v := getenv("CONDUCTOR_FILE_VIEW"); v != "" {
		cfg.FileView = FileView(v)
	}
	if v := getenv("CONDUCTOR_DEV"); v == "1" || v == "true" {
		cfg.Dev = true
	}
	if v := getenv("CONDUCTOR_EXAMPLES"); v == "1" || v == "true" {
		cfg.Examples = true
	}
	switch getenv("CONDUCTOR_YOLO") {
	case "1", "true":
		cfg.Yolo = true
	case "0", "false":
		cfg.Yolo = false
	}
	for key, dst := range map[string]*int{
		"CONDUCTOR_SCROLLBACK_BYTES":        &cfg.ScrollbackBytes,
		"CONDUCTOR_MAX_SESSIONS":            &cfg.MaxSessions,
		"CONDUCTOR_MAX_VIEWERS_PER_SESSION": &cfg.MaxViewersPerSession,
		"CONDUCTOR_RELAY_TIMEOUT_MS":        &cfg.RelayTimeoutMs,
	} {
		if err := num(key, dst); err != nil {
			return err
		}
	}
	if v := getenv("CONDUCTOR_EXITED_RETENTION"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("CONDUCTOR_EXITED_RETENTION: %w", err)
		}
		cfg.ExitedRetention = Duration(d)
	}
	if v := getenv("CONDUCTOR_WEBHOOKS"); v != "" {
		hooks, err := parseWebhooks(v)
		if err != nil {
			return fmt.Errorf("CONDUCTOR_WEBHOOKS: %w", err)
		}
		cfg.Webhooks = hooks
	}
	if v := getenv("CONDUCTOR_ICE_SERVERS"); v != "" {
		var servers []ICEServer
		for _, u := range strings.Split(v, ",") {
			if u = strings.TrimSpace(u); u != "" {
				servers = append(servers, ICEServer{URLs: []string{u}})
			}
		}
		cfg.ICEServers = servers
	}
	return nil
}

// Validate checks bounds and normalizes paths. What it finds worth saying
// but not wrong, it keeps for Warnings.
func (c *Config) Validate() error {
	var errs []error
	c.warnings = nil
	if c.Listen == "" {
		errs = append(errs, errors.New("listen must not be empty"))
	}
	if c.PublicURL == "" {
		errs = append(errs, errors.New("publicUrl must not be empty"))
	}
	c.PublicURL = strings.TrimRight(c.PublicURL, "/")
	if c.ScrollbackBytes < 4096 || c.ScrollbackBytes > 64<<20 {
		errs = append(errs, errors.New("scrollbackBytes must be between 4096 and 67108864"))
	}
	if c.MaxSessions < 1 || c.MaxSessions > 10000 {
		errs = append(errs, errors.New("maxSessions must be between 1 and 10000"))
	}
	if c.MaxViewersPerSession < 1 || c.MaxViewersPerSession > 1000 {
		errs = append(errs, errors.New("maxViewersPerSession must be between 1 and 1000"))
	}
	if c.RelayTimeoutMs < 500 || c.RelayTimeoutMs > 120000 {
		errs = append(errs, errors.New("relayTimeoutMs must be between 500 and 120000"))
	}
	if time.Duration(c.ExitedRetention) < 0 {
		errs = append(errs, errors.New("exitedRetention must not be negative"))
	}
	switch c.FileView {
	case FileViewView, FileViewControl, FileViewOff:
	default:
		errs = append(errs, fmt.Errorf("fileView must be view, control or off, got %q", c.FileView))
	}
	for i, root := range c.AllowedRoots {
		abs, err := filepath.Abs(root)
		if err != nil {
			errs = append(errs, fmt.Errorf("allowedRoots[%d]: %w", i, err))
			continue
		}
		c.AllowedRoots[i] = abs
	}
	if len(c.AllowedRoots) == 0 {
		errs = append(errs, errors.New("allowedRoots must not be empty"))
	}
	if c.DefaultCwd != "" {
		abs, err := filepath.Abs(c.DefaultCwd)
		if err != nil {
			errs = append(errs, fmt.Errorf("defaultCwd: %w", err))
		} else {
			c.DefaultCwd = abs
		}
	}
	if c.CatalogPath != "" {
		abs, err := filepath.Abs(c.CatalogPath)
		if err != nil {
			errs = append(errs, fmt.Errorf("catalogPath: %w", err))
		} else {
			c.CatalogPath = abs
		}
	}
	for i, s := range c.ICEServers {
		if len(s.URLs) == 0 {
			errs = append(errs, fmt.Errorf("iceServers[%d]: urls must not be empty", i))
		}
	}
	hookErrs, warnings := c.validateWebhooks()
	errs = append(errs, hookErrs...)
	c.warnings = warnings
	return errors.Join(errs...)
}

// Warnings are what the last Validate found worth saying but not wrong: a
// webhook host that did not resolve. conductor serve logs them.
func (c *Config) Warnings() []string { return c.warnings }

// ErrNoHome is in the error ResolveDataDir returns when the data directory
// is the default, ~/.conductor, and the home directory is unknown. Callers
// that choose the directory with settings of their own (conductor hooks
// --data-dir) name those instead.
var ErrNoHome = errors.New("the home directory is unknown")

// ResolveDataDir fills DataDir when neither dataDir nor CONDUCTOR_DATA_DIR set
// it: ~/.conductor, in the home of the user running conductor serve, never
// next to the config file or in the working directory. An older Conductor
// chose conductor.d next to the config file, or in the working directory
// without one. While ~/.conductor holds no server data (holdsServerData) and
// that directory exists, it is kept, and notice says so, naming both and how
// to move. conductor host writes ~/.conductor/hooks for itself, so hooks/
// alone does not end the rule. Without a home directory that old directory
// is kept too, so an upgraded service started without HOME keeps working;
// with no old directory either, the error (ErrNoHome) names the settings
// that choose one. When ~/.conductor wins and the old directory holds server
// data as well, notice names the directory this server no longer reads. A
// ~/.conductor that cannot be looked into is an error. An old directory is
// kept only when it is a real directory of the user running the server
// (oldDataDir): one that would be kept but is a link or another user's is an
// error naming it, its owner and the settings.
//
// The result is absolute: agent processes started in other working
// directories are handed paths under it. A value that was set is made
// absolute relative to the current directory, like allowedRoots and
// defaultCwd, and is kept as it is when the working directory cannot be
// determined.
func (c *Config) ResolveDataDir(configPath string) (notice string, err error) {
	if c.DataDir == "" {
		if notice, err = c.defaultDataDir(configPath); err != nil {
			return "", err
		}
	}
	if abs, aerr := filepath.Abs(c.DataDir); aerr == nil {
		c.DataDir = abs
	}
	return notice, nil
}

// defaultDataDir sets DataDir when nothing chose it, by the rules
// ResolveDataDir describes, and returns what the server should say about it.
func (c *Config) defaultDataDir(configPath string) (notice string, err error) {
	old, aerr := filepath.Abs(legacyDataDir(configPath))
	var hasOld bool
	var refused error // why old, which would be kept, is not
	if aerr == nil {
		hasOld, refused = oldDataDir(old)
	}
	home, herr := os.UserHomeDir()
	if herr != nil || !filepath.IsAbs(home) {
		if refused != nil {
			return "", refused
		}
		if !hasOld {
			return "", fmt.Errorf("the data directory defaults to ~/.conductor, but %w: set dataDir in the config, or CONDUCTOR_DATA_DIR", ErrNoHome)
		}
		c.DataDir = old
		return fmt.Sprintf("using the data directory %s, where an older Conductor put it; the default is now ~/.conductor, but the home directory is unknown. To keep it where it is, set dataDir or CONDUCTOR_DATA_DIR to it", old), nil
	}
	def := filepath.Join(home, ".conductor")
	held, err := holdsServerData(def)
	if err != nil {
		return "", fmt.Errorf("data directory %s is not usable (%w); set dataDir in the config or CONDUCTOR_DATA_DIR to a writable directory", def, err)
	}
	if !held && refused != nil {
		return "", refused
	}
	c.DataDir = def
	switch {
	case !hasOld:
		// Nothing older to keep or to name. A refused directory is not
		// named either once ~/.conductor holds server data: it is not used.
	case !held:
		c.DataDir = old
		notice = fmt.Sprintf("using the data directory %s, where an older Conductor put it; the default is now %s, which holds no server data yet (catalog.json, crews/ or crews.json). To move it, stop the server, move the files in %s into %s (hooks/ need not move: the server writes it at every start) and start it again; to keep it where it is, set dataDir or CONDUCTOR_DATA_DIR to it", old, def, old, def)
	default:
		// What cannot be looked into is left alone: the server reads def.
		if oldHeld, _ := holdsServerData(old); oldHeld {
			notice = fmt.Sprintf("using the data directory %s; %s, where an older Conductor put it, holds server data too, which this server does not read. Move what you need from it into %s while the server is stopped, or set dataDir or CONDUCTOR_DATA_DIR to it to use it instead", def, old, def)
		}
	}
	return notice, nil
}

// serverData names what only a server writes in its data directory: the
// Agents page's overlay and the crews, in the layout of this version and of
// the one before. conductor host writes hooks/ into ~/.conductor too, so
// hooks/ is not among them.
var serverData = []string{"catalog.json", "crews", "crews.json"}

// holdsServerData reports whether dir holds anything in serverData, as a
// file, a directory or a link. A dir that does not exist holds nothing; one
// that cannot be looked into (no permission, a file in its place) is an
// error, never a directory without server data.
func holdsServerData(dir string) (bool, error) {
	for _, name := range serverData {
		_, err := os.Lstat(filepath.Join(dir, name))
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return false, err
		}
	}
	return false, nil
}

// legacyDataDir is where an older Conductor put the data directory by
// default: conductor.d next to the config file, or in the working directory
// without one.
func legacyDataDir(configPath string) string {
	base := "."
	if configPath != "" {
		base = filepath.Dir(configPath)
	}
	return filepath.Join(base, "conductor.d")
}

// geteuid is the user conductor serve runs as. Tests replace it.
var geteuid = os.Geteuid

// oldDataDir reports whether old, where an older Conductor put the data
// directory, is one to keep: a real directory, not a link, that belongs to
// the user running the server (where the system says who owns a file; a real
// directory suffices elsewhere). Nothing there, or something that is neither
// a directory nor a link (a file), is no old directory, as before. A link or
// another user's directory is refused with an error naming it, its owner and
// the settings that choose a data directory: in a shared working directory
// anyone may make ./conductor.d, and its catalog.json would choose the
// commands the server launches.
func oldDataDir(old string) (bool, error) {
	fi, err := os.Lstat(old)
	if err != nil {
		return false, nil
	}
	const settings = "set dataDir in the config or CONDUCTOR_DATA_DIR to the data directory to use (the default is ~/.conductor)"
	owner, known := agents.FileOwner(fi)
	uid := geteuid()
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		by := ""
		if known {
			by = fmt.Sprintf(" owned by uid %d", owner)
		}
		return false, fmt.Errorf("%s, where an older Conductor put the data directory, is a symbolic link%s, and an old data directory is kept only when it is a real directory of the user running conductor serve (uid %d), as its catalog.json chooses the commands agents run: %s", old, by, uid, settings)
	case !fi.IsDir():
		return false, nil
	case known && owner != uid:
		return false, fmt.Errorf("%s, where an older Conductor put the data directory, belongs to uid %d, not to uid %d, which runs conductor serve, and an old data directory is kept only when it is the server's own, as its catalog.json chooses the commands agents run: %s", old, owner, uid, settings)
	}
	return true, nil
}

// LoadCatalog merges the inline catalog and the optional catalog file.
func (c *Config) LoadCatalog() (catalog.Catalog, error) {
	file := c.Catalog
	if c.CatalogPath != "" {
		extra, err := catalog.ReadFile(c.CatalogPath)
		if err != nil {
			return catalog.Catalog{}, err
		}
		file.DisableDefaults = file.DisableDefaults || extra.DisableDefaults
		file.Agents = append(file.Agents, extra.Agents...)
	}
	cat, err := catalog.Load(file)
	if err != nil {
		return catalog.Catalog{}, err
	}
	// The catalog cannot check an adapter (the adapters import it): the
	// config's agents are checked here, as the Agents page checks the ones it
	// saves. Each error names the catalog the agent comes from: the inline
	// agents come first in file.Agents, the catalog file's after them.
	var errs []error
	for i, a := range file.Agents {
		if err := agents.CheckAdapter(a.Adapter); err != nil {
			src := "config catalog"
			if i >= len(c.Catalog.Agents) {
				src = "catalog " + c.CatalogPath
			}
			errs = append(errs, fmt.Errorf("%s: agent %s: %w", src, a.ID, err))
		}
	}
	if len(errs) > 0 {
		return catalog.Catalog{}, errors.Join(errs...)
	}
	return cat, nil
}

// DataDirOverlap returns the first allowed root that overlaps DataDir: one that
// contains it, is inside it or is the same directory. It returns "" when none
// does. Paths are compared with the symlinks of their existing part resolved,
// so a data directory not created yet is placed correctly too. An overlap is a
// misconfiguration: agents working in the root can read the data directory and
// commit its secrets, and the file viewer refuses the whole data directory, so
// it reads nothing in a root inside it.
func (c *Config) DataDirOverlap() string {
	data := resolved(c.DataDir)
	for _, root := range c.AllowedRoots {
		r := resolved(root)
		if within(data, r) || within(r, data) {
			return root
		}
	}
	return ""
}

// resolved returns p with its symlinks evaluated. Where p does not exist (yet)
// its longest existing ancestor is evaluated and the rest joined back on, so a
// data directory not created yet compares with a root that does exist. When
// even that fails, p is only cleaned.
func resolved(p string) string {
	if real, err := evalExisting(p); err == nil {
		return real
	}
	return filepath.Clean(p)
}

// evalExisting is filepath.EvalSymlinks for a path whose last elements need
// not exist. It mirrors the helper of the same name in internal/session, which
// this package does not import.
func evalExisting(p string) (string, error) {
	real, err := filepath.EvalSymlinks(p)
	if err == nil {
		return real, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	dir, base := filepath.Split(filepath.Clean(p))
	if dir == "" || dir == p {
		return "", err
	}
	parent, err := evalExisting(filepath.Clean(dir))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, base), nil
}

// within reports whether path is dir or inside it. Both must be absolute.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
