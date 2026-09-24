// Package catalog defines the launchable agent commands. Commands are argv
// arrays; nothing in the catalog is ever passed through a shell.
package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
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
}

// File is the JSON shape operators write.
type File struct {
	DisableDefaults bool    `json:"disableDefaults"`
	Agents          []Agent `json:"agents"`
}

// Catalog is an ordered, validated set of agents keyed by ID.
type Catalog struct {
	agents map[string]Agent
	order  []string
}

var idPattern = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

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
	return nil
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

// Redacted returns a copy with environment values hidden, suitable for API output.
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
