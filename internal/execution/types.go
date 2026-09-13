// Package execution runs approved task inputs inside disposable Docker sandboxes.
// It produces source-bound patches and separately executed check evidence; it
// never grants approval, publishes code, or sequences durable work.
package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/phenixrizen/conductor/internal/domain"
)

const (
	CodexVersion   = "0.154.0"
	ClaudeVersion  = "2.1.270"
	MaxInputBytes  = 64 << 20
	MaxSourceBytes = 32 << 20
	MaxPatchBytes  = 8 << 20
	MaxLogBytes    = 1 << 20
	MaxFiles       = 10000
)

var ErrInvalid = errors.New("invalid execution input")
var ErrUnavailable = errors.New("execution unavailable")
var ErrSandbox = errors.New("sandbox failed")
var ErrDependencyConflict = errors.New("dependency patches conflict")

var checkIdentifier = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,64}$`)
var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)
var oid = regexp.MustCompile(`^[0-9a-f]{40}$`)
var digest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Repository bundles are acquired by a trusted activity under canonical scope.
// They are complete Git bundles, not directories controlled by an API caller.
type Repository struct {
	ID            string   `json:"id"`
	Commit        string   `json:"commit"`
	Bundle        []byte   `json:"bundle"`
	WritablePaths []string `json:"writablePaths"`
	Dependencies  []Patch  `json:"dependencies,omitempty"`
}

type Check struct {
	ID             string                           `json:"id"`
	RepositoryID   string                           `json:"repositoryId"`
	Argv           []string                         `json:"argv"`
	TimeoutSeconds int                              `json:"timeoutSeconds"`
	Requirements   []domain.VerificationRequirement `json:"requirements,omitempty"`
}

type Request struct {
	RunID             string       `json:"runId"`
	TaskID            string       `json:"taskId"`
	ChangeID          string       `json:"changeId"`
	Revision          int64        `json:"revision"`
	Digest            string       `json:"digest"`
	GraphDigest       string       `json:"graphDigest"`
	PlanDigest        string       `json:"planDigest,omitempty"`
	DependencyDigests []string     `json:"dependencyDigests,omitempty"`
	Repositories      []Repository `json:"repositories"`
	Prompt            string       `json:"prompt"`
	Checks            []Check      `json:"checks"`
	TimeoutSeconds    int          `json:"timeoutSeconds"`
}

// Profile is trusted operator configuration, outside the API task payload.
// command/v1 runs a configured program; it is not an assistant impersonation.
type Profile struct {
	Adapter      string   `json:"adapter"`
	Model        string   `json:"model,omitempty"`
	Command      []string `json:"command,omitempty"`
	MaxBudgetUSD string   `json:"maxBudgetUsd,omitempty"`
}

type Evidence struct {
	ID           string                           `json:"id"`
	RepositoryID string                           `json:"repositoryId"`
	Argv         []string                         `json:"argv"`
	State        string                           `json:"state"`
	ExitCode     *int                             `json:"exitCode,omitempty"`
	Output       string                           `json:"output,omitempty"`
	OutputDigest string                           `json:"outputDigest"`
	Truncated    bool                             `json:"truncated"`
	StartedAt    time.Time                        `json:"startedAt"`
	FinishedAt   time.Time                        `json:"finishedAt"`
	SourceDigest string                           `json:"sourceDigest"`
	Requirements []domain.VerificationRequirement `json:"requirements,omitempty"`
}

type Patch struct {
	RepositoryID string   `json:"repositoryId"`
	BaseCommit   string   `json:"baseCommit"`
	BaseTree     string   `json:"baseTree"`
	ResultTree   string   `json:"resultTree"`
	Patch        []byte   `json:"patch"`
	Digest       string   `json:"digest"`
	Paths        []string `json:"paths"`
}

type Result struct {
	CleanupConfirmed bool       `json:"cleanupConfirmed"`
	InputDigest      string     `json:"inputDigest"`
	ProfileDigest    string     `json:"profileDigest"`
	Image            string     `json:"image"`
	Adapter          string     `json:"adapter"`
	AdapterVersion   string     `json:"adapterVersion"`
	Producer         Evidence   `json:"producer"`
	Patches          []Patch    `json:"patches"`
	Checks           []Evidence `json:"checks"`
	StartedAt        time.Time  `json:"startedAt"`
	FinishedAt       time.Time  `json:"finishedAt"`
}

func Sum(b []byte) string { value := sha256.Sum256(b); return hex.EncodeToString(value[:]) }

func InputDigest(r Request) string { b, _ := json.Marshal(r); return Sum(b) }

func safePath(p string) bool {
	if p == "" || len(p) > 1024 || path.Clean(p) != p || path.IsAbs(p) || strings.ContainsAny(p, "\\\x00\r\n\t") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." || part == "." || strings.EqualFold(part, ".git") {
			return false
		}
	}
	return true
}

// Canonical repository IDs are labels, not paths. Their opaque directory mapping
// supports every bounded server ID without permitting traversal or collisions
// with another repository's spelling.
func validRepositoryID(id string) bool {
	return id != "" && len(id) <= 128 && utf8.ValidString(id) && strings.TrimSpace(id) == id && !strings.ContainsFunc(id, unicode.IsControl)
}
func RepositoryDirectory(id string) string { return "/work/repos/r-" + Sum([]byte(id)) }
func directoryKey(id string) string        { return "r-" + Sum([]byte(id)) }

func allowedPath(p string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}

func ValidateRequest(r Request) error {
	if r.PlanDigest != "" && !digest.MatchString(r.PlanDigest) {
		return ErrInvalid
	}
	if !identifier.MatchString(r.RunID) || !identifier.MatchString(r.TaskID) || !identifier.MatchString(r.ChangeID) || r.Revision < 1 || !digest.MatchString(r.Digest) || !digest.MatchString(r.GraphDigest) {
		return fmt.Errorf("%w: exact task, package and graph identity required", ErrInvalid)
	}
	if r.TimeoutSeconds < 1 || r.TimeoutSeconds > 3600 || len(r.Prompt) == 0 || len(r.Prompt) > 256<<10 || len(r.Repositories) < 1 || len(r.Repositories) > 16 || len(r.Checks) > 32 || len(r.DependencyDigests) > 64 {
		return fmt.Errorf("%w: request bounds exceeded", ErrInvalid)
	}
	for _, d := range r.DependencyDigests {
		if !digest.MatchString(d) {
			return fmt.Errorf("%w: dependency digest", ErrInvalid)
		}
	}
	repos := map[string]bool{}
	total := 0
	for _, repo := range r.Repositories {
		if !validRepositoryID(repo.ID) || repos[repo.ID] || !oid.MatchString(repo.Commit) || len(repo.Bundle) == 0 || len(repo.Bundle) > MaxSourceBytes || len(repo.WritablePaths) > 128 {
			return fmt.Errorf("%w: repository identity or bounds", ErrInvalid)
		}
		repos[repo.ID] = true
		total += len(repo.Bundle)
		for _, p := range repo.WritablePaths {
			if !safePath(p) {
				return fmt.Errorf("%w: writable path", ErrInvalid)
			}
		}
		if len(repo.Dependencies) > 32 {
			return fmt.Errorf("%w: dependency patch bound", ErrInvalid)
		}
		for _, p := range repo.Dependencies {
			if p.RepositoryID != repo.ID || p.BaseCommit != repo.Commit || !oid.MatchString(p.BaseTree) || !oid.MatchString(p.ResultTree) || p.Digest != Sum(p.Patch) || len(p.Patch) > MaxPatchBytes || len(p.Paths) > MaxFiles {
				return fmt.Errorf("%w: dependency patch identity or bound", ErrInvalid)
			}
			for _, name := range p.Paths {
				if !safePath(name) {
					return fmt.Errorf("%w: dependency patch path", ErrInvalid)
				}
			}
		}
	}
	if total > MaxSourceBytes {
		return fmt.Errorf("%w: total bundle bytes", ErrInvalid)
	}
	checks := map[string]bool{}
	for _, c := range r.Checks {
		if domain.ValidateVerificationRequirements(c.Requirements) != nil {
			return fmt.Errorf("%w: verification requirements", ErrInvalid)
		}
		if !checkIdentifier.MatchString(c.ID) || checks[c.ID] || !repos[c.RepositoryID] || c.TimeoutSeconds < 1 || c.TimeoutSeconds > r.TimeoutSeconds || !validArgv(c.Argv) {
			return fmt.Errorf("%w: check identity, command or deadline", ErrInvalid)
		}
		checks[c.ID] = true
	}
	return nil
}

func validArgv(args []string) bool {
	if len(args) < 1 || len(args) > 128 || args[0] == "" {
		return false
	}
	for _, arg := range args {
		if len(arg) > 16384 || strings.ContainsRune(arg, 0) {
			return false
		}
	}
	return true
}

func (p Profile) Validate() error {
	if len(p.Model) > 128 || strings.ContainsAny(p.Model, "\x00\r\n") {
		return fmt.Errorf("%w: model identifier", ErrInvalid)
	}
	switch p.Adapter {
	case "codex/0.154.0":
		if strings.TrimSpace(p.Model) == "" {
			return fmt.Errorf("%w: native profile requires an explicit operator model ID", ErrInvalid)
		}
		if len(p.Command) != 0 || p.MaxBudgetUSD != "" {
			return fmt.Errorf("%w: Codex profile has no currency budget contract", ErrInvalid)
		}
	case "claude-code/2.1.270":
		if strings.TrimSpace(p.Model) == "" {
			return fmt.Errorf("%w: native profile requires an explicit operator model ID", ErrInvalid)
		}
		if len(p.Command) != 0 || !regexp.MustCompile(`^(?:[1-9][0-9]{0,2}|0\.[0-9]{1,2}|[1-9][0-9]{0,2}\.[0-9]{1,2})$`).MatchString(p.MaxBudgetUSD) || p.MaxBudgetUSD == "0.0" || p.MaxBudgetUSD == "0.00" {
			return fmt.Errorf("%w: Claude requires an explicit bounded USD budget", ErrInvalid)
		}
	case "command/v1":
		if !validArgv(p.Command) || p.Model != "" || p.MaxBudgetUSD != "" {
			return fmt.Errorf("%w: command profile", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: unsupported adapter profile", ErrUnavailable)
	}
	return nil
}
