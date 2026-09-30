// Package crew holds saved crews: named teams of agents, each member with a
// role prompt, extra arguments and a start condition, that a run launches
// together. Crews persist as crews.json in the data directory.
package crew

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/phenixrizen/conductor/internal/catalog"
	"github.com/phenixrizen/conductor/internal/proto"
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

// ErrInvalid is matched (errors.Is) by every error that says a crew breaks a
// rule: from Validate, CheckAgents and the store's checks. The API answers it
// with 400 invalid_crew.
var ErrInvalid = errors.New("invalid crew")

// invalidError is a rule a crew breaks. Its message is the problem alone.
type invalidError struct{ msg string }

func (e *invalidError) Error() string { return e.msg }
func (e *invalidError) Unwrap() error { return ErrInvalid }

func invalidf(format string, args ...any) error {
	return &invalidError{msg: fmt.Sprintf(format, args...)}
}

// maxQuoted is how many characters of a value an error message quotes.
const maxQuoted = 80

// quote quotes a value for an error message as %q does, cut to maxQuoted
// characters with "…" where it was cut: whatever a client sends, an error
// never sends it back whole.
func quote(s string) string {
	n := 0
	for i := range s {
		if n == maxQuoted {
			return strconv.Quote(s[:i] + "…")
		}
		n++
	}
	return strconv.Quote(s)
}

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
	ViewLinkTTLSeconds int64     `json:"viewLinkTtlSeconds,omitempty"` // a launch creates a view link that lasts this long; none when 0
	Members            []Member  `json:"members"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

// Limits enforced by Validate. The store adds one more: at most maxCrews crews.
const (
	maxName      = 60   // characters (runes) in a crew name
	maxGoal      = 2000 // characters (runes)
	maxCwd       = 4096 // bytes
	maxMembers   = 12
	maxPrompt    = 4000    // characters (runes)
	maxArgs      = 32      // entries in a member's args
	maxArg       = 4096    // bytes in one entry
	maxArgsBytes = 8 << 10 // bytes in all of a member's args
	// maxEncoded bounds the crew as JSON. The limits above allow more, as a
	// character can take six bytes there (\u0001).
	maxEncoded = 512 << 10
	// maxLinkTTL bounds ViewLinkTTLSeconds as POST /api/sessions/{id}/links
	// bounds ttlSeconds.
	maxLinkTTL = 365 * 24 * 3600
)

// memberNamePattern keeps member names short, lower case and free of path
// separators: they become branch names and worktree paths. gitRefuses adds
// what git refuses in a branch name that the pattern lets through.
var memberNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,39}$`)

// gitRefuses reports whether git check-ref-format refuses a name that matches
// memberNamePattern as a branch name.
func gitRefuses(name string) bool {
	return strings.Contains(name, "..") || strings.HasSuffix(name, ".") || strings.HasSuffix(name, ".lock")
}

// Validate checks c apart from its ID, which the store checks, and its agents,
// which CheckAgents checks against a catalog: the name, goal, working
// directory and options, the members' names, agent IDs, prompts (with the
// goal in them, as they are typed), arguments and start conditions, all
// within their limits, and the size of c as JSON,
// which counts the ID and the times. The first problem found is returned; it
// matches ErrInvalid, and quotes at most 80 characters of a value.
func (c Crew) Validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return invalidf("name must not be empty")
	}
	if utf8.RuneCountInString(c.Name) > maxName {
		return invalidf("name must be at most %d characters", maxName)
	}
	if strings.ContainsFunc(c.Name, unicode.IsControl) {
		return invalidf("name must not contain control characters")
	}
	if utf8.RuneCountInString(c.Goal) > maxGoal {
		return invalidf("goal must be at most %d characters", maxGoal)
	}
	if len(c.Cwd) > maxCwd {
		return invalidf("cwd must be at most %d bytes", maxCwd)
	}
	if strings.ContainsRune(c.Cwd, 0) {
		return invalidf("cwd contains NUL")
	}
	if c.Where != WhereServer && c.Where != WhereHost {
		return invalidf("where %s must be %q or %q", quote(c.Where), WhereServer, WhereHost)
	}
	if c.Isolation != IsolationNone && c.Isolation != IsolationWorktree {
		return invalidf("isolation %s must be %q or %q", quote(c.Isolation), IsolationNone, IsolationWorktree)
	}
	if c.ViewLinkTTLSeconds < 0 || c.ViewLinkTTLSeconds > maxLinkTTL {
		return invalidf("viewLinkTtlSeconds must be between 0 and %d", maxLinkTTL)
	}
	if len(c.Members) > maxMembers {
		return invalidf("too many members (at most %d)", maxMembers)
	}
	names := make([]string, 0, len(c.Members))
	for _, m := range c.Members {
		if err := m.Validate(); err != nil {
			return err
		}
		if err := m.checkTypedPrompt(c.Goal); err != nil {
			return err
		}
		if slices.Contains(names, m.Name) {
			return invalidf("member %s: the name is used twice", quote(m.Name))
		}
		names = append(names, m.Name)
	}
	if err := c.checkStarts(names); err != nil {
		return err
	}
	b, err := json.Marshal(c)
	if err != nil {
		return invalidf("cannot be written as JSON: %v", err)
	}
	if len(b) > maxEncoded {
		return invalidf("the crew is %d bytes as JSON, more than %d (512 KiB)", len(b), maxEncoded)
	}
	return nil
}

