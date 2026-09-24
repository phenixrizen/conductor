// Package config loads the server configuration from an optional JSON file,
// applies CONDUCTOR_* environment overrides, fills defaults and validates.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/catalog"
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
	// CatalogPath points at a separate catalog JSON file.
	CatalogPath string `json:"catalogPath"`
	// Dev relaxes origin checks for the Nuxt dev server on localhost:3000.
	Dev bool `json:"dev"`

	// GeneratedAdminToken is true when AdminToken was created at startup.
	GeneratedAdminToken bool `json:"-"`
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
func Load(path string) (*Config, error) {
	cfg := Defaults()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read config: %w", err)
		}
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		if err := dec.Decode(cfg); err != nil {
			return nil, fmt.Errorf("parse config %s: %w", path, err)
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
	if v := getenv("CONDUCTOR_FILE_VIEW"); v != "" {
		cfg.FileView = FileView(v)
	}
	if v := getenv("CONDUCTOR_DEV"); v == "1" || v == "true" {
		cfg.Dev = true
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

// Validate checks bounds and normalizes paths.
func (c *Config) Validate() error {
	var errs []error
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
	for i, s := range c.ICEServers {
		if len(s.URLs) == 0 {
			errs = append(errs, fmt.Errorf("iceServers[%d]: urls must not be empty", i))
		}
	}
	return errors.Join(errs...)
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
	return catalog.Load(file)
}
