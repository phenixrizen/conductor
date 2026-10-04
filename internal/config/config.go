// Package config loads the server configuration from an optional JSON file,
// applies CONDUCTOR_* environment overrides, fills defaults and validates.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"net/url"
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

// Reach says how the server finds out, and makes it so, that it can be
// reached from outside its network (internal/reach).
type Reach struct {
	// Mode is auto (the default: the public address by STUN and the TLS
	// port mapped on the gateway through UPnP, PCP or NAT-PMP), manual (the
	// address by STUN only; the person forwarded PublicPort) or off.
	Mode string `json:"mode"`
	// PublicPort is the port on the public address that reaches the TLS
	// listener: mapped in auto, forwarded by the person in manual. 443 by
	// default.
	PublicPort int `json:"publicPort"`
	// PublicPort80 also maps port 80 to the plain listener, for an ACME
	// http-01 challenge.
	PublicPort80 bool `json:"publicPort80"`
	// STUNServer answers the public address: a stun: URL or host:port. The
	// first stun: URL of iceServers when empty.
	STUNServer string `json:"stunServer"`
}

// TLS configures a second listener that serves the same workbench over TLS,
// with a certificate from Let's Encrypt through ACME (internal/certs) or
// from files.
type TLS struct {
	// Listen is the TLS listener's address; ":8443" when ACME or files are
	// set and it is empty. The public port (reach.publicPort, 443) is mapped
	// to it.
	Listen string `json:"listen"`
	// CertFile and KeyFile serve a certificate of the person's own (PEM),
	// re-read when they change. Exclusive with ACME.
	CertFile string `json:"certFile"`
	KeyFile  string `json:"keyFile"`
	// ACME obtains the certificate from a CA.
	ACME *ACME `json:"acme"`
}

// ACME says what certificate to obtain and how to prove the identifiers.
type ACME struct {
	// Email is the account's contact (optional, recommended).
	Email string `json:"email"`
	// Domains are the identifiers: DNS names or literal IP addresses. Empty
	// means the public address reach finds (an IP-address certificate).
	Domains []string `json:"domains"`
	// Challenge is tls-alpn-01 (the default, on the TLS listener through the
	// mapped 443), http-01 (on the plain listener through a mapped 80) or
	// dns-01 (through DNSProvider; DNS names only).
	Challenge string `json:"challenge"`
	// DNSProvider is cloudflare, exec or httpreq; DNSEnv holds its settings
	// under lego's variable names (CLOUDFLARE_DNS_API_TOKEN, EXEC_PATH,
	// HTTPREQ_ENDPOINT, …), never logged or shown.
	DNSProvider string            `json:"dnsProvider"`
	DNSEnv      map[string]string `json:"dnsEnv"`
	// CADirectory is the ACME directory URL; Let's Encrypt's when empty.
	CADirectory string `json:"caDirectory"`
	// Profile is the ACME profile asked for; "shortlived" for IP addresses
	// (Let's Encrypt issues those as six-day certificates only), else the
	// CA's default.
	Profile string `json:"profile"`
}

// ICE is how published sessions gather their WebRTC candidates.
type ICE struct {
	UDPPort  int    `json:"udpPort"`
	PublicIP string `json:"publicIp"`
}