// checkStarts checks the after conditions against the member names: each
// names another member, and following them from any member ends at one that
// does not start after another, so every member can start.
func (c Crew) checkStarts(names []string) error {
	after := make(map[string]string, len(c.Members)) // member -> the member it starts after
	for _, m := range c.Members {
		if m.Start.When != StartAfter {
			continue
		}
		if m.Start.Member == m.Name {
			return invalidf("member %s: cannot start after itself", quote(m.Name))
		}
		if !slices.Contains(names, m.Start.Member) {
			return invalidf("member %s: starts after %s, which is not a member of this crew", quote(m.Name), quote(m.Start.Member))
		}
		after[m.Name] = m.Start.Member
	}
	// A chain visits each member at most once before it ends, so at most
	// len(c.Members) steps: a name seen twice is a cycle.
	for _, m := range c.Members {
		chain := []string{m.Name}
		for next, ok := after[m.Name]; ok; next, ok = after[next] {
			seen := slices.Contains(chain, next)
			chain = append(chain, next)
			if seen {
				return invalidf("member %s: the start conditions go round in a cycle, so it never starts: %s", quote(m.Name), strings.Join(chain, " after "))
			}
		}
	}
	return nil
}

// Validate checks what m can be checked for alone: its name, agent ID,
// prompt, arguments and start condition, within their limits. Crew.Validate
// checks it against the other members, and a run against its members when m
// joins it. The error matches ErrInvalid.
func (m Member) Validate() error {
	if !memberNamePattern.MatchString(m.Name) {
		return invalidf("member %s: name must match %s", quote(m.Name), memberNamePattern)
	}
	if gitRefuses(m.Name) {
		return invalidf(`member %s: git refuses the name for a branch: no "..", and no "." or ".lock" at the end`, quote(m.Name))
	}
	if !catalog.ValidID(m.AgentID) {
		return invalidf("member %s: agentId %s is not an agent id (a-z, 0-9 and -, 1 to 32 of them)", quote(m.Name), quote(m.AgentID))
	}
	if utf8.RuneCountInString(m.Prompt) > maxPrompt {
		return invalidf("member %s: prompt must be at most %d characters", quote(m.Name), maxPrompt)
	}
	if len(m.Args) > maxArgs {
		return invalidf("member %s: too many args (at most %d)", quote(m.Name), maxArgs)
	}
	total := 0
	for i, a := range m.Args {
		if strings.ContainsRune(a, 0) {
			return invalidf("member %s: args[%d] contains NUL", quote(m.Name), i)
		}
		if len(a) > maxArg {
			return invalidf("member %s: args[%d] is longer than %d bytes", quote(m.Name), i, maxArg)
		}
		total += len(a)
	}
	if total > maxArgsBytes {
		return invalidf("member %s: args must be at most %d bytes in all", quote(m.Name), maxArgsBytes)
	}
	switch m.Start.When {
	case StartAfter:
		if m.Start.Member == "" {
			return invalidf("member %s: start.member must name the member to start after", quote(m.Name))
		}
	case StartImmediately, StartManual:
		if m.Start.Member != "" {
			return invalidf("member %s: start.member is only set with start.when %q", quote(m.Name), StartAfter)
		}
	default:
		return invalidf("member %s: start.when %s must be %q, %q or %q", quote(m.Name), quote(m.Start.When), StartImmediately, StartAfter, StartManual)
	}
	return nil
}

// Launchable reports why c cannot be launched as it is: it has no members, or
// it runs on a host, which the server cannot launch a session on. The error
// matches ErrInvalid.
func (c Crew) Launchable() error {
	if len(c.Members) == 0 {
		return invalidf("the crew has no members")
	}
	if c.Where != WhereServer {
		return invalidf("the crew runs on a host: only a crew that runs on the server can be launched")
	}
	return nil
}

// CheckAgents reports the first member whose agent cat does not list. It is
// apart from Validate because the catalog changes: the API checks it when a
// crew is saved or duplicated, and a launch checks it again. The error
// matches ErrInvalid.
func (c Crew) CheckAgents(cat catalog.Catalog) error {
	for _, m := range c.Members {
		if _, ok := cat.Get(m.AgentID); !ok {
			return invalidf("member %s: unknown agent %s", quote(m.Name), quote(m.AgentID))
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

// typedPrompt is the line a member's prompt types, less its carriage return:
// the prompt with the goal in place of $GOAL (ExpandPrompt), made one line as
// a handoff's message and a broadcast are, each line break, carriage return
// and tab a space, so that an agent that submits at a line break takes the
// whole prompt. The crew keeps the prompt as written.
func typedPrompt(prompt, goal string) string {
	return oneLine.Replace(ExpandPrompt(prompt, goal))
}

// typedPromptLen is len(typedPrompt(prompt, goal)) + 1, the bytes typing the
// prompt writes with its carriage return, counted without expanding it: a
// goal of 2000 characters in 800 references would take megabytes. Making it
// one line changes no length, so the count is exact.
func typedPromptLen(prompt, goal string) int {
	n := len(prompt) + 1
	for _, ref := range goalRef.FindAllStringIndex(prompt, -1) {
		n += len(goal) - (ref[1] - ref[0])
	}
	return n
}

// checkTypedPrompt checks that m's prompt, with goal in place of $GOAL, made
// one line and with a carriage return (typedPrompt), fits what a session
// takes at once (session.Local.Type): a member never fails to start for the
// size of its prompt. The error matches ErrInvalid.
func (m Member) checkTypedPrompt(goal string) error {
	if n := typedPromptLen(m.Prompt, goal); n > proto.MaxInput {
		return invalidf("member %s: the prompt with the goal in place of $GOAL is %d bytes, more than %d", quote(m.Name), n-1, proto.MaxInput-1)
	}
	return nil
}

// clone returns a copy of c that shares no slice with it.
func (c Crew) clone() Crew {
	c.Members = slices.Clone(c.Members)
	for i := range c.Members {
		c.Members[i].Args = slices.Clone(c.Members[i].Args)
	}
	return c
}
