package domain

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxCoordinationTasks = 32

type ExecutionCapabilities struct {
	RepositoryID string `json:"repositoryId"`
	CanExecute   bool   `json:"canExecute"`
	CanPublish   bool   `json:"canPublish"`
}

// Execution grants are provisioned independently from design-review grants.
// A person may review architecture without being permitted to spend execution
// resources or publish code. Agents cannot turn either grant into human authority.
type ExecutionGrantConfig struct {
	RepositoryID string `json:"repositoryId"`
	PrincipalID  string `json:"principalId"`
	CanExecute   bool   `json:"canExecute"`
	CanPublish   bool   `json:"canPublish"`
}

type PackagePin struct {
	ChangeID     string `json:"changeId"`
	RepositoryID string `json:"repositoryId"`
	Revision     int64  `json:"revision"`
	Digest       string `json:"digest"`
}

type CoordinationRepository struct {
	RepositoryID     string `json:"repositoryId"`
	Commit           string `json:"commit"`
	CollectionID     string `json:"collectionId"`
	ReceiptDigest    string `json:"receiptDigest"`
	FullSourceDigest string `json:"fullSourceDigest,omitempty"`
}

type TaskScope struct {
	RepositoryID  string   `json:"repositoryId"`
	WritablePaths []string `json:"writablePaths"`
}

type VerificationCommand struct {
	ID             string                    `json:"id"`
	RepositoryID   string                    `json:"repositoryId"`
	Argv           []string                  `json:"argv"`
	TimeoutSeconds int                       `json:"timeoutSeconds"`
	Requirements   []VerificationRequirement `json:"requirements,omitempty"`
}

type CoordinationTask struct {
	ID             string                `json:"id"`
	Perspective    string                `json:"perspective"`
	Profile        string                `json:"profile"`
	ProfileDigest  string                `json:"profileDigest,omitempty"`
	Image          string                `json:"image,omitempty"`
	Prompt         string                `json:"prompt"`
	DependsOn      []string              `json:"dependsOn"`
	Scopes         []TaskScope           `json:"scopes"`
	Checks         []VerificationCommand `json:"checks"`
	TimeoutSeconds int                   `json:"timeoutSeconds"`
}

// The immutable plan binds every producer to inspected packages and baselines.
// Task IDs are stable plan keys; workflow payloads use separate server-generated
// task IDs so prompts, source and review content stay outside workflow history.
type CoordinationPlan struct {
	SchemaVersion int                      `json:"schemaVersion"`
	GraphID       string                   `json:"graphId"`
	GraphDigest   string                   `json:"graphDigest"`
	Packages      []PackagePin             `json:"packages"`
	Repositories  []CoordinationRepository `json:"repositories"`
	Tasks         []CoordinationTask       `json:"tasks"`
	MaxParallel   int                      `json:"maxParallel"`
}

type RunAuthorization struct {
	Actor     string    `json:"actor"`
	Digest    string    `json:"digest"`
	CreatedAt time.Time `json:"createdAt"`
}

