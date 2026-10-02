// Package catalog defines the launchable agent commands. Commands are argv
// arrays; nothing in the catalog is ever passed through a shell.
package catalog

import (
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/phenixrizen/conductor/internal/store"
)

// Agent describes one launchable command.
type Agent struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Command     []string          `json:"command"`
	AllowArgs   bool              `json:"allowArgs"`
	Env         map[string]string `json:"env,omitempty"`
	Cwd         string            `json:"cwd,omitempty"`
	Icon        string            `json:"icon,omitempty"`
	// Site is the agent's website, which the Agents page links to beside an
	// agent that is not installed on the server: an https URL, or empty.
	Site string `json:"site,omitempty"`
	// EnvPassthrough names server environment variables this agent's
	// sessions may inherit, in addition to the server-wide envPassthrough.
	EnvPassthrough []string `json:"envPassthrough,omitempty"`
	// Adapter names the hook adapter for this agent; empty means none.
	Adapter string `json:"adapter,omitempty"`
	// Signal says how the agent reports that it needs input; nil means the
	// bell default (see EffectiveSignal).
	Signal *Signal `json:"signal,omitempty"`
	// Yolo is the agent's yolo recipe: what a launch with yolo on adds to
	// skip the agent's permission prompts. nil in a saved override takes the
	// replaced agent's; an empty recipe ({}) has none, so yolo does nothing
	// for the agent.
	Yolo *Yolo `json:"yolo,omitempty"`
	// TrustPrompt matches the agent's workspace-trust question in the text of
	// its screen (escape sequences read as spaces): while it shows, a crew
	// run types no prompt and the session needs a person's answer. RE2, at
	// most 200 bytes, never matching an empty text; empty in a saved
	// override takes the replaced agent's.
	TrustPrompt string `json:"trustPrompt,omitempty"`
}

// Yolo is an agent's yolo recipe: Args go after the agent's command and the
// launch's own arguments, Env over the agent's own environment, through the
// same filtered environment (never as Conductor's own variables).
type Yolo struct {
	Args []string          `json:"args,omitempty"`
	Env  map[string]string `json:"env,omitempty"`
}

// Empty reports whether y adds nothing to a launch: nil, or no arguments and
// no environment.
func (y *Yolo) Empty() bool { return y == nil || (len(y.Args) == 0 && len(y.Env) == 0) }

// clone returns a copy of y that shares nothing with it.
func (y *Yolo) clone() *Yolo {
	if y == nil {
		return nil
	}
	return &Yolo{Args: slices.Clone(y.Args), Env: maps.Clone(y.Env)}
}

// Signal kinds (Signal.Kind).
const (
	SignalHook    = "hook"    // the agent's hooks report through its adapter
	SignalBell    = "bell"    // a terminal bell or an OSC notification; the default
	SignalPattern = "pattern" // Pattern matches the last line of the screen
	SignalNone    = "none"    // never flagged
)

// Signal describes how an agent reports that it needs attention.
type Signal struct {
	Kind    string `json:"kind"`              // hook | bell | pattern | none
	Pattern string `json:"pattern,omitempty"` // RE2 on the last screen line, kind=pattern only
	// ToolEvents asks hook adapters to report tool use as events (chatty; off by default).
	ToolEvents bool `json:"toolEvents,omitempty"`
}

// File is the JSON shape operators write.
type File struct {
	DisableDefaults bool    `json:"disableDefaults"`
	Agents          []Agent `json:"agents"`
}

// Overlay is the UI-managed layer over the configured catalog: agents to add
// or replace by ID, and IDs to hide.
type Overlay struct {
	Agents []Agent  `json:"agents"`
	Hidden []string `json:"hidden,omitempty"`
}

// Source says where the catalog took an agent from.
type Source string

const (
	SourceBuiltIn Source = "built-in" // defaults.go
	SourceConfig  Source = "config"   // the catalog of the config file, or the catalog file
	SourceSaved   Source = "saved"    // the overlay the Agents page saves (catalog.json)
)