// Switchyard is Conductor as a coordinator alone: it takes hosted sessions
// from other Conductors and conductor host, brokers their signaling and,
// when Relay is on, relays the terminal for the pairs ICE cannot connect;
// it launches nothing (no sessions, crews, runs or catalog of its own).
type Switchyard struct {
	Enabled bool `json:"enabled"`
	// Relay serves viewers through this server when WebRTC fails. On by
	// default; off, such a viewer gets relay_off and a relayOnly host is
	// refused.
	Relay *bool `json:"relay,omitempty"`
	// RelayKBps bounds what one host connection may send through the relay,
	// in kilobytes a second (the host's output to all of its viewers; a
	// burst of twice that is allowed): a public switchyard's protection
	// against a session that streams. 0, the default, is no bound.
	RelayKBps int `json:"relayKBps,omitempty"`
	// AllowedOrigins are the browser origins that may fetch the join route
	// and open a hosted session's WebSocket from another page, as host
	// patterns (host[:port], * wildcards; no scheme): the desktop app's own
	// workbench, served from a loopback address. Loopback with any port by
	// default.
	AllowedOrigins []string `json:"allowedOrigins,omitempty"`
	// OpenHosts admits a host that presents no token at all, under the
	// limits below, so a Conductor publishes here with nothing configured:
	// the public switchyard. Read only while Enabled (conductor switchyard
	// sets that after the file is read, so it is not a validation rule). A host with a wrong token is still refused (a
	// typo must show); one with a host token is trusted and outside the
	// limits.
	OpenHosts bool `json:"openHosts,omitempty"`
	// OpenHostSessions is how many live hosted sessions one address may
	// hold as an open host (4 by default).
	OpenHostSessions int `json:"openHostSessions,omitempty"`
	// OpenHostRegistrationsPerMinute bounds how often one address may
	// register as an open host (6 a minute by default).
	OpenHostRegistrationsPerMinute int `json:"openHostRegistrationsPerMinute,omitempty"`
	// OpenHostRelayKBps bounds an open host's relayed output, like
	// RelayKBps but for open hosts (128 KiB/s by default); 0 falls back to
	// RelayKBps.
	OpenHostRelayKBps int `json:"openHostRelayKBps,omitempty"`
}

// SwitchyardRelay reports whether the switchyard relays (Switchyard.Relay, on by default).
func (c *Config) SwitchyardRelay() bool { return c.Switchyard.Relay == nil || *c.Switchyard.Relay }

// SwitchyardOrigins are the origins a switchyard answers cross-origin: the
// configured ones, else loopback with any port.
func (c *Config) SwitchyardOrigins() []string {
	if len(c.Switchyard.AllowedOrigins) > 0 {
		return c.Switchyard.AllowedOrigins
	}
	return []string{"127.0.0.1:*", "localhost:*"}
}

// Agents holds what agents may do on their own, with their session's token.
type Agents struct {
	// SelfService lets an agent form a crew around its own session, add
	// members to its run, read its run and mint a view link to itself
	// (docs/protocol.md): a scoped grant on the session's agent token, never
	// the workbench token. On by default.
	SelfService *bool `json:"selfService,omitempty"`
	// InstallSkill puts the Conductor skill where an agent reads skills, in
	// the server user's home, when the agent is first launched after the
	// server starts (one attempt per adapter; a SKILL.md of the user's own
	// is left alone). On by default.
	InstallSkill *bool `json:"installSkill,omitempty"`
	// MCP registers Conductor's MCP server (conductor mcp) with agents that
	// take one at launch (Claude Code, Codex), so the skill's reports and
	// crew actions are tools. On by default.
	MCP *bool `json:"mcp,omitempty"`
}

// MCP reports whether launches register the MCP server (Agents.MCP, on by default).
func (c *Config) MCP() bool { return c.Agents.MCP == nil || *c.Agents.MCP }

// InstallSkill reports whether a launch installs the skill (Agents.InstallSkill, on by default).
func (c *Config) InstallSkill() bool { return c.Agents.InstallSkill == nil || *c.Agents.InstallSkill }

// SelfService reports whether agents may serve themselves (Agents.SelfService, on by default).
func (c *Config) SelfService() bool { return c.Agents.SelfService == nil || *c.Agents.SelfService }

// Rendezvous names a public Conductor this server publishes its sessions
// to, through the host protocol, so they can be shared from there when this
// server cannot be reached from outside (carrier-grade NAT, a corporate
// network, WSL2 in its NAT mode).
type Rendezvous struct {
	// Server is the public Conductor's URL (http(s)://host[:port]).
	Server string `json:"server"`
	// Token is one of its host tokens (hostTokens, or its workbench token).
	Token string `json:"token"`
	// HostName labels this server there; the machine's name when empty.
	HostName string `json:"hostName"`
	// RelayOnly serves viewers through the rendezvous's relay only, without
	// WebRTC.
	RelayOnly bool `json:"relayOnly"`
}

// ACME challenges.
const (
	ChallengeTLSALPN = "tls-alpn-01"
	ChallengeHTTP    = "http-01"
	ChallengeDNS     = "dns-01"
)