type TaskReceipt struct {
	TaskID         string    `json:"taskId"`
	TaskKey        string    `json:"taskKey"`
	Digest         string    `json:"digest"`
	Outcome        string    `json:"outcome"`
	ArtifactDigest string    `json:"artifactDigest,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
}

type CoordinationRun struct {
	ID                string               `json:"id"`
	WorkspaceID       string               `json:"workspaceId"`
	RepositoryID      string               `json:"repositoryId"`
	ProposerID        string               `json:"proposerId"`
	Plan              CoordinationPlan     `json:"plan"`
	Digest            string               `json:"digest"`
	CreatedAt         time.Time            `json:"createdAt"`
	Authorization     *RunAuthorization    `json:"authorization,omitempty"`
	CancelRequestedAt *time.Time           `json:"cancelRequestedAt,omitempty"`
	Execution         *CollectionExecution `json:"execution,omitempty"`
	Receipts          []TaskReceipt        `json:"receipts"`
}

type CoordinationPage struct {
	Runs       []CoordinationSummary `json:"runs"`
	NextBefore string                `json:"nextBefore,omitempty"`
}

// Discovery stays bounded even when a plan contains many prompts and paths.
// Retrieve an individual run to inspect its exact plan before authorization.
type CoordinationSummary struct {
	ID                string               `json:"id"`
	WorkspaceID       string               `json:"workspaceId"`
	RepositoryID      string               `json:"repositoryId"`
	ProposerID        string               `json:"proposerId"`
	Digest            string               `json:"digest"`
	CreatedAt         time.Time            `json:"createdAt"`
	Authorized        bool                 `json:"authorized"`
	CancelRequestedAt *time.Time           `json:"cancelRequestedAt,omitempty"`
	Execution         *CollectionExecution `json:"execution,omitempty"`
	Tasks             int                  `json:"tasks"`
	Receipts          int                  `json:"receipts"`
}

func planKey(s string) bool {
	if len(s) < 1 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' && c != '.' {
			return false
		}
	}
	return true
}

func scopedPath(path string) bool {
	if path == "" || len(path) > 1024 || !utf8.ValidString(path) || strings.TrimSpace(path) == "" || strings.HasPrefix(path, "/") || strings.HasPrefix(path, "-") || strings.ContainsAny(path, "\\\x00\r\n\t") {
		return false
	}
	for _, c := range path {
		if c < 32 || c == 127 {
			return false
		}
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." || strings.EqualFold(part, ".git") {
			return false
		}
	}
	return true
}

// ExecutionImagePinned accepts only immutable image identities reviewed in a plan.
func ExecutionImagePinned(image string) bool {
	if strings.HasPrefix(image, "sha256:") {
		return IsLowerHex(strings.TrimPrefix(image, "sha256:"), 64)
	}
	parts := strings.Split(image, "@sha256:")
	return len(parts) == 2 && parts[0] != "" && !strings.ContainsAny(parts[0], " \t\r\n") && IsLowerHex(parts[1], 64)
}

func PathsOverlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// Empty task collections have one retained representation, so omitted optional
// lists and explicit empty lists do not fork an idempotent proposal's identity.
func NormalizeCoordinationPlan(p CoordinationPlan) (CoordinationPlan, error) {
	if err := ValidateCoordinationPlan(p); err != nil {
		return CoordinationPlan{}, err
	}
	p.Tasks = append([]CoordinationTask{}, p.Tasks...)
	for i := range p.Tasks {
		t := &p.Tasks[i]
		t.DependsOn = append([]string{}, t.DependsOn...)
		t.Checks = append([]VerificationCommand{}, t.Checks...)
		for j := range t.Checks {
			t.Checks[j].Requirements = append([]VerificationRequirement(nil), t.Checks[j].Requirements...)
		}
		t.Scopes = append([]TaskScope{}, t.Scopes...)
		for j := range t.Scopes {
			t.Scopes[j].WritablePaths = append([]string{}, t.Scopes[j].WritablePaths...)
		}
	}
	return p, nil
}

// ValidateCoordinationPlan rejects ambiguous ownership before admission. Tasks
// writing overlapping paths must be ordered by an explicit dependency, rather
// than relying on model cooperation or which parallel worker happens to finish.
func ValidateCoordinationPlan(p CoordinationPlan) error {
	invalid := func(why string) error { return fmt.Errorf("%w: %s", ErrInvalidInput, why) }
	encoded, err := json.Marshal(p)
	if err != nil || len(encoded) > 1<<20 {
		return invalid("plan exceeds encoded size bound")
	}
	if p.SchemaVersion != 1 || !IsLowerHex(p.GraphID, 32) || !IsLowerHex(p.GraphDigest, 64) || p.MaxParallel < 1 || p.MaxParallel > 4 || len(p.Repositories) < 1 || len(p.Repositories) > 16 || len(p.Packages) != len(p.Repositories) || len(p.Tasks) < 1 || len(p.Tasks) > MaxCoordinationTasks {
		return invalid("invalid plan identity or bounds")
	}
	repos := map[string]bool{}
	for _, r := range p.Repositories {
		if r.FullSourceDigest != "" && !IsLowerHex(r.FullSourceDigest, 64) {
			return ErrInvalidInput
		}
		if ValidateAccessID(r.RepositoryID) != nil || repos[r.RepositoryID] || !IsLowerHex(r.Commit, 40) || !IsLowerHex(r.CollectionID, 32) || !IsLowerHex(r.ReceiptDigest, 64) {
			return invalid("each repository needs one exact source receipt")
		}
		repos[r.RepositoryID] = true
	}
	packages := map[string]bool{}
	pinned := map[string]bool{}
	for _, pin := range p.Packages {
		if ValidateAccessID(pin.ChangeID) != nil || !repos[pin.RepositoryID] || pinned[pin.RepositoryID] || packages[pin.ChangeID] || pin.Revision < 1 || !IsLowerHex(pin.Digest, 64) {
			return invalid("each repository needs one exact package revision")
		}
		pinned[pin.RepositoryID] = true
		packages[pin.ChangeID] = true
	}
	tasks := map[string]CoordinationTask{}
	for _, task := range p.Tasks {
		if task.ProfileDigest != "" && !IsLowerHex(task.ProfileDigest, 64) || task.Image != "" && !ExecutionImagePinned(task.Image) {
			return ErrInvalidInput
		}
		if !planKey(task.ID) || !planKey(task.Profile) || task.Prompt == "" || len(task.Prompt) > 32<<10 || !utf8.ValidString(task.Prompt) || len(task.Scopes) < 1 || len(task.Scopes) > len(repos) || len(task.Checks) > 16 || len(task.DependsOn) > MaxCoordinationTasks || task.TimeoutSeconds < 1 || task.TimeoutSeconds > 1800 {
			return invalid("invalid task bounds or identity")
		}
		if _, ok := tasks[task.ID]; ok {
			return invalid("duplicate task ID")
		}
		switch task.Perspective {
		case "architect", "developer", "qc", "product", "operations":
		default:
			return invalid("unknown task perspective")
		}
		scopes := map[string]bool{}
		for _, scope := range task.Scopes {
			if !repos[scope.RepositoryID] || scopes[scope.RepositoryID] || len(scope.WritablePaths) > 128 {
				return invalid("invalid task repository scope")
			}
			scopes[scope.RepositoryID] = true
			paths := map[string]bool{}
			for _, path := range scope.WritablePaths {
				if !scopedPath(path) || paths[path] {
					return invalid("invalid or duplicate writable path")
				}
				paths[path] = true
			}
		}
		checks := map[string]bool{}
		for _, check := range task.Checks {
			if ValidateVerificationRequirements(check.Requirements) != nil {
				return invalid("invalid verification requirement references")
			}
			for _, requirement := range check.Requirements {
				matched := false
				for _, pin := range p.Packages {
					if pin.ChangeID == requirement.ChangeID && pin.Revision == requirement.Revision && pin.Digest == requirement.Digest && scopes[pin.RepositoryID] {
						matched = true
						break
					}
				}
				if !matched {
					return invalid("verification requirement must match an exact task-scoped package pin")
				}
			}
			if !planKey(check.ID) || checks[check.ID] || !scopes[check.RepositoryID] || len(check.Argv) < 1 || len(check.Argv) > 64 || check.TimeoutSeconds < 1 || check.TimeoutSeconds > task.TimeoutSeconds {
				return invalid("invalid verification command")
			}
			checks[check.ID] = true
			for _, arg := range check.Argv {
				if len(arg) > 4096 || !utf8.ValidString(arg) || strings.ContainsRune(arg, 0) {
					return invalid("invalid verification argument")
				}
			}
			if strings.TrimSpace(check.Argv[0]) == "" {
				return invalid("verification executable required")
			}
		}
		tasks[task.ID] = task
	}
	marks := map[string]int{}
	ancestors := map[string]map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if marks[id] == 1 {
			return invalid("task dependencies form a cycle")
		}
		if marks[id] == 2 {
			return nil
		}
		marks[id] = 1
		task, ok := tasks[id]
		if !ok {
			return invalid("task dependency does not exist")
		}
		ancestors[id] = map[string]bool{}
		seen := map[string]bool{}
		for _, dep := range task.DependsOn {
			if dep == id || seen[dep] {
				return invalid("duplicate or self dependency")
			}
			seen[dep] = true
			if err := visit(dep); err != nil {
				return err
			}
			ancestors[id][dep] = true
			for ancestor := range ancestors[dep] {
				ancestors[id][ancestor] = true
			}
		}
		marks[id] = 2
		return nil
	}
	for _, task := range p.Tasks {
		if err := visit(task.ID); err != nil {
			return err
		}
	}
	for i, a := range p.Tasks {
		for _, b := range p.Tasks[i+1:] {
			if ancestors[a.ID][b.ID] || ancestors[b.ID][a.ID] {
				continue
			}
			for _, as := range a.Scopes {
				for _, bs := range b.Scopes {
					if as.RepositoryID != bs.RepositoryID {
						continue
					}
					for _, ap := range as.WritablePaths {
						for _, bp := range bs.WritablePaths {
							if PathsOverlap(ap, bp) {
								return invalid("parallel tasks have overlapping write scopes")
							}
						}
					}
				}
			}
		}
	}
	data, err := JSONDigest(p)
	if err != nil || data == "" {
		return invalid("invalid plan encoding")
	}
	return nil
}

func CoordinationRepositoryIDs(plan CoordinationPlan) []string {
	ids := make([]string, 0, len(plan.Repositories))
	for _, r := range plan.Repositories {
		ids = append(ids, r.RepositoryID)
	}
	sort.Strings(ids)
	return ids
}
