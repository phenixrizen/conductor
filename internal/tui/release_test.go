package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/execution"
	"github.com/phenixrizen/conductor/internal/reviewinput"
	"github.com/phenixrizen/conductor/pkg/client"
)

func releaseFixture(t *testing.T) (releaseModel, domain.CoordinationRun, *[]releaseRequest) {
	t.Helper()
	profile := domain.WorkerProfile{Adapter: "command/v1", Command: []string{"true"}}
	pd, _ := domain.JSONDigest(profile)
	image := "sha256:" + strings.Repeat("e", 64)
	p, _ := domain.NormalizeCoordinationPlan(domain.CoordinationPlan{SchemaVersion: 1, GraphID: strings.Repeat("a", 32), GraphDigest: strings.Repeat("b", 64), Repositories: []domain.CoordinationRepository{{RepositoryID: "application", Commit: strings.Repeat("a", 40), CollectionID: strings.Repeat("c", 32), ReceiptDigest: strings.Repeat("d", 64), FullSourceDigest: strings.Repeat("e", 64)}}, Packages: []domain.PackagePin{{RepositoryID: "application", ChangeID: "CHG-example", Revision: 1, Digest: strings.Repeat("f", 64)}}, Tasks: []domain.CoordinationTask{{ID: "one", Perspective: "architect", Profile: "synthetic", ProfileDigest: pd, Image: image, Prompt: "Inspect \x1b]52;c;YQ==\a source", Scopes: []domain.TaskScope{{RepositoryID: "application", WritablePaths: []string{"src"}}}, TimeoutSeconds: 30}}, MaxParallel: 1})
	if domain.ValidateCoordinationPlan(p) != nil {
		t.Fatal("invalid test plan")
	}
	digest, _ := domain.JSONDigest(p)
	run := domain.CoordinationRun{ID: strings.Repeat("1", 32), WorkspaceID: "team", RepositoryID: "application", ProposerID: "agent", Plan: p, Digest: digest, Receipts: []domain.TaskReceipt{}}
	requests := []releaseRequest{}
	execute := func(r releaseRequest) (tea.Cmd, context.CancelFunc) {
		requests = append(requests, r)
		return func() tea.Msg { return nil }, func() {}
	}
	m := releaseModel{access: accessState{authenticated: true, ready: true, workspaceID: "team", repositoryID: "application", generation: 1, principal: domain.Principal{ID: "reviewer", Kind: "human"}, repository: domain.ManagedRepository{CanRead: true, CanAuthor: true, CanApprove: true}}, view: "runs", record: run, inspectID: run.ID, execute: execute, width: 140, height: 45, cursors: []string{""}, caps: domain.ExecutionCapabilities{RepositoryID: "application", CanExecute: true}, profiles: domain.ExecutionProfilePage{Profiles: []domain.ExecutionProfile{{ID: "synthetic", ProfileDigest: pd, Image: image, WorkerProfile: profile}}}}
	m.rebuildRelease()
	return m, run, &requests
}
func releaseKey(m releaseModel, key string) (releaseModel, tea.Cmd) {
	var k tea.KeyMsg
	if key == "enter" {
		k = tea.KeyMsg{Type: tea.KeyEnter}
	} else if key == "esc" {
		k = tea.KeyMsg{Type: tea.KeyEsc}
	} else {
		k = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	next, cmd := m.Update(k)
	return next.(releaseModel), cmd
}
func TestReleaseConfirmationHasNoRefreshAndRejectsChangedEvidence(t *testing.T) {
	m, run, calls := releaseFixture(t)
	if strings.Contains(m.View(), "\x1b]52") {
		t.Fatal("repository terminal controls escaped renderer")
	}
	m, _ = releaseKey(m, "a")
	if m.prompt != "authorize-run" || len(*calls) != 0 || m.pending.digest != run.Digest {
		t.Fatal("confirmation refreshed or replaced digest")
	}
	m, _ = releaseKey(m, "authorize-run")
	m, _ = releaseKey(m, "enter")
	if len(*calls) != 1 || (*calls)[0].op != "authorize" || (*calls)[0].digest != run.Digest {
		t.Fatal("wrong command", *calls)
	}
	wrong := run
	wrong.Digest = strings.Repeat("0", 64)
	next, _ := m.Update(releaseResult{releaseRequest: (*calls)[0], value: wrong})
	m = next.(releaseModel)
	if m.access.ready || m.record != nil || m.draft != nil || m.caps.CanExecute {
		t.Fatal("substituted authorization response retained inspection")
	}
}
func TestReleaseAgentProfileAndViewportGates(t *testing.T) {
	m, _, calls := releaseFixture(t)
	m.access.principal.Kind = "agent"
	m, _ = releaseKey(m, "a")
	if m.prompt != "" || len(*calls) != 0 {
		t.Fatal("agent could authorize")
	}
	m.access.principal.Kind = "human"
	m.profiles.Profiles = nil
	m.profiles.Truncated = true
	m, _ = releaseKey(m, "a")
	if m.prompt != "" || len(*calls) != 0 {
		t.Fatal("missing profile invented")
	}
	m, _, calls = releaseFixture(t)
	m.width = 35
	m, _ = releaseKey(m, "a")
	if m.prompt != "" || len(*calls) != 0 {
		t.Fatal("incomplete viewport could confirm")
	}
}
func TestReleaseLostCreateRetainsKeyAndRevocationClearsEveryView(t *testing.T) {
	m, run, calls := releaseFixture(t)
	b, _ := json.Marshal(map[string]any{"idempotencyKey": "same-run-key", "input": run.Plan})
	d, err := reviewinput.Decode(b, "run")
	if err != nil {
		t.Fatal(err)
	}
	m.draft = &d
	m, _ = releaseKey(m, "s")
	m, _ = releaseKey(m, "propose-run")
	m, _ = releaseKey(m, "enter")
	req := (*calls)[0]
	next, _ := m.Update(releaseResult{releaseRequest: req, err: context.DeadlineExceeded})
	m = next.(releaseModel)
	if m.draft == nil || !m.uncertain || m.draft.IdempotencyKey != "same-run-key" {
		t.Fatal("lost immutable recovery input")
	}
	m, _ = releaseKey(m, "s")
	m, _ = releaseKey(m, "propose-run")
	m, _ = releaseKey(m, "enter")
	retry := (*calls)[1]
	if retry.draft.Digest != req.draft.Digest || retry.draft.IdempotencyKey != req.draft.IdempotencyKey {
		t.Fatal("retry replaced input")
	}
	next, _ = m.Update(releaseResult{releaseRequest: retry, err: &client.APIError{StatusCode: http.StatusForbidden}})
	m = next.(releaseModel)
	if m.access.ready || m.draft != nil || m.record != nil || m.artifact != nil || len(m.profiles.Profiles) != 0 {
		t.Fatal("revocation retained private state")
	}
	next, _ = m.Update(releaseResult{releaseRequest: req, value: run})
	m = next.(releaseModel)
	if m.record != nil {
		t.Fatal("superseded result restored inspection")
	}
}
func TestReleaseReadResponseRejectsScopeAndCommandResultMismatch(t *testing.T) {
	m, run, _ := releaseFixture(t)
	if err := m.validateRelease(releaseResult{releaseRequest: releaseRequest{op: "get", view: "runs", id: run.ID}, value: run}); err != nil {
		t.Fatal(err)
	}
	bad := run
	bad.WorkspaceID = "foreign"
	if m.validateRelease(releaseResult{releaseRequest: releaseRequest{op: "get", view: "runs", id: run.ID}, value: bad}) == nil {
		t.Fatal("foreign workspace accepted")
	}
	auth := run
	auth.Authorization = &domain.RunAuthorization{Actor: "other", Digest: run.Digest, CreatedAt: time.Now()}
	if m.validateRelease(releaseResult{releaseRequest: releaseRequest{op: "authorize", view: "runs", id: run.ID, digest: run.Digest}, value: auth}) == nil {
		t.Fatal("another actor authorization mistaken for this command")
	}
}

func TestGraphArtifactBindsEveryInspectedSourceField(t *testing.T) {
	m, _, _ := releaseFixture(t)
	m.view = "graphs"
	text := "synthetic source\n"
	source := domain.GraphSourceRecord{GraphSource: domain.GraphSource{RepositoryID: "related/repository", CollectionID: strings.Repeat("b", 32), Digest: strings.Repeat("c", 64), FullSourceDigest: strings.Repeat("d", 64)}, Commit: strings.Repeat("a", 40), CollectedAt: time.Now().UTC(), Freshness: "unknown"}
	g := domain.RepositoryGraph{ID: strings.Repeat("f", 32), Digest: strings.Repeat("e", 64), Snapshot: domain.GraphSnapshot{Sources: []domain.GraphSourceRecord{source}}}
	m.record = g
	req := releaseRequest{op: "graph-artifact", view: "graphs", id: g.ID, graphArtifact: domain.GraphArtifactQuery{GraphDigest: g.Digest, RepositoryID: source.RepositoryID, CollectionID: source.CollectionID, ReceiptDigest: source.Digest, FullSourceDigest: source.FullSourceDigest, Path: "README.md"}}
	artifact := domain.ContextArtifact{Path: "README.md", State: "collected", BlobOID: strings.Repeat("a", 40), Text: &text, Digest: execution.Sum([]byte(text))}
	value := domain.GraphArtifactResult{GraphID: g.ID, Digest: g.Digest, Source: source, Artifact: artifact, Coverage: "full_source"}
	if err := m.validateRelease(releaseResult{releaseRequest: req, value: value}); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*domain.GraphArtifactResult){func(v *domain.GraphArtifactResult) { v.Source.RepositoryID = "foreign" }, func(v *domain.GraphArtifactResult) { v.Source.FullSourceDigest = strings.Repeat("0", 64) }, func(v *domain.GraphArtifactResult) { v.Artifact.Digest = strings.Repeat("0", 64) }, func(v *domain.GraphArtifactResult) { v.Artifact.Path = "other" }} {
		bad := value
		change(&bad)
		if m.validateRelease(releaseResult{releaseRequest: req, value: bad}) == nil {
			t.Fatal("substituted source artifact accepted")
		}
	}
}

