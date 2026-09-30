// Package crew holds saved crews: named teams of agents, each member with a
// role prompt, extra arguments and a start condition, that a run launches
// together. Crews persist as crews.json in the data directory.
package crew

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/phenixrizen/conductor/internal/catalog"
)

// When a member starts in a run (Start.When).
const (
	StartImmediately = "immediately"
	StartAfter       = "after" // once the member Start.Member names first reports done
	StartManual      = "manual"
)

// Where a crew's members run (Crew.Where).
const (
	WhereServer = "server"
	WhereHost   = "host"
)

// How members share the working directory (Crew.Isolation).
const (
	IsolationNone     = "none"     // every member works in Cwd
	IsolationWorktree = "worktree" // every member gets a git worktree of Cwd
)

// Start says when a member starts in a run.
type Start struct {
	When   string `json:"when"`             // immediately | after | manual
	Member string `json:"member,omitempty"` // with when=after: the member to wait for
}

// Member is one agent of a crew.
type Member struct {
	// Name identifies the member in its crew. It becomes a branch name and a
	// worktree path, so it is held to memberNamePattern.
	Name    string   `json:"name"`
	AgentID string   `json:"agentId"`
	Prompt  string   `json:"prompt"` // typed once the member is ready; see ExpandPrompt
	Args    []string `json:"args,omitempty"`
	Start   Start    `json:"start"`
}

// Crew is a saved team of agents. The server sets ID, CreatedAt and UpdatedAt.
type Crew struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	Goal               string    `json:"goal"`
	Cwd                string    `json:"cwd"`
	Where              string    `json:"where"`     // server | host
	Isolation          string    `json:"isolation"` // none | worktree
	OpenAfterLaunch    bool      `json:"openAfterLaunch"`
	ViewLinkTTLSeconds int64     `json:"viewLinkTtlSeconds,omitempty"`
	Members            []Member  `json:"members"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

// Limits enforced by Validate. The store adds one more: at most maxCrews crews.
const (
	maxName    = 60   // characters (runes) in a crew name
	maxGoal    = 2000 // characters (runes)
	maxCwd     = 4096 // bytes
	maxMembers = 12
	maxPrompt  = 4000 // characters (runes)
	maxArgs    = 32   // entries in a member's args
	maxArg     = 4096 // bytes in one entry
	// maxLinkTTL bounds ViewLinkTTLSeconds as POST /api/sessions/{id}/links
	// bounds ttlSeconds.
	maxLinkTTL = 365 * 24 * 3600
)

// memberNamePattern keeps member names short, lower case and free of path
// separators: they become branch names and worktree paths.
var memberNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,39}$`)

// Validate checks c apart from its ID, which the store checks, and its agents,
// which CheckAgents checks against a catalog: the name, goal, working
// directory and options, and the members' names, prompts, arguments and start
// conditions, all within their limits. The first problem found is returned.
func (c Crew) Validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return errors.New("name must not be empty")
	}
	if utf8.RuneCountInString(c.Name) > maxName {
		return fmt.Errorf("name must be at most %d characters", maxName)
	}
	if utf8.RuneCountInString(c.Goal) > maxGoal {
		return fmt.Errorf("goal must be at most %d characters", maxGoal)
	}
	if len(c.Cwd) > maxCwd {
		return fmt.Errorf("cwd must be at most %d bytes", maxCwd)
	}
	if strings.ContainsRune(c.Cwd, 0) {
		return errors.New("cwd contains NUL")
	}
	if c.Where != WhereServer && c.Where != WhereHost {
		return fmt.Errorf("where %q must be %q or %q", c.Where, WhereServer, WhereHost)
	}
	if c.Isolation != IsolationNone && c.Isolation != IsolationWorktree {
		return fmt.Errorf("isolation %q must be %q or %q", c.Isolation, IsolationNone, IsolationWorktree)
	}
	if c.ViewLinkTTLSeconds < 0 || c.ViewLinkTTLSeconds > maxLinkTTL {
		return fmt.Errorf("viewLinkTtlSeconds must be between 0 and %d", maxLinkTTL)
	}
	if len(c.Members) > maxMembers {
		return fmt.Errorf("too many members (at most %d)", maxMembers)
	}
	names := make([]string, 0, len(c.Members))
	for _, m := range c.Members {
		if err := m.validate(); err != nil {
			return err
		}
		if slices.Contains(names, m.Name) {
			return fmt.Errorf("member %q: the name is used twice", m.Name)
		}
		names = append(names, m.Name)
	}
	for _, m := range c.Members {
		if m.Start.When != StartAfter {
			continue
		}
		if m.Start.Member == m.Name {
			return fmt.Errorf("member %q: cannot start after itself", m.Name)
		}
		if !slices.Contains(names, m.Start.Member) {
			return fmt.Errorf("member %q: starts after %q, which is not a member of this crew", m.Name, m.Start.Member)
		}
	}
	return nil
}

