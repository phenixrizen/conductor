// Package catalog defines the launchable agent commands. Commands are argv
// arrays; nothing in the catalog is ever passed through a shell.
package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
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
	// EnvPassthrough names server environment variables this agent's
	// sessions may inherit, in addition to the server-wide envPassthrough.
	EnvPassthrough []string `json:"envPassthrough,omitempty"`
	// Adapter names the hook adapter for this agent; empty means none.
	Adapter string `json:"adapter,omitempty"`
	// Signal says how the agent reports that it needs input; nil means the
	// bell default (see EffectiveSignal).
	Signal *Signal `json:"signal,omitempty"`
}

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

// Catalog is an ordered, validated set of agents keyed by ID.
type Catalog struct {
	agents map[string]Agent
	order  []string
}

var idPattern = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// envNamePattern matches the environment variable names an agent may list in
// envPassthrough.
var envNamePattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

const (
	maxSignalPattern  = 200 // bytes
	maxEnvPassthrough = 32  // entries per agent
)

// ReadFile parses a catalog file, rejecting unknown fields.
func ReadFile(path string) (File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return File{}, fmt.Errorf("read catalog: %w", err)
	}
	var f File
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return File{}, fmt.Errorf("parse catalog %s: %w", path, err)
	}
	return f, nil
}

// Default returns the built-in catalog.
func Default() Catalog {
	c := Catalog{agents: map[string]Agent{}}
	for _, a := range defaults() {
		c.add(a)
	}
	return c
}

// Load merges f over the defaults (unless disabled) and validates every entry.
func Load(f File) (Catalog, error) {
	c := Catalog{agents: map[string]Agent{}}
	if !f.DisableDefaults {
		for _, a := range defaults() {
			c.add(a)
		}
	}
	var errs []error
	for i, a := range f.Agents {
		if err := validate(a); err != nil {
			errs = append(errs, fmt.Errorf("agents[%d]: %w", i, err))
			continue
		}
		c.add(a)
	}
	if len(errs) > 0 {
		return Catalog{}, errors.Join(errs...)
	}
	if len(c.order) == 0 {
		return Catalog{}, errors.New("catalog has no agents")
	}
	return c, nil
}

func (c *Catalog) add(a Agent) {
	if c.agents == nil {
		c.agents = map[string]Agent{}
	}
	if _, exists := c.agents[a.ID]; !exists {
		c.order = append(c.order, a.ID)
	}
	c.agents[a.ID] = a
}

func validate(a Agent) error {
	if !idPattern.MatchString(a.ID) {
		return fmt.Errorf("id %q must match %s", a.ID, idPattern)
	}
	if strings.TrimSpace(a.Name) == "" {
		return fmt.Errorf("agent %s: name must not be empty", a.ID)
	}
	if len(a.Command) == 0 || strings.TrimSpace(a.Command[0]) == "" {
		return fmt.Errorf("agent %s: command must have at least one element", a.ID)
	}
	for _, arg := range a.Command {
		if strings.ContainsRune(arg, 0) {
			return fmt.Errorf("agent %s: command contains NUL", a.ID)
		}
	}
	for k, v := range a.Env {
		if k == "" || strings.ContainsAny(k, "=\x00") || strings.ContainsRune(v, 0) {
			return fmt.Errorf("agent %s: invalid env entry %q", a.ID, k)
		}
	}
	if a.Signal != nil {
		switch a.Signal.Kind {
		case "hook", "bell", "none":
			if a.Signal.Pattern != "" {
				return fmt.Errorf("agent %s: signal: pattern only applies to kind=pattern", a.ID)
			}
		case "pattern":
			if a.Signal.Pattern == "" || len(a.Signal.Pattern) > maxSignalPattern {
				return fmt.Errorf("agent %s: signal: pattern required, at most %d bytes", a.ID, maxSignalPattern)
			}
			if _, err := regexp.Compile(a.Signal.Pattern); err != nil {
				return fmt.Errorf("agent %s: signal: %w", a.ID, err)
			}
		default:
			return fmt.Errorf("agent %s: signal: unknown kind %q", a.ID, a.Signal.Kind)
		}
	}
	if len(a.EnvPassthrough) > maxEnvPassthrough {
		return fmt.Errorf("agent %s: too many envPassthrough entries (at most %d)", a.ID, maxEnvPassthrough)
	}
	for _, name := range a.EnvPassthrough {
		if !envNamePattern.MatchString(name) {
			return fmt.Errorf("agent %s: invalid envPassthrough entry %q", a.ID, name)
		}
	}
	return nil
}

// Upsert validates a, then replaces the agent with the same ID in place (so it
// keeps its position) or appends a as a new one.
func (c *Catalog) Upsert(a Agent) error {
	if err := validate(a); err != nil {
		return err
	}
	c.add(a)
	return nil
}

// Hide removes the agent with the given ID and reports whether it was there.
func (c *Catalog) Hide(id string) bool {
	if _, ok := c.agents[id]; !ok {
		return false
	}
	delete(c.agents, id)
	c.order = slices.DeleteFunc(c.order, func(other string) bool { return other == id })
	return true
}

// ApplyOverlay upserts every agent in o, then hides every ID in o.Hidden, so
// hiding wins over an agent with the same ID. Hiding an ID that is not in the
// catalog is not an error. If any agent is invalid the catalog is unchanged.
func (c *Catalog) ApplyOverlay(o Overlay) error {
	next := c.Clone()
	for i, a := range o.Agents {
		if err := next.Upsert(a); err != nil {
			return fmt.Errorf("agents[%d]: %w", i, err)
		}
	}
	for _, id := range o.Hidden {
		next.Hide(id)
	}
	*c = next
	return nil
}

// Clone returns a deep copy: no map, slice or signal is shared with c. Upsert,
// Hide and ApplyOverlay change a catalog in place, so a catalog that other
// goroutines read should be cloned, changed and swapped in, not changed itself.
func (c *Catalog) Clone() Catalog {
	out := Catalog{
		agents: make(map[string]Agent, len(c.agents)),
		order:  slices.Clone(c.order),
	}
	for id, a := range c.agents {
		out.agents[id] = a.clone()
	}
	return out
}

// clone returns a copy of a that shares no slice, map or pointer with it.
func (a Agent) clone() Agent {
	a.Command = slices.Clone(a.Command)
	a.Env = maps.Clone(a.Env)
	a.EnvPassthrough = slices.Clone(a.EnvPassthrough)
	if a.Signal != nil {
		s := *a.Signal
		a.Signal = &s
	}
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
		cp.Env[k] = "***"
	}
	return cp
}

// EffectiveSignal returns the agent's signal, or the bell default when it has
// none.
func (a Agent) EffectiveSignal() Signal {
	if a.Signal == nil {
		return Signal{Kind: "bell"}
	}
	return *a.Signal
}