func TestTaskArtifactCapturesReceiptAndRejectsSubstitution(t *testing.T) {
	m, run, requests := releaseFixture(t)
	result := execution.Result{Producer: execution.Evidence{State: "failed", Output: "Synthetic failed report\x1b]52;c;bad\a"}, Patches: []execution.Patch{}}
	raw, _ := json.Marshal(result)
	digest, _ := domain.JSONDigest(result)
	receipt := domain.TaskReceipt{TaskID: strings.Repeat("a", 32), TaskKey: "one", Outcome: "failed", ArtifactDigest: digest}
	run.Receipts = []domain.TaskReceipt{receipt}
	m.record = run
	m, _ = releaseKey(m, "v")
	if m.prompt != "task-artifact" {
		t.Fatal("artifact selection absent")
	}
	m.input = "one"
	m, _ = releaseKey(m, "enter")
	if len(*requests) != 1 {
		t.Fatal("artifact selection fetched new run")
	}
	req := (*requests)[0]
	if req.op != "task-artifact" || req.taskArtifact.RunDigest != run.Digest || req.taskArtifact.TaskID != receipt.TaskID || req.taskArtifact.ArtifactDigest != digest {
		t.Fatal("artifact request lost inspected receipt")
	}
	value := domain.CoordinationArtifact{RunID: run.ID, RunDigest: run.Digest, TaskID: receipt.TaskID, ArtifactDigest: digest, Artifact: raw}
	if err := m.validateRelease(releaseResult{releaseRequest: req, value: value}); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*domain.CoordinationArtifact){func(v *domain.CoordinationArtifact) { v.TaskID = strings.Repeat("f", 32) }, func(v *domain.CoordinationArtifact) { v.RunDigest = strings.Repeat("f", 64) }, func(v *domain.CoordinationArtifact) { v.ArtifactDigest = strings.Repeat("f", 64) }, func(v *domain.CoordinationArtifact) { v.Artifact = []byte(`{}`) }} {
		bad := value
		change(&bad)
		if m.validateRelease(releaseResult{releaseRequest: req, value: bad}) == nil {
			t.Fatal("substituted task output accepted")
		}
	}
	m.presentation = value
	m.rebuildRelease()
	if strings.Contains(strings.Join(m.lines, "\n"), "\x1b") {
		t.Fatal("report controls reached terminal")
	}
}