// Reach modes.
const (
	ReachOff    = "off"
	ReachAuto   = "auto"
	ReachManual = "manual"
)

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
	// PublicURL is the externally reachable base URL used to build share
	// links and the agents' notify URL. While it is unset or names localhost
	// (PublicURLIsLocal), a share link takes the address its request came
	// through instead: the address the workbench was opened at.
	PublicURL string `json:"publicUrl"`
	// WorkbenchToken is the operator's token: it opens the workbench and every
	// management route (the admin principal). Generated when empty.
	WorkbenchToken string `json:"workbenchToken"`
	// AdminToken is the old name of WorkbenchToken, still read from a config
	// file and from CONDUCTOR_ADMIN_TOKEN so nothing breaks: applyEnv moves it
	// over, and WorkbenchTokenRenamed says so, for one warning at start.
	AdminToken string `json:"adminToken"`
	// HostTokens authorize `conductor host` registrations. Empty means the workbench token only.
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
	// Reach: the public address and the port mapping on the gateway.
	Reach Reach `json:"reach"`
	// TLS: the TLS listener and where its certificate comes from.
	TLS TLS `json:"tls"`
	// Rendezvous: a public Conductor to publish sessions to.
	Rendezvous Rendezvous `json:"rendezvous"`
	// Agents: what agents may do on their own.
	Agents Agents `json:"agents"`
	// Switchyard, when enabled, makes this server a coordinator of hosted
	// sessions that launches nothing (conductor switchyard).
	Switchyard Switchyard `json:"switchyard"`
	// ICE is how this server's published sessions (Rendezvous) gather their
	// WebRTC candidates: one UDP port for every connection and the address
	// advertised as this machine's, for a forwarder in front of it (the
	// desktop app on Windows, in front of WSL); zero lets pion gather as it
	// does by default.
	ICE ICE `json:"ice"`

	// GeneratedWorkbenchToken is true when WorkbenchToken was created at startup.
	GeneratedWorkbenchToken bool `json:"-"`
	// WorkbenchTokenRenamed is true when the token came in under its old name
	// (adminToken, CONDUCTOR_ADMIN_TOKEN).
	WorkbenchTokenRenamed bool `json:"-"`
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
		Switchyard:           Switchyard{OpenHostSessions: 4, OpenHostRegistrationsPerMinute: 6, OpenHostRelayKBps: 128},
		ExitedRetention:      Duration(10 * time.Minute),
		ICEServers:           []ICEServer{{URLs: []string{"stun:stun.l.google.com:19302"}}},
		RelayTimeoutMs:       8000,
		FileView:             FileViewView,
		Reach:                Reach{Mode: ReachAuto, PublicPort: 443},
	}
}

// TLSEnabled reports whether there is a TLS listener: ACME or certificate
// files are configured.
func (c *Config) TLSEnabled() bool { return c.TLS.ACME != nil || c.TLS.CertFile != "" }

// ACMEIdentifiersAreIPs reports whether the ACME identifiers are IP
// addresses: the discovered address (no domains), or literal addresses.
func (c *Config) ACMEIdentifiersAreIPs() bool {
	if c.TLS.ACME == nil {
		return false
	}
	if len(c.TLS.ACME.Domains) == 0 {
		return true
	}
	for _, d := range c.TLS.ACME.Domains {
		if _, err := netip.ParseAddr(d); err == nil {
			return true
		}
	}
	return false
}