// validate checks what m can be checked for alone; Validate checks it against
// the other members.
func (m Member) validate() error {
	if !memberNamePattern.MatchString(m.Name) {
		return fmt.Errorf("member %q: name must match %s", m.Name, memberNamePattern)
	}
	if m.AgentID == "" {
		return fmt.Errorf("member %q: agentId must not be empty", m.Name)
	}
	if utf8.RuneCountInString(m.Prompt) > maxPrompt {
		return fmt.Errorf("member %q: prompt must be at most %d characters", m.Name, maxPrompt)
	}
	if len(m.Args) > maxArgs {
		return fmt.Errorf("member %q: too many args (at most %d)", m.Name, maxArgs)
	}
	for i, a := range m.Args {
		if strings.ContainsRune(a, 0) {
			return fmt.Errorf("member %q: args[%d] contains NUL", m.Name, i)
		}
		if len(a) > maxArg {
			return fmt.Errorf("member %q: args[%d] is longer than %d bytes", m.Name, i, maxArg)
		}
	}
	switch m.Start.When {
	case StartAfter:
		if m.Start.Member == "" {
			return fmt.Errorf("member %q: start.member must name the member to start after", m.Name)
		}
	case StartImmediately, StartManual:
		if m.Start.Member != "" {
			return fmt.Errorf("member %q: start.member is only set with start.when %q", m.Name, StartAfter)
		}
	default:
		return fmt.Errorf("member %q: start.when %q must be %q, %q or %q", m.Name, m.Start.When, StartImmediately, StartAfter, StartManual)
	}
	return nil
}

// CheckAgents reports the first member whose agent cat does not list. It is
// apart from Validate because the catalog changes: the API checks it when a
// crew is saved or duplicated, and a launch checks it again.
func (c Crew) CheckAgents(cat catalog.Catalog) error {
	for _, m := range c.Members {
		if _, ok := cat.Get(m.AgentID); !ok {
			return fmt.Errorf("member %q: unknown agent %q", m.Name, m.AgentID)
		}
	}
	return nil
}

// goalRef matches $GOAL and ${GOAL}. $GOAL must end where a shell variable
// name would, so $GOALS and $GOAL_2 are left alone.
var goalRef = regexp.MustCompile(`\$(?:\{GOAL\}|GOAL\b)`)

// ExpandPrompt replaces $GOAL and ${GOAL} in a role prompt with the crew's
// goal. The goal goes in literally: nothing in it is expanded.
func ExpandPrompt(prompt, goal string) string {
	return goalRef.ReplaceAllLiteralString(prompt, goal)
}

// clone returns a copy of c that shares no slice with it.
func (c Crew) clone() Crew {
	c.Members = slices.Clone(c.Members)
	for i := range c.Members {
		c.Members[i].Args = slices.Clone(c.Members[i].Args)
	}
	return c
}
