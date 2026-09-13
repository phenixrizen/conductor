package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/reviewinput"
	"github.com/phenixrizen/conductor/pkg/client"
)

// The release workbench uses its own controls; a graph or execution key cannot
// fall through to package approval. Identity, scope and credential stay fixed.
type releaseModel struct {
	access                                     accessState
	view, inspectID                            string
	execute                                    releaseExecutor
	cancel                                     context.CancelFunc
	serial                                     int
	busy, status, prompt, input                string
	pending                                    releaseRequest
	blocked, uncertain, pageTruncated          bool
	width, height, offset, selected, pageIndex int
	cursors                                    []string
	next                                       string
	rows                                       []releaseRow
	record, presentation                       any
	draft                                      *reviewinput.Draft
	artifact                                   *domain.DeliveryArtifact
	caps                                       domain.ExecutionCapabilities
	profiles                                   domain.ExecutionProfilePage
	tracker                                    domain.TrackerSettings
	lines                                      []string
}
type releaseRow struct{ ID, Digest, Label string }
type releaseStart struct{}

func validReleaseView(view string) bool {
	return view == "graphs" || view == "runs" || view == "deliveries" || view == "tracker"
}
func releaseSession(ctx context.Context, c *client.Client, options Options) (releaseModel, error) {
	base, err := sessionModel(ctx, c, options)
	if err != nil {
		return releaseModel{}, err
	}
	if !base.access.authenticated || !validReleaseView(options.View) || options.ID != "" && !domain.IsLowerHex(options.ID, 32) {
		return releaseModel{}, errors.New("release workbench requires authenticated workspace/repository scope and --view graphs, runs, deliveries or tracker")
	}
	fixed := *c
	if c.HTTP != nil {
		copyHTTP := *c.HTTP
		fixed.HTTP = &copyHTTP
	}
	return releaseModel{access: base.access, view: options.View, inspectID: options.ID, execute: releaseRunner(ctx, &fixed), width: 100, height: 30, cursors: []string{""}}, nil
}
func (m releaseModel) Init() tea.Cmd {
	return tea.Batch(func() tea.Msg { return releaseStart{} }, collectionClock())
}
func (m *releaseModel) clear() {
	m.rows = nil
	m.record = nil
	m.presentation = nil
	m.draft = nil
	m.artifact = nil
	m.lines = nil
	m.cursors = []string{""}
	m.pageIndex = 0
	m.next = ""
	m.selected = 0
	m.offset = 0
	m.prompt = ""
	m.input = ""
	m.pending = releaseRequest{}
	m.blocked = true
	m.uncertain = false
	m.pageTruncated = false
	m.caps = domain.ExecutionCapabilities{}
	m.profiles = domain.ExecutionProfilePage{}
	m.tracker = domain.TrackerSettings{}
}
func (m releaseModel) refresh() (tea.Model, tea.Cmd) {
	if m.cancel != nil {
		m.cancel()
	}
	m.serial++
	m.cancel = nil
	m.busy = ""
	m.clear()
	m.access.ready = false
	m.access.generation++
	if m.access.restartRequired {
		m.status = "Access unavailable: " + errPrincipalChanged.Error()
		return m, nil
	}
	m.status = "Checking fixed identity and scope..."
	return m.start(releaseRequest{op: "access", view: m.view, id: m.inspectID})
}
func (m releaseModel) start(req releaseRequest) (tea.Model, tea.Cmd) {
	if req.op != "access" && !m.access.ready {
		m.status = "Access unavailable: r rechecks access."
		return m, nil
	}
	if releaseWrite(req.op) {
		if req.generation != m.access.generation {
			m.status = "Action blocked: access changed; inspect again."
			return m, nil
		}
		if err := m.allowed(req); err != nil {
			m.status = "Action blocked: " + err.Error()
			m.prompt = ""
			m.pending = releaseRequest{}
			return m, nil
		}
	}
	if m.cancel != nil {
		m.cancel()
	}
	m.serial++
	req.serial = m.serial
	req.view = m.view
	m.busy = req.op
	m.prompt = ""
	m.input = ""
	m.pending = releaseRequest{}
	cmd, cancel := m.execute(req)
	m.cancel = cancel
	return m, cmd
}
func releaseWrite(op string) bool { return op == "create" || op == "authorize" || op == "cancel" }
func (m releaseModel) list(index int) (tea.Model, tea.Cmd) {
	if index == 0 {
		m.cursors = []string{""}
	}
	m.pageIndex = index
	m.rows = nil
	m.record = nil
	m.presentation = nil
	m.artifact = nil
	m.inspectID = ""
	m.next = ""
	m.offset = 0
	m.selected = 0
	m.rebuildRelease()
	return m.start(releaseRequest{op: "list", cursor: m.cursors[index]})
}
func (m releaseModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch x := msg.(type) {
	case releaseStart:
		return m.refresh()
	case collectionClockMsg:
		return m, collectionClock()
	case tea.WindowSizeMsg:
		m.width = max(1, x.Width)
		m.height = max(1, x.Height)
		m.rebuildRelease()
	case releaseResult:
		if x.serial != m.serial {
			return m, nil
		}
		m.cancel = nil
		m.busy = ""
		if x.err == nil {
			x.err = m.validateRelease(x)
		}
		if x.err != nil {
			if x.op == "access" || accessDenied(x.err) || errors.Is(x.err, errResponseScope) {
				m.serial++
				m.clear()
				m.access.ready = false
				m.access.generation++
				m.status = "Access unavailable: r rechecks fixed access and inspection. " + x.err.Error()
				return m, nil
			}
			m.status = "Unavailable: " + x.err.Error()
			if releaseWrite(x.op) {
				m.blocked = true
				if x.op == "create" {
					m.uncertain = true
					m.status = "Write not confirmed. Exact input and key retained; s retries explicitly. " + x.err.Error()
				} else {
					m.status = "Write not confirmed. Press r to inspect shared facts before another command. " + x.err.Error()
				}
			}
			if x.op == "list" || x.op == "get" {
				m.rows = nil
				m.record = nil
				m.presentation = nil
				m.artifact = nil
				m.blocked = true
				m.rebuildRelease()
			}
			return m, nil
		}
		if x.op == "access" {
			access, err := m.access.inspect(x.session, x.repositories)
			if err != nil {
				m.clear()
				m.access.ready = false
				m.access.restartRequired = errors.Is(err, errPrincipalChanged)
				m.status = "Access unavailable: " + err.Error()
				return m, nil
			}
			m.access = access
			m.caps = x.caps
			m.profiles = x.profiles
			m.tracker = x.tracker
			if x.id != "" {
				return m.start(releaseRequest{op: "get", id: x.id})
			}
			return m.list(0)
		}
		switch x.op {
		case "file":
			d := x.value.(reviewinput.Draft)
			m.draft = &d
			m.presentation = nil
			m.artifact = nil
			m.uncertain = false
			m.status = "Request preview loaded. Inspect the complete input and key; s confirms."
		case "list":
			m.setRows(x.value)
			m.blocked = false
			m.status = "Shared " + m.view + " loaded. Missing evidence is not passing."
		case "task-artifact":
			m.presentation = x.value
			m.status = "Exact task artifact inspected. Failed checks and design reports remain evidence, not publication authority."
		case "graph-artifact":
			m.presentation = x.value
			m.status = "Graph source artifact inspected. Coverage and missing source remain explicit."
		case "artifact":
			a := x.value.(domain.DeliveryArtifact)
			m.artifact = &a
			m.presentation = a
			m.status = "Exact artifact inspected. Patch and check evidence are retained separately from provider observations."
		case "query":
			m.presentation = x.value
			m.status = "Graph query inspected. Truncation and source gaps remain explicit."
		case "sync":
			m.presentation = x.value
			m.status = "Tracker sync inspected. Ticket state cannot grant design approval or prove a merge."
		default:
			if x.op == "create" && x.draft.Kind == "tracker-sync" {
				m.presentation = x.value
				m.blocked = true
				m.status = "Tracker sync requested. Press r before another write; background completion is separate."
			} else {
				m.record = x.value
				m.presentation = nil
				m.artifact = nil
				m.inspectID, _ = m.recordIdentity()
				m.next = ""
				m.blocked = false
				m.status = "Shared record inspected. Review its exact digest before an action."
			}
			m.draft = nil
			m.uncertain = false
			if x.op == "create" && x.draft.Kind != "tracker-sync" {
				m.status = "Proposal recorded. Inspect the returned facts; proposal acceptance is not execution or publication."
			}
			if x.op == "create" && x.draft.Kind == "delivery-reconcile" {
				m.status = "Reconciliation requested. Existing provider evidence remains a recorded observation."
			}
			if x.op == "authorize" {
				m.status = "Authorization recorded for the inspected digest. Execution or publication is not confirmed."
			}
			if x.op == "cancel" {
				m.status = "Cancellation requested. Stopping and resource cleanup still require evidence."
			}
		}
		m.offset = 0
		m.rebuildRelease()
	case tea.KeyMsg:
		key := x.String()
		if key == "ctrl+c" || (key == "q" && m.prompt == "") {
			if m.cancel != nil {
				m.cancel()
			}
			return m, tea.Quit
		}
		if m.busy != "" {
			if key == "esc" {
				m.cancel()
				m.cancel = nil
				m.serial++
				if releaseWrite(m.busy) {
					m.blocked = true
					m.uncertain = m.busy == "create"
					m.status = "Write outcome unknown. Use the retained exact input/key or r to inspect."
				} else {
					m.status = "Read cancelled; r renews inspection."
				}
				m.busy = ""
			}
			return m, nil
		}
		if m.prompt != "" {
			return m.releasePrompt(x)
		}
		if m.small() {
			return m, nil
		}
		if key == "1" || key == "2" || key == "3" || key == "4" {
			if m.draft != nil {
				m.status = "Save or discard the preview before changing views."
				return m, nil
			}
			m.view = map[string]string{"1": "graphs", "2": "runs", "3": "deliveries", "4": "tracker"}[key]
			m.inspectID = ""
			return m.refresh()
		}
		if key == "r" {
			return m.refresh()
		}
		if !m.access.ready {
			return m, nil
		}
		switch key {
		case "c", "u":
			if m.draft != nil {
				m.status = "Save or discard the existing preview first."
				break
			}
			if !m.canCreate() {
				m.status = "Action blocked: author or tracker synchronization permission is required."
				break
			}
			kind := map[string]string{"graphs": "graph", "runs": "run", "deliveries": "delivery", "tracker": "tracker-link"}[m.view]
			if key == "u" {
				if m.record == nil {
					m.status = "Inspect a record first."
					break
				}
				if m.view == "tracker" {
					kind = "tracker-sync"
				} else if m.view == "deliveries" {
					kind = "delivery-reconcile"
				} else {
					break
				}
			}
			m.prompt = "file"
			m.input = ""
			m.pending = releaseRequest{kind: kind}
		case "s":
			if m.draft != nil {
				return m.confirmRelease("create")
			}
		case "a":
			return m.confirmRelease("authorize")
		case "x":
			if m.view == "runs" {
				return m.confirmRelease("cancel")
			}
		case "v":
			if m.view == "runs" && m.record != nil && m.draft == nil {
				m.prompt = "task-artifact"
				m.input = ""
				m.status = "Select a task key from the inspected retained receipts."
			}
			if m.view == "deliveries" && m.record != nil && m.draft == nil {
				id, d := m.recordIdentity()
				return m.start(releaseRequest{op: "artifact", id: id, digest: d})
			}
		case "f":
			if m.view == "graphs" && m.record != nil && m.draft == nil {
				m.prompt = "source-repository"
				m.input = ""
			}
		case "e":
			if m.view == "runs" {
				m.presentation = m.profiles
				m.offset = 0
				m.rebuildRelease()
				m.status = "Reviewed operator catalog; truncated discovery cannot invent a missing profile."
			} else if m.view == "tracker" {
				m.presentation = m.tracker
				m.offset = 0
				m.rebuildRelease()
			}
		case "/", "t":
			if m.view == "graphs" && m.record != nil && m.draft == nil {
				m.prompt = "search"
				if key == "t" {
					m.prompt = "node"
				}
				m.input = ""
			}
		case "o":
			if m.draft == nil {
				m.prompt = "open"
				m.input = ""
			}
		case "i":
			if link, ok := m.record.(domain.TrackerLink); ok && link.LatestSyncID != "" {
				return m.start(releaseRequest{op: "sync", id: link.LatestSyncID})
			}
		case "b":
			if m.draft == nil {
				return m.list(0)
			}
		case "enter":
			if m.record == nil && m.draft == nil && len(m.rows) > 0 {
				return m.start(releaseRequest{op: "get", id: m.rows[m.selected].ID})
			}
		case "n":
			if m.record == nil && m.draft == nil && m.next != "" {
				if len(m.cursors) >= 1000 {
					m.status = "Page limit reached; r restarts discovery."
					break
				}
				m.cursors = append(m.cursors[:m.pageIndex+1], m.next)
				return m.list(m.pageIndex + 1)
			}
		case "p":
			if m.record == nil && m.draft == nil && m.pageIndex > 0 {
				return m.list(m.pageIndex - 1)
			}
		case "esc":
			if m.draft != nil {
				m.draft = nil
				m.status = "Preview discarded. A prior uncertain request may already have committed; retain its original file and key."
			} else {
				m.presentation = nil
			}
			m.offset = 0
			m.rebuildRelease()
		case "down", "j":
			m.releaseMove(1)
		case "up", "k":
			m.releaseMove(-1)
		case "pgdown", " ":
			m.releaseMove(m.bodyRows())
		case "pgup":
			m.releaseMove(-m.bodyRows())
		case "home":
			m.releaseMove(-len(m.lines) - len(m.rows))
		case "end":
			m.releaseMove(len(m.lines) + len(m.rows))
		}
	}
	return m, nil
}
func (m releaseModel) canCreate() bool {
	if !m.access.ready {
		return false
	}
	if m.view == "tracker" {
		return m.tracker.Enabled && m.tracker.CanSync
	}
	r := m.access.repository
	return domain.ValidateRepositoryAction(m.access.principal, r.CanRead, r.CanAuthor, r.CanApprove, "author") == nil
}
func (m releaseModel) allowed(req releaseRequest) error {
	if !m.access.ready {
		return errors.New("confirmed access required")
	}
	if req.op == "create" {
		if !m.canCreate() || m.draft == nil || req.draft.Digest != m.draft.Digest || req.draft.IdempotencyKey != m.draft.IdempotencyKey {
			return errors.New("inspect a complete request preview first")
		}
		if (req.draft.Kind == "tracker-sync" || req.draft.Kind == "delivery-reconcile") && m.blocked && !m.uncertain {
			return errors.New("refresh and inspect the existing record before another request")
		}
		if req.draft.Kind == "tracker-sync" {
			link, ok := m.record.(domain.TrackerLink)
			var in domain.TrackerSyncInput
			_ = json.Unmarshal(req.draft.Input, &in)
			if !ok || in.LinkDigest != link.Digest || req.id != link.ID {
				return errors.New("sync input must match the inspected link")
			}
			if !m.uncertain && link.LatestSyncID != "" && (link.Observation == nil || link.Observation.SyncID != link.LatestSyncID) {
				return errors.New("the latest synchronization has no retained outcome; refresh its observation first")
			}
			if in.Mode != "refresh" && (link.Observation == nil || !link.Observation.Current || link.Observation.Issue == nil || in.ExpectedProjectionDigest != link.Observation.ProjectionDigest) {
				return errors.New("inspect the current tracker projection digest before publishing or restoring")
			}
			if in.Mode == "restore" && !m.tracker.CanResolve {
				return errors.New("conflict resolution permission required")
			}
		}
		if req.draft.Kind == "delivery-reconcile" {
			delivery, ok := m.record.(domain.Delivery)
			if !ok || delivery.Observation == nil {
				return errors.New("inspect a committed provider observation before reconciliation")
			}
			id, d := m.recordIdentity()
			var in struct {
				Digest string `json:"digest"`
			}
			_ = json.Unmarshal(req.draft.Input, &in)
			if id == "" || req.id != id || in.Digest != d {
				return errors.New("reconciliation must bind the inspected delivery")
			}
		}
		return nil
	}
	if m.blocked || m.record == nil || m.draft != nil {
		return errors.New("refresh and inspect the record before another command")
	}
	id, d := m.recordIdentity()
	if req.id != id || req.digest != d {
		return errors.New("confirmation must match displayed identity and digest")
	}
	if m.access.principal.Kind != "human" {
		return errors.New("only a human can grant execution or publication authority")
	}
	switch m.view {
	case "runs":
		if !m.caps.CanExecute {
			return errors.New("execution permission required")
		}
		run := m.record.(domain.CoordinationRun)
		if req.op == "authorize" {
			if run.Authorization != nil || run.CancelRequestedAt != nil {
				return errors.New("run already authorized or cancelled")
			}
			for _, r := range run.Plan.Repositories {
				if r.FullSourceDigest == "" {
					return errors.New("whole-source pins are required")
				}
			}
			for _, task := range run.Plan.Tasks {
				found := false
				for _, p := range m.profiles.Profiles {
					if p.ID == task.Profile && p.ProfileDigest == task.ProfileDigest && p.Image == task.Image {
						found = true
					}
				}
				if !found {
					return errors.New("inspected catalog does not contain every exact task profile/image")
				}
			}
		}
		if req.op == "cancel" && run.CancelRequestedAt != nil {
			return errors.New("cancellation already requested")
		}
	case "deliveries":
		if req.op != "authorize" || !m.caps.CanPublish {
			return errors.New("human publication permission required")
		}
		d := m.record.(domain.Delivery)
		if d.Authorization != nil {
			return errors.New("publication already authorized")
		}
		if m.artifact == nil || m.artifact.DeliveryDigest != d.Digest {
			return errors.New("press v and inspect the exact patch/check artifact before authorization")
		}
	default:
		return errors.New("this view has no execution or publication authorization")
	}
	return nil
}
func (m releaseModel) confirmRelease(op string) (tea.Model, tea.Cmd) {
	id, d := m.recordIdentity()
	req := releaseRequest{op: op, id: id, digest: d, generation: m.access.generation, view: m.view}
	if op == "create" && m.draft != nil {
		req.draft = *m.draft
		req.draft.Input = append(json.RawMessage(nil), m.draft.Input...)
	}
	if err := m.allowed(req); err != nil {
		m.status = "Action blocked: " + err.Error()
		return m, nil
	}
	m.pending = req
	m.input = ""
	m.prompt = op
	if op == "create" {
		m.prompt = map[string]string{"graph": "create-graph", "run": "propose-run", "delivery": "propose-delivery", "tracker-link": "link-ticket", "tracker-sync": "sync-ticket", "delivery-reconcile": "reconcile-delivery"}[req.draft.Kind]
	} else if op == "authorize" {
		m.prompt = "authorize-run"
		if m.view == "deliveries" {
			m.prompt = "authorize-publication"
		}
	} else {
		m.prompt = "cancel-run"
	}
	return m, nil
}
func (m releaseModel) releasePrompt(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.prompt = ""
		m.input = ""
		m.pending = releaseRequest{}
		m.status = "Confirmation cancelled; no command sent."
	case "enter":
		if m.small() {
			return m, nil
		}
		switch m.prompt {
		case "file":
			return m.start(releaseRequest{op: "file", path: m.input, kind: m.pending.kind})
		case "open":
			if domain.IsLowerHex(m.input, 32) {
				return m.start(releaseRequest{op: "get", id: m.input})
			}
			m.status = "Record ID must be 32 lowercase hexadecimal characters."
		case "task-artifact":
			run, ok := m.record.(domain.CoordinationRun)
			if !ok {
				break
			}
			for _, receipt := range run.Receipts {
				if receipt.TaskKey == m.input || receipt.TaskID == m.input {
					if receipt.ArtifactDigest == "" {
						m.status = "This receipt has no retained artifact."
						return m, nil
					}
					return m.start(releaseRequest{op: "task-artifact", id: run.ID, taskArtifact: domain.CoordinationArtifactQuery{RunDigest: run.Digest, TaskID: receipt.TaskID, ArtifactDigest: receipt.ArtifactDigest}})
				}
			}
			m.status = "Select an exact task key or opaque ID from the inspected receipts."
		case "source-repository":
			g, ok := m.record.(domain.RepositoryGraph)
			if !ok {
				break
			}
			found := false
			for _, s := range g.Snapshot.Sources {
				if s.RepositoryID == m.input {
					m.pending = releaseRequest{op: "graph-artifact", id: g.ID, graphArtifact: domain.GraphArtifactQuery{GraphDigest: g.Digest, RepositoryID: s.RepositoryID, CollectionID: s.CollectionID, ReceiptDigest: s.Digest, FullSourceDigest: s.FullSourceDigest}}
					found = true
					break
				}
			}
			if !found {
				m.status = "Select an exact repository ID from the inspected graph sources."
				break
			}
			m.prompt = "source-path"
			m.input = ""
		case "source-path":
			req := m.pending
			req.graphArtifact.Path = m.input
			if domain.ValidateGraphArtifactQuery(req.graphArtifact) != nil {
				m.status = "Source path must be a clean explicit relative file path."
				break
			}
			return m.start(req)
		case "search", "node":
			q := domain.GraphQuery{Limit: 20, Depth: 1}
			if m.prompt == "search" {
				q.Search = m.input
			} else {
				q.NodeID = m.input
			}
			if domain.ValidateGraphQuery(q) != nil {
				m.status = "Invalid bounded graph query."
				break
			}
			id, d := m.recordIdentity()
			return m.start(releaseRequest{op: "query", id: id, digest: d, query: q})
		default:
			if m.input == m.prompt {
				return m.start(m.pending)
			}
			m.status = "Type the displayed action word exactly."
		}
	case "backspace", "ctrl+h":
		r := []rune(m.input)
		if len(r) > 0 {
			m.input = string(r[:len(r)-1])
		}
	case "ctrl+u":
		m.input = ""
	default:
		if key.Type == tea.KeyRunes {
			for _, r := range key.Runes {
				if !unicode.IsControl(r) && !unicode.Is(unicode.Cf, r) && len(m.input)+len(string(r)) <= 4096 {
					m.input += string(r)
				}
			}
		}
	}
	return m, nil
}
func (m releaseModel) recordIdentity() (string, string) {
	switch r := m.record.(type) {
	case domain.RepositoryGraph:
		return r.ID, r.Digest
	case domain.CoordinationRun:
		return r.ID, r.Digest
	case domain.Delivery:
		return r.ID, r.Digest
	case domain.TrackerLink:
		return r.ID, r.Digest
	}
	return "", ""
}
func (m *releaseModel) setRows(value any) {
	m.rows = nil
	m.next = ""
	m.pageTruncated = false
	switch p := value.(type) {
	case domain.RepositoryGraphPage:
		for _, r := range p.Graphs {
			m.rows = append(m.rows, releaseRow{r.ID, r.Digest, "immutable graph"})
		}
		m.next = p.NextBefore
	case domain.CoordinationPage:
		for _, r := range p.Runs {
			m.rows = append(m.rows, releaseRow{r.ID, r.Digest, fmt.Sprintf("authorized=%t tasks=%d receipts=%d", r.Authorized, r.Tasks, r.Receipts)})
		}
		m.next = p.NextBefore
	case domain.DeliveryPage:
		for _, r := range p.Deliveries {
			m.rows = append(m.rows, releaseRow{r.ID, r.Digest, "provider delivery"})
		}
		m.next = p.NextBefore
	case domain.TrackerLinkPage:
		for _, r := range p.Links {
			m.rows = append(m.rows, releaseRow{r.ID, r.Digest, "ticket " + r.Input.IssueID})
		}
		m.next = p.Next
		m.pageTruncated = p.Truncated
	}
}
func (m *releaseModel) releaseMove(delta int) {
	if m.record == nil && m.draft == nil && m.presentation == nil {
		m.selected = max(0, min(len(m.rows)-1, m.selected+delta))
	} else {
		m.offset = max(0, min(max(0, len(m.lines)-m.bodyRows()), m.offset+delta))
	}
}