// Catalog is an ordered, validated set of agents keyed by ID.
type Catalog struct {
	agents  map[string]Agent
	sources map[string]Source
	order   []string
}

var idPattern = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// ValidID reports whether id has the shape every agent ID has, for packages
// that keep agent IDs of their own, as crews do.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// envNamePattern matches the environment variable names an agent may list in
// envPassthrough. Lower case is allowed: proxy variables such as http_proxy,
// https_proxy and no_proxy are lower case by convention.
var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// iconPattern is what an icon name looks like: an Iconify name in a class
// (i-lucide-sparkles), at most 64 bytes.
var iconPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9:-]{0,63}$`)

// Size limits enforced by validate, so that a saved agent stays small enough
// for catalog.json and the Agents page.
const (
	maxName           = 60   // characters (runes)
	maxDescription    = 200  // characters (runes)
	maxCommandArgs    = 32   // elements in command
	maxCommandArg     = 4096 // bytes in one command element
	maxEnvKeys        = 32   // entries in env
	maxEnvPassthrough = 32   // entries in envPassthrough
	maxSignalPattern  = 200  // bytes in a signal pattern or a trust prompt
	maxSite           = 200  // bytes in site
	maxYoloArgs       = 16   // elements in yolo.args
	maxYoloEnv        = 16   // entries in yolo.env
	maxYoloValue      = 4096 // bytes in a yolo.env value
)

const (
	maxCwd      = 4096     // bytes in cwd
	maxEnvKey   = 128      // bytes in an env key or an envPassthrough name
	maxEnvValue = 16 << 10 // bytes in an env value
)

// ReadFile parses a catalog file: one JSON value, no unknown field, nothing
// after it (store.DecodeStrict).
func ReadFile(path string) (File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return File{}, fmt.Errorf("read catalog: %w", err)
	}
	var f File
	if err := store.DecodeStrict(b, &f); err != nil {
		return File{}, fmt.Errorf("parse catalog %s: %w", path, err)
	}
	return f, nil
}

// Default returns the built-in catalog.
func Default() Catalog {
	c := Catalog{agents: map[string]Agent{}}
	for _, a := range defaults() {
		c.add(a, SourceBuiltIn)
	}
	return c
}

// Load merges f over the defaults (unless disabled) and validates every entry.
func Load(f File) (Catalog, error) {
	c := Catalog{agents: map[string]Agent{}}
	if !f.DisableDefaults {
		for _, a := range defaults() {
			c.add(a, SourceBuiltIn)
		}
	}
	var errs []error
	for i, a := range f.Agents {
		if err := validate(a); err != nil {
			errs = append(errs, fmt.Errorf("agents[%d]: %w", i, err))
			continue
		}
		c.add(a, SourceConfig)
	}
	if len(errs) > 0 {
		return Catalog{}, errors.Join(errs...)
	}
	if len(c.order) == 0 {
		return Catalog{}, errors.New("catalog has no agents")
	}
	return c, nil
}

func (c *Catalog) add(a Agent, src Source) {
	if c.agents == nil {
		c.agents = map[string]Agent{}
	}
	if c.sources == nil {
		c.sources = map[string]Source{}
	}
	if _, exists := c.agents[a.ID]; !exists {
		c.order = append(c.order, a.ID)
	}
	c.agents[a.ID] = a
	c.sources[a.ID] = src
}

// validate checks one agent against every rule. Load and Upsert both run it,
// and ApplyOverlay goes through Upsert.
func validate(a Agent) error {
	if !idPattern.MatchString(a.ID) {
		return fmt.Errorf("id %q must match %s", a.ID, idPattern)
	}
	if strings.TrimSpace(a.Name) == "" {
		return fmt.Errorf("agent %s: name must not be empty", a.ID)
	}
	if utf8.RuneCountInString(a.Name) > maxName {
		return fmt.Errorf("agent %s: name must be at most %d characters", a.ID, maxName)
	}
	if utf8.RuneCountInString(a.Description) > maxDescription {
		return fmt.Errorf("agent %s: description must be at most %d characters", a.ID, maxDescription)
	}
	if len(a.Command) == 0 || strings.TrimSpace(a.Command[0]) == "" {
		return fmt.Errorf("agent %s: command must have at least one element", a.ID)
	}
	if len(a.Command) > maxCommandArgs {
		return fmt.Errorf("agent %s: command has too many elements (at most %d)", a.ID, maxCommandArgs)
	}
	for i, arg := range a.Command {
		if strings.ContainsRune(arg, 0) {
			return fmt.Errorf("agent %s: command contains NUL", a.ID)
		}
		if len(arg) > maxCommandArg {
			return fmt.Errorf("agent %s: command[%d] is longer than %d bytes", a.ID, i, maxCommandArg)
		}
	}
	if len(a.Cwd) > maxCwd || strings.ContainsRune(a.Cwd, 0) {
		return fmt.Errorf("agent %s: cwd must be at most %d bytes, without NUL", a.ID, maxCwd)
	}
	if a.Icon != "" && !iconPattern.MatchString(a.Icon) {
		return fmt.Errorf("agent %s: icon must match %s", a.ID, iconPattern)
	}
	if a.Site != "" {
		if err := validateSite(a.Site); err != nil {
			return fmt.Errorf("agent %s: site: %w", a.ID, err)
		}
	}
	// The adapter's shape; the registry of adapters is checked where it is
	// known (agents.CheckAdapter), for the config and for saved agents alike.
	if a.Adapter != "" && !idPattern.MatchString(a.Adapter) {
		return fmt.Errorf("agent %s: adapter must match %s", a.ID, idPattern)
	}
	if len(a.Env) > maxEnvKeys {
		return fmt.Errorf("agent %s: too many env entries (at most %d)", a.ID, maxEnvKeys)
	}
	for k, v := range a.Env {
		if k == "" || strings.ContainsAny(k, "=\x00") || strings.ContainsRune(v, 0) {
			return fmt.Errorf("agent %s: invalid env entry %q", a.ID, cut(k))
		}
		if len(k) > maxEnvKey {
			return fmt.Errorf("agent %s: env key %q… is longer than %d bytes", a.ID, cut(k), maxEnvKey)
		}
		if len(v) > maxEnvValue {
			return fmt.Errorf("agent %s: env %s: the value is %d bytes, more than %d", a.ID, k, len(v), maxEnvValue)
		}
	}
	if a.Signal != nil {
		if err := validateSignal(*a.Signal); err != nil {
			return fmt.Errorf("agent %s: signal: %w", a.ID, err)
		}
	}
	if a.Yolo != nil {
		if err := validateYolo(*a.Yolo); err != nil {
			return fmt.Errorf("agent %s: yolo: %w", a.ID, err)
		}
	}
	if a.TrustPrompt != "" {
		if _, err := CompilePattern(a.TrustPrompt); err != nil {
			return fmt.Errorf("agent %s: trustPrompt: %w", a.ID, err)
		}
	}
	if len(a.EnvPassthrough) > maxEnvPassthrough {
		return fmt.Errorf("agent %s: too many envPassthrough entries (at most %d)", a.ID, maxEnvPassthrough)
	}
	for _, name := range a.EnvPassthrough {
		if len(name) > maxEnvKey {
			return fmt.Errorf("agent %s: envPassthrough name %q… is longer than %d bytes", a.ID, cut(name), maxEnvKey)
		}
		if !envNamePattern.MatchString(name) {
			return fmt.Errorf("agent %s: invalid envPassthrough entry %q", a.ID, name)
		}
	}
	return nil
}

// validateYolo bounds a yolo recipe like the command and the environment it
// adds to: at most 16 arguments of at most 4096 bytes without NUL, at most 16
// variables, each named like an envPassthrough entry, never CONDUCTOR_*,
// whose values have at most 4096 bytes without NUL. Its errors carry no agent
// id; validate adds it.
func validateYolo(y Yolo) error {
	if len(y.Args) > maxYoloArgs {
		return fmt.Errorf("too many args (at most %d)", maxYoloArgs)
	}
	for i, arg := range y.Args {
		if arg == "" || strings.ContainsRune(arg, 0) || len(arg) > maxCommandArg {
			return fmt.Errorf("args[%d] must be 1 to %d bytes without NUL", i, maxCommandArg)
		}
	}
	if len(y.Env) > maxYoloEnv {
		return fmt.Errorf("too many env entries (at most %d)", maxYoloEnv)
	}
	for k, v := range y.Env {
		if len(k) > maxEnvKey || !envNamePattern.MatchString(k) || strings.HasPrefix(k, "CONDUCTOR_") {
			return fmt.Errorf("invalid env name %q", cut(k))
		}
		if strings.ContainsRune(v, 0) || len(v) > maxYoloValue {
			return fmt.Errorf("env %s: the value must be at most %d bytes without NUL", k, maxYoloValue)
		}
	}
	return nil
}

// cut keeps the first 40 bytes of s for an error message, so an error never
// sends a long value back whole.
func cut(s string) string {
	if len(s) > 40 {
		return s[:40]
	}
	return s
}

// validateSite accepts an https URL with a host name, a port in range if any,
// no user info and no white space (a no-break space included), at most maxSite
// bytes, so that the Agents page can link to it as it is and a browser parses
// it as the server does.
func validateSite(site string) error {
	if len(site) > maxSite {
		return fmt.Errorf("must be at most %d bytes", maxSite)
	}
	u, err := url.Parse(site)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || strings.ContainsFunc(site, unicode.IsSpace) || !validPort(u.Port()) {
		return errors.New("must be an https:// URL with a host")
	}
	return nil
}

// validPort reports whether port, as url.URL.Port gives it, is absent or a
// TCP port from 1 to 65535.
func validPort(port string) bool {
	if port == "" {
		return true
	}
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535
}

// validateSignal checks a signal's kind and pattern. Its errors carry no agent
// id; validate adds it.
func validateSignal(s Signal) error {
	switch s.Kind {
	case SignalHook, SignalBell, SignalNone:
		if s.Pattern != "" {
			return errors.New("pattern only applies to kind=pattern")
		}
	case SignalPattern:
		if _, err := CompilePattern(s.Pattern); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown kind %q", s.Kind)
	}
	return nil
}

// CompilePattern compiles a screen pattern: the pattern of a signal of kind
// "pattern" and the value of `conductor host --signal-pattern`. It is RE2, so
// matching the last line of a terminal takes linear time whatever the pattern,
// it is at most 200 bytes, and it does not match an empty line: one that did
// would match a screen that shows nothing and mark every quiet session as
// waiting.
func CompilePattern(pattern string) (*regexp.Regexp, error) {
	if pattern == "" || len(pattern) > maxSignalPattern {
		return nil, fmt.Errorf("pattern required, at most %d bytes", maxSignalPattern)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	if re.MatchString("") {
		return nil, errors.New("pattern must not match an empty line")
	}
	return re, nil
}

// Upsert validates a, then replaces the agent with the same ID in place (so it
// keeps its position) or appends a copy of a as a new one, from the overlay
// (SourceSaved). a is not kept: changing it later changes nothing here.
func (c *Catalog) Upsert(a Agent) error {
	if err := validate(a); err != nil {
		return err
	}
	c.add(a.clone(), SourceSaved)
	return nil
}

// Hide removes the agent with the given ID and reports whether it was there.
func (c *Catalog) Hide(id string) bool {
	if _, ok := c.agents[id]; !ok {
		return false
	}
	delete(c.agents, id)
	delete(c.sources, id)
	c.order = slices.DeleteFunc(c.order, func(other string) bool { return other == id })
	return true
}

// ApplyOverlay upserts every agent in o over the agent it replaces, with what
// it leaves out taken from that one (inherit), then hides every ID in
// o.Hidden, so hiding wins over an agent with the same ID. Hiding an ID that
// is not in the catalog is not an error. If any agent is invalid the catalog
// is unchanged.
func (c *Catalog) ApplyOverlay(o Overlay) error {
	next := c.Clone()
	for i, a := range o.Agents {
		prev, had := next.Get(a.ID)
		if err := next.Upsert(inherit(a, prev, had)); err != nil {
			return fmt.Errorf("agents[%d]: %w", i, err)
		}
	}
	for _, id := range o.Hidden {
		next.Hide(id)
	}
	*c = next
	return nil
}

// inherit returns a, an agent saved over prev (had says there was one), with
// what it leaves to prev filled in: an adapter, a signal, a site, a yolo recipe or a trust prompt it omits,
// and every env value it holds as RedactedValue. The Agents page stores the
// mask for a key whose value the editor did not change, so a value changed in
// the config reaches the agent. A value equal to prev's counts as the mask: it
// is prev's value, and the server stores the mask for it at start and on save
// (internal/api), which is how an override saved in full by an earlier version
// comes to follow the config. A masked key that prev does not have, and every
// masked value when there is no prev, is dropped. a itself is not changed.
func inherit(a, prev Agent, had bool) Agent {
	a = a.clone()
	if had {
		if a.Adapter == "" {
			a.Adapter = prev.Adapter
		}
		if a.Signal == nil && prev.Signal != nil {
			s := *prev.Signal
			a.Signal = &s
		}
		if a.Site == "" {
			a.Site = prev.Site
		}
		if a.Yolo == nil {
			a.Yolo = prev.Yolo.clone()
		}
		if a.TrustPrompt == "" {
			a.TrustPrompt = prev.TrustPrompt
		}
	}
	for k, v := range a.Env {
		pv, inPrev := prev.Env[k]
		inPrev = had && inPrev
		if v != RedactedValue && (!inPrev || v != pv) {
			continue // a value of its own
		}
		if inPrev {
			a.Env[k] = pv
		} else {
			delete(a.Env, k)
		}
	}
	return a
}

// Clone returns a deep copy: no map, slice or signal is shared with c. Upsert,
// Hide and ApplyOverlay change a catalog in place, so a catalog that other
// goroutines read should be cloned, changed and swapped in, not changed itself.
func (c Catalog) Clone() Catalog {
	out := Catalog{
		agents:  make(map[string]Agent, len(c.agents)),
		sources: maps.Clone(c.sources),
		order:   slices.Clone(c.order),
	}
	for id, a := range c.agents {
		out.agents[id] = a.clone()
	}
	return out
}

// Source says where the agent with the given ID comes from: built in, the
// config, or saved from the Agents page; "" when the catalog has no such agent.
func (c Catalog) Source(id string) Source { return c.sources[id] }

// clone returns a copy of a that shares no slice, map or pointer with it.
func (a Agent) clone() Agent {
	a.Command = slices.Clone(a.Command)
	a.Env = maps.Clone(a.Env)
	a.EnvPassthrough = slices.Clone(a.EnvPassthrough)
	if a.Signal != nil {
		s := *a.Signal
		a.Signal = &s
	}
	a.Yolo = a.Yolo.clone()
	return a
}

// Get returns the agent with the given ID.
func (c Catalog) Get(id string) (Agent, bool) {
	a, ok := c.agents[id]
	return a, ok
}

// List returns agents in definition order.
func (c Catalog) List() []Agent {
	out := make([]Agent, 0, len(c.order))
	for _, id := range c.order {
		out = append(out, c.agents[id])
	}
	return out
}

// RedactedValue replaces every environment value in the output of Redacted.
// The API treats it as "unchanged" when an agent comes back on save with a value
// still set to it.
const RedactedValue = "***"

// Redacted returns a copy with environment values hidden, suitable for API
// output. Everything else, including Adapter, Signal and EnvPassthrough, is
// kept: none of it is secret.
func (a Agent) Redacted() Agent {
	if len(a.Env) == 0 {
		return a
	}
	cp := a
	cp.Env = make(map[string]string, len(a.Env))
	for k := range a.Env {
		cp.Env[k] = RedactedValue
	}
	return cp
}

// EffectiveSignal returns the agent's signal, or the bell default when it has
// none.
func (a Agent) EffectiveSignal() Signal {
	if a.Signal == nil {
		return Signal{Kind: SignalBell}
	}
	return *a.Signal
}