// STUNServer is the server the reach lookup asks for the public address:
// reach.stunServer, else the first stun: URL of iceServers, else "".
func (c *Config) STUNServer() string {
	if c.Reach.STUNServer != "" {
		return c.Reach.STUNServer
	}
	for _, s := range c.ICEServers {
		for _, u := range s.URLs {
			if strings.HasPrefix(strings.ToLower(u), "stun:") {
				return u
			}
		}
	}
	return ""
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
	str("CONDUCTOR_WORKBENCH_TOKEN", &cfg.WorkbenchToken)
	str("CONDUCTOR_ADMIN_TOKEN", &cfg.AdminToken) // the old name, folded below
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
	str("CONDUCTOR_TLS_LISTEN", &cfg.TLS.Listen)
	str("CONDUCTOR_TLS_CERT_FILE", &cfg.TLS.CertFile)
	str("CONDUCTOR_TLS_KEY_FILE", &cfg.TLS.KeyFile)
	acme := func() *ACME {
		if cfg.TLS.ACME == nil {
			cfg.TLS.ACME = &ACME{}
		}
		return cfg.TLS.ACME
	}
	for key, set := range map[string]func(*ACME, string){
		"CONDUCTOR_TLS_ACME_EMAIL":        func(a *ACME, v string) { a.Email = v },
		"CONDUCTOR_TLS_ACME_CHALLENGE":    func(a *ACME, v string) { a.Challenge = v },
		"CONDUCTOR_TLS_ACME_DNS_PROVIDER": func(a *ACME, v string) { a.DNSProvider = v },
		"CONDUCTOR_TLS_ACME_CA":           func(a *ACME, v string) { a.CADirectory = v },
		"CONDUCTOR_TLS_ACME_PROFILE":      func(a *ACME, v string) { a.Profile = v },
	} {
		if v := getenv(key); v != "" {
			set(acme(), v)
		}
	}
	if v := getenv("CONDUCTOR_TLS_ACME"); v == "1" || v == "true" {
		acme()
	}
	if v := getenv("CONDUCTOR_TLS_ACME_DOMAINS"); v != "" {
		var out []string
		list("CONDUCTOR_TLS_ACME_DOMAINS", &out)
		acme().Domains = out
	}
	if v := getenv("CONDUCTOR_TLS_ACME_DNS_ENV"); v != "" {
		a := acme()
		if a.DNSEnv == nil {
			a.DNSEnv = map[string]string{}
		}
		for _, kv := range strings.Split(v, ",") {
			if k, val, ok := strings.Cut(strings.TrimSpace(kv), "="); ok && k != "" {
				a.DNSEnv[k] = val
			}
		}
	}
	switch getenv("CONDUCTOR_AGENT_SELF_SERVICE") {
	case "1", "true":
		on := true
		cfg.Agents.SelfService = &on
	case "0", "false":
		off := false
		cfg.Agents.SelfService = &off
	}
	if v := getenv("CONDUCTOR_SWITCHYARD"); v == "1" || v == "true" {
		cfg.Switchyard.Enabled = true
	}
	switch getenv("CONDUCTOR_SWITCHYARD_RELAY") {
	case "1", "true":
		on := true
		cfg.Switchyard.Relay = &on
	case "0", "false":
		off := false
		cfg.Switchyard.Relay = &off
	}
	list("CONDUCTOR_SWITCHYARD_ORIGINS", &cfg.Switchyard.AllowedOrigins)
	if err := num("CONDUCTOR_SWITCHYARD_RELAY_KBPS", &cfg.Switchyard.RelayKBps); err != nil {
		return err
	}
	if v := getenv("CONDUCTOR_SWITCHYARD_OPEN_HOSTS"); v == "1" || v == "true" {
		cfg.Switchyard.OpenHosts = true
	}
	if err := num("CONDUCTOR_SWITCHYARD_OPEN_HOST_SESSIONS", &cfg.Switchyard.OpenHostSessions); err != nil {
		return err
	}
	if err := num("CONDUCTOR_SWITCHYARD_OPEN_HOST_REGISTRATIONS", &cfg.Switchyard.OpenHostRegistrationsPerMinute); err != nil {
		return err
	}
	if err := num("CONDUCTOR_SWITCHYARD_OPEN_HOST_RELAY_KBPS", &cfg.Switchyard.OpenHostRelayKBps); err != nil {
		return err
	}
	if err := num("CONDUCTOR_ICE_UDP_PORT", &cfg.ICE.UDPPort); err != nil {
		return err
	}
	str("CONDUCTOR_ICE_PUBLIC_IP", &cfg.ICE.PublicIP)
	switch getenv("CONDUCTOR_AGENT_MCP") {
	case "1", "true":
		on := true
		cfg.Agents.MCP = &on
	case "0", "false":
		off := false
		cfg.Agents.MCP = &off
	}
	switch getenv("CONDUCTOR_AGENT_INSTALL_SKILL") {
	case "1", "true":
		on := true
		cfg.Agents.InstallSkill = &on
	case "0", "false":
		off := false
		cfg.Agents.InstallSkill = &off
	}
	str("CONDUCTOR_RENDEZVOUS_SERVER", &cfg.Rendezvous.Server)
	str("CONDUCTOR_RENDEZVOUS_TOKEN", &cfg.Rendezvous.Token)
	str("CONDUCTOR_RENDEZVOUS_HOST_NAME", &cfg.Rendezvous.HostName)
	if v := getenv("CONDUCTOR_RENDEZVOUS_RELAY_ONLY"); v == "1" || v == "true" {
		cfg.Rendezvous.RelayOnly = true
	}
	str("CONDUCTOR_REACH", &cfg.Reach.Mode)
	str("CONDUCTOR_REACH_STUN", &cfg.Reach.STUNServer)
	if err := num("CONDUCTOR_REACH_PUBLIC_PORT", &cfg.Reach.PublicPort); err != nil {
		return err
	}
	switch getenv("CONDUCTOR_REACH_PUBLIC_PORT_80") {
	case "1", "true":
		cfg.Reach.PublicPort80 = true
	case "0", "false":
		cfg.Reach.PublicPort80 = false
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
	// The old name of the workbench token, from the file or the environment,
	// still works; the new name wins when both are set.
	if cfg.AdminToken != "" {
		if cfg.WorkbenchToken == "" {
			cfg.WorkbenchToken = cfg.AdminToken
			cfg.WorkbenchTokenRenamed = true
		}
		cfg.AdminToken = ""
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
	if u, err := url.Parse(c.PublicURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs = append(errs, errors.New("publicUrl must be an http or https URL with a host"))
	}
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
	if c.ICE.UDPPort < 0 || c.ICE.UDPPort > 65535 {
		errs = append(errs, errors.New("ice.udpPort must be between 0 and 65535"))
	}
	if c.ICE.PublicIP != "" {
		if _, err := netip.ParseAddr(c.ICE.PublicIP); err != nil {
			errs = append(errs, fmt.Errorf("ice.publicIp must be an IP address, got %q", c.ICE.PublicIP))
		}
	}
	if c.Switchyard.RelayKBps < 0 || c.Switchyard.RelayKBps > 1<<20 {
		errs = append(errs, errors.New("switchyard.relayKBps must be between 0 and 1048576"))
	}
	if n := c.Switchyard.OpenHostSessions; n < 1 || n > 1000 {
		errs = append(errs, errors.New("switchyard.openHostSessions must be between 1 and 1000"))
	}
	if n := c.Switchyard.OpenHostRegistrationsPerMinute; n < 1 || n > 600 {
		errs = append(errs, errors.New("switchyard.openHostRegistrationsPerMinute must be between 1 and 600"))
	}
	if n := c.Switchyard.OpenHostRelayKBps; n < 0 || n > 1<<20 {
		errs = append(errs, errors.New("switchyard.openHostRelayKBps must be between 0 and 1048576"))
	}
	for _, o := range c.Switchyard.AllowedOrigins {
		if strings.TrimSpace(o) == "" || strings.Contains(o, "://") {
			errs = append(errs, fmt.Errorf("switchyard.allowedOrigins: %q must be a host pattern such as 127.0.0.1:* (no scheme)", o))
		}
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
	switch c.Reach.Mode {
	case ReachOff, ReachAuto, ReachManual:
	default:
		errs = append(errs, fmt.Errorf("reach.mode must be off, auto or manual, got %q", c.Reach.Mode))
	}
	if c.Reach.PublicPort < 1 || c.Reach.PublicPort > 65535 {
		errs = append(errs, errors.New("reach.publicPort must be between 1 and 65535"))
	}
	if c.Reach.Mode != ReachOff && c.STUNServer() == "" {
		errs = append(errs, errors.New("reach needs a STUN server: reach.stunServer, or a stun: URL in iceServers"))
	}
	errs = append(errs, c.validateTLS()...)
	if r := &c.Rendezvous; r.Server != "" || r.Token != "" {
		r.Server = strings.TrimRight(r.Server, "/")
		if u, err := url.Parse(r.Server); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, errors.New("rendezvous.server must be an http or https URL with a host"))
		}
		if r.Token == "" {
			errs = append(errs, errors.New("rendezvous.token is needed: a host token of the rendezvous"))
		}
		if r.Server == c.PublicURL {
			errs = append(errs, errors.New("rendezvous.server must be another server, not this one's publicUrl"))
		}
	}
	hookErrs, warnings := c.validateWebhooks()
	errs = append(errs, hookErrs...)
	c.warnings = warnings
	return errors.Join(errs...)
}

// validateTLS checks the TLS listener and its certificate source, and fills
// the defaults (listen, challenge, profile) in.
func (c *Config) validateTLS() []error {
	var errs []error
	t := &c.TLS
	if !c.TLSEnabled() {
		if t.Listen != "" || t.KeyFile != "" {
			errs = append(errs, errors.New("tls.listen and tls.keyFile need tls.acme or tls.certFile"))
		}
		return errs
	}
	if t.CertFile != "" && t.ACME != nil {
		errs = append(errs, errors.New("tls.certFile and tls.acme are exclusive: one certificate source"))
	}
	if (t.CertFile == "") != (t.KeyFile == "") {
		errs = append(errs, errors.New("tls.certFile and tls.keyFile go together"))
	}
	if t.Listen == "" {
		t.Listen = ":8443"
	}
	if t.Listen == c.Listen {
		errs = append(errs, errors.New("tls.listen must differ from listen"))
	}
	for _, f := range []*string{&t.CertFile, &t.KeyFile} {
		if *f != "" {
			if abs, err := filepath.Abs(*f); err == nil {
				*f = abs
			}
		}
	}
	a := t.ACME
	if a == nil {
		return errs
	}
	if a.Challenge == "" {
		a.Challenge = ChallengeTLSALPN
	}
	switch a.Challenge {
	case ChallengeTLSALPN, ChallengeHTTP, ChallengeDNS:
	default:
		errs = append(errs, fmt.Errorf("tls.acme.challenge must be tls-alpn-01, http-01 or dns-01, got %q", a.Challenge))
	}
	for i, d := range a.Domains {
		d = strings.TrimSpace(d)
		a.Domains[i] = d
		if d == "" || strings.ContainsAny(d, " /\\") {
			errs = append(errs, fmt.Errorf("tls.acme.domains[%d]: %q is not a DNS name or an IP address", i, d))
		}
	}
	ips := c.ACMEIdentifiersAreIPs()
	if ips && a.Challenge == ChallengeDNS {
		errs = append(errs, errors.New("tls.acme: dns-01 cannot prove an IP address; use tls-alpn-01 or http-01"))
	}
	if ips {
		if a.Profile == "" {
			a.Profile = "shortlived"
		} else if a.Profile != "shortlived" {
			errs = append(errs, errors.New("tls.acme.profile must be shortlived for IP addresses (Let's Encrypt issues six-day certificates for them)"))
		}
	}
	if len(a.Domains) == 0 && c.Reach.Mode == ReachOff {
		errs = append(errs, errors.New("tls.acme without domains needs reach (the certificate is for the public address reach finds); set reach.mode to auto or manual, or name domains"))
	}
	if len(a.Domains) == 0 && c.Reach.PublicPort != 443 && a.Challenge == ChallengeTLSALPN {
		errs = append(errs, errors.New("tls.acme for the public address needs reach.publicPort 443: the CA validates tls-alpn-01 on 443 only"))
	}
	if a.Challenge == ChallengeHTTP && c.Reach.Mode == ReachAuto && !c.Reach.PublicPort80 {
		errs = append(errs, errors.New("tls.acme with http-01 needs reach.publicPort80, so port 80 reaches the plain listener"))
	}
	if a.Challenge == ChallengeDNS {
		switch a.DNSProvider {
		case "cloudflare", "exec", "httpreq":
		case "":
			errs = append(errs, errors.New("tls.acme with dns-01 needs tls.acme.dnsProvider (cloudflare, exec or httpreq)"))
		default:
			errs = append(errs, fmt.Errorf("tls.acme.dnsProvider must be cloudflare, exec or httpreq, got %q", a.DNSProvider))
		}
	}
	if a.CADirectory != "" {
		if u, err := url.Parse(a.CADirectory); err != nil || u.Scheme != "https" || u.Host == "" {
			errs = append(errs, errors.New("tls.acme.caDirectory must be an https URL"))
		}
	}
	return errs
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

// PublicURLIsLocal reports whether PublicURL names this machine alone
// (localhost, 127.0.0.1 or ::1), as the default and the example config do: a
// link built on it would not reach anyone else, so share links take the
// address their request came through instead (internal/api).
func (c *Config) PublicURLIsLocal() bool {
	u, err := url.Parse(c.PublicURL)
	if err != nil {
		return true
	}
	switch strings.ToLower(u.Hostname()) {
	case "", "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}
