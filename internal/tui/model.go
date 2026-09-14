package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/pkg/client"
)

type model struct {
	options                         Options
	access                          accessState
	inspectID                       string
	execute                         executor
	cancel                          context.CancelFunc
	serial                          int
	busy                            string
	status                          string
	blocked                         bool
	width, height, selected, offset int
	page                            domain.ChangePage
	// Cursors are bounded to visited pages; the server owns opaque cursor meaning.
	cursors         []string
	pageIndex       int
	pack            *domain.Package
	draft           domain.Content
	draftOp         string
	draftGuided     bool
	editor          *designEditor
	rawJSON         bool
	recovery        *designRecovery
	activeDraft     *designRecovery
	recoveryPreview bool
	lines           []string
	prompt, input   string
	pending         request
	collectionMode  bool
	collections     collectionState
	assistanceMode  bool
	assistance      assistanceState
}

func newModel(options Options, execute executor) model {
	return model{options: options, execute: execute, width: 80, height: 24,
		cursors: []string{""}, collections: collectionState{cursors: []string{""}}, status: "Loading shared work..."}
}

func (m model) Init() tea.Cmd {
	// Starting via Update gives every operation a serial and cancel function,
	// including the initial read, so a late response can never replace inspection.
	return tea.Batch(func() tea.Msg { return startMsg{} }, collectionClock())
}

type startMsg struct{}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case collectionClockMsg:
		return m, collectionClock()
	case startMsg:
		if m.access.authenticated {
			return m.recheckAccess(m.options.ID)
		}
		if m.options.ID != "" {
			return m.start(request{op: "open", id: m.options.ID})
		}
		return m.list(0)
	case tea.WindowSizeMsg:
		m.width, m.height = max(1, msg.Width), max(1, msg.Height)
		m.rebuild()
	case result:
		if msg.serial != m.serial {
			return m, nil
		}
		if msg.err == nil {
			msg.err = m.validateScope(msg)
		}
		if msg.err == nil && isMutation(msg.op) {
			msg.err = validateMutationResult(msg.request, msg.pack, m.actor())
		}
		m.cancel = nil
		m.busy = ""
		if msg.err != nil {
			if m.access.authenticated && (msg.op == "access" || accessDenied(msg.err) || (isAssistanceOperation(msg.op) && assistanceNotFound(msg.err)) || errors.Is(msg.err, errResponseScope)) {
				retained := m.recovery
				assistanceRecovery := m.assistance.uncertain
				if isCollectionOperation(msg.op) || msg.collectionView {
					m.collections.inspectID = msg.id
				} else if !isAssistanceOperation(msg.op) {
					m.inspectID = msg.id
				}
				m.invalidateAccess()
				if msg.op == "access" && !accessDenied(msg.err) && !errors.Is(msg.err, errResponseScope) {
					// A failed transport cannot establish a new identity. Keep the
					// recovery input hidden until the same principal is verified.
					m.recovery = retained
					m.assistance.uncertain = assistanceRecovery
				}
				m.status = "Access unavailable: press r to recheck access and inspect shared state. " + msg.err.Error()
				return m, nil
			}
			m.status = "Unavailable: " + msg.err.Error()
			if isAssistanceOperation(msg.op) {
				return m.assistanceResult(msg)
			}
			if isCollectionOperation(msg.op) {
				return m.collectionResult(msg)
			}
			if isMutation(msg.op) {
				if m.activeDraft != nil {
					m.recovery = m.activeDraft
					m.activeDraft = nil
				}
				m.blocked = true
				var apiErr *client.APIError
				if errors.As(msg.err, &apiErr) && apiErr.StatusCode == 409 {
					m.status = "Stale inspection: press r to load and inspect the latest revision. " + msg.err.Error()
				} else {
					m.status = "Write not confirmed. Inspect shared state with r before another write; do not assume it failed. " + msg.err.Error()
				}
			} else if m.access.authenticated && (msg.op == "open" || msg.op == "list") {
				m.clearInspection()
			}
			return m, nil
		}
		switch msg.op {
		case "access":
			access, err := m.access.inspect(msg.session, msg.repositories)
			if err != nil {
				m.invalidateAccess()
				m.access.restartRequired = errors.Is(err, errPrincipalChanged)
				m.status = "Access unavailable: press r to recheck access. " + err.Error()
				if m.access.restartRequired {
					m.status = "Access unavailable: " + err.Error()
				}
				return m, nil
			}
			m.access = access
			if msg.assistanceView {
				return m.start(request{op: "open", id: msg.id, assistanceView: true, assistanceID: msg.assistanceID})
			}
			if msg.collectionView {
				if msg.id != "" {
					return m.start(request{op: "collection", id: msg.id})
				}
				return m.listCollections(0)
			}
			if msg.id != "" {
				return m.start(request{op: "open", id: msg.id})
			}
			return m.list(0)
		case "list":
			m.page, m.selected = msg.page, 0
			m.pack, m.draft = nil, nil
			m.editor, m.rawJSON = nil, false
			m.draftGuided = false
			m.recoveryPreview = false
			m.blocked = false
			m.inspectID = ""
			m.status = "Shared work loaded. Repository labels are discovery filters only."
		case "collections", "collection", "collection-file", "collect", "cancel-collection":
			return m.collectionResult(msg)
		case "assist-list", "assist-get", "assist-request", "assist-apply":
			return m.assistanceResult(msg)
		case "file":
			m.editor, m.rawJSON = nil, false
			m.draftGuided = false
			m.recoveryPreview = false
			m.draft = msg.content
			if m.draftOp == "create" {
				m.pack = nil
			}
			m.status = "File loaded for preview. This is the complete replacement content; s confirms saving."
		default:
			m.pack = &msg.pack
			m.editor, m.rawJSON = nil, false
			m.draftGuided = false
			m.recoveryPreview = false
			if msg.op == "create" || msg.op == "revise" {
				m.recovery, m.activeDraft = nil, nil
			}
			if msg.op == "attach" {
				m.collectionMode = false
			}
			m.inspectID = msg.pack.ID
			m.draft = nil
			m.blocked = false
			m.status = "Loaded latest revision. Inspect the complete content before an action."
			if msg.op == "open" && msg.assistanceView {
				m.assistanceMode = true
				if msg.assistanceID != "" {
					return m.start(request{op: "assist-get", id: msg.id, assistanceID: msg.assistanceID})
				}
				return m.listAssistance(0)
			}
			if msg.op == "approve" {
				m.status = "Design approval recorded. Implementation verification, merge and deployment are separate."
			} else if isMutation(msg.op) {
				m.status = msg.op + " recorded. Inspect the returned revision before another action."
			}
		}
		if m.recovery != nil && (msg.op == "list" || msg.op == "open") {
			m.status = "Saved work loaded. Inspect it, then v compares retained design input; no write was retried."
		}
		m.offset = 0
		m.rebuild()
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC || (msg.String() == "q" && m.prompt == "" && m.editor == nil && !m.assistanceTyping()) {
			if m.cancel != nil {
				m.cancel()
			}
			return m, tea.Quit
		}
		if m.busy != "" {
			if msg.String() == "esc" {
				m.cancel()
				m.cancel = nil
				m.serial++
				if isAssistanceOperation(m.busy) {
					m.assistanceCancelled(m.busy)
				} else if isCollectionOperation(m.busy) {
					m.collectionCancelled(m.busy)
				} else if isMutation(m.busy) {
					if m.activeDraft != nil {
						m.recovery = m.activeDraft
						m.activeDraft = nil
					}
					m.blocked = true
					m.status = "Write cancelled; outcome unknown. Press r to inspect shared state before another write."
				} else {
					m.status = "Read cancelled. Press r to retry inspection."
				}
				m.busy = ""
			}
			return m, nil
		}
		if m.prompt != "" {
			return m.updatePrompt(msg)
		}
		if m.tooSmall() {
			return m, nil
		}
		if m.access.authenticated && !m.access.ready {
			if msg.String() == "r" {
				if m.collectionMode {
					return m.recheckCollectionAccess()
				}
				return m.recheckAccess(m.inspectID)
			}
			return m, nil
		}
		if m.editor != nil {
			return m.designKey(msg)
		}
		if m.assistanceMode {
			return m.assistanceKey(msg)
		}
		if msg.String() == "h" && !m.collectionMode {
			return m.openAssistance()
		}
		if msg.String() == "g" {
			if !m.access.authenticated {
				m.status = "Action blocked: context collection requires authenticated workspace and repository access."
				return m, nil
			}
			if m.draft != nil {
				m.status = "Save or discard the package preview before switching views."
				return m, nil
			}
			m.collectionMode = !m.collectionMode
			m.offset = 0
			m.rebuild()
			if m.collectionMode && m.collections.record == nil && m.collections.draft == nil {
				return m.listCollections(0)
			}
			m.status = "View changed. Inspect the displayed facts before an action."
			return m, nil
		}
		if m.collectionMode {
			return m.collectionKey(msg)
		}
		switch msg.String() {
		case "o":
			m.prompt, m.input = "open", ""
		case "c":
			return m.beginDesign("create")
		case "e":
			return m.beginDesign("revise")
		case "i":
			op := "create"
			if m.pack != nil {
				op = "revise"
			}
			if err := m.actionAllowed(op); err != nil {
				m.status = "Action blocked: " + err.Error()
				break
			}
			if m.draft != nil {
				m.status = "Save or discard the current preview before loading another file."
				break
			}
			if m.blocked {
				m.status = "Blocked: refresh shared state with r before another write."
				break
			}
			m.prompt, m.input, m.draftOp = "file", m.options.File, op
		case "J":
			m.rawJSON = !m.rawJSON
			m.offset = 0
			m.rebuild()
		case "v":
			return m.recoverDesign()
		case "b":
			return m.list(0)
		case "r":
			if m.access.authenticated {
				return m.recheckAccess(m.inspectID)
			}
			if m.pack != nil {
				return m.start(request{op: "open", id: m.pack.ID})
			}
			return m.list(0)
		case "s":
			if m.blocked {
				m.status = "Blocked: refresh and inspect with r before another write."
				break
			}
			if m.draft != nil {
				return m.confirm(m.draftOp)
			}
			m.status = "No draft preview to save. e edits the design; u submits a saved revision."
		case "u":
			if m.blocked {
				m.status = "Blocked: refresh and inspect with r before another write."
				break
			}
			if m.draft != nil {
				m.status = "Save or discard the preview before submitting a saved revision."
				break
			}
			if m.pack == nil {
				break
			}
			if m.pack.Revision.SubmittedAt != nil {
				m.status = "This revision is already submitted."
				break
			}
			return m.confirm("submit")
		case "a":
			if m.pack == nil || m.draft != nil {
				break
			}
			if m.blocked {
				m.status = "Blocked: refresh and inspect with r before approval."
				break
			}
			if err := domain.ValidateApproval(m.pack.Revision, m.pack.Revision.Number, m.pack.Revision.Digest, m.actor()); err != nil {
				m.status = "Approval blocked: " + err.Error()
				break
			}
			if m.pack.Approved {
				m.status = "This revision already has effective design approval."
				break
			}
			return m.confirm("approve")
		case "esc":
			if m.draft != nil {
				m.draft = nil
				m.draftGuided = false
				m.recoveryPreview = false
				m.offset = 0
				m.rebuild()
				m.status = "Draft preview discarded; no write sent."
			}
		case "n":
			if m.pack == nil && m.draft == nil && m.page.NextBefore != "" {
				if len(m.cursors) >= 1000 {
					m.status = "Page navigation limit reached; r restarts discovery."
					break
				}
				m.cursors = append(m.cursors[:m.pageIndex+1], m.page.NextBefore)
				return m.list(m.pageIndex + 1)
			}
		case "p":
			if m.pack == nil && m.draft == nil && m.pageIndex > 0 {
				return m.list(m.pageIndex - 1)
			}
		case "enter":
			if m.pack == nil && m.draft == nil && len(m.page.Changes) > 0 {
				return m.start(request{op: "open", id: m.page.Changes[m.selected].ID})
			}
		case "down", "j":
			m.move(1)
		case "up", "k":
			m.move(-1)
		case "pgdown", " ":
			m.move(m.bodyHeight())
		case "pgup":
			m.move(-m.bodyHeight())
		case "home":
			m.move(-len(m.lines) - len(m.page.Changes))
		case "end":
			m.move(len(m.lines) + len(m.page.Changes))
		}
	}
	return m, nil
}

func isMutation(op string) bool {
	return op == "create" || op == "revise" || op == "submit" || op == "approve" || op == "attach"
}

// A command can commit and then read a concurrently edited package. Treat that
// response as uncertain instead of claiming the newly returned revision received
// the approval or submission the user sent for their inspected tuple.
func validateMutationResult(req request, p domain.Package, actor string) error {
	wantRevision, wantDigest := req.revision, req.digest
	if req.op == "create" || req.op == "revise" || req.op == "attach" {
		var err error
		wantDigest, err = domain.Digest(req.content)
		if err != nil {
			return err
		}
		wantRevision++
		if req.op == "create" {
			wantRevision = 1
		}
	}
	if p.ID == "" || (req.op != "create" && p.ID != req.id) || p.Revision.Number != wantRevision || p.Revision.Digest != wantDigest {
		return errors.New("command response does not match the confirmed revision and content")
	}
	if req.op == "attach" && (p.Approved || p.Approval != nil || p.Revision.SubmittedAt != nil || p.Revision.Author != actor) {
		return errors.New("attachment response does not confirm a new unapproved draft by the authenticated author")
	}
	if req.op == "approve" && (!p.Approved || p.Approval == nil || p.Approval.Revision != req.revision || p.Approval.Digest != req.digest) {
		return errors.New("command response does not confirm the inspected design approval")
	}
	if req.op == "submit" && p.Revision.SubmittedAt == nil {
		return errors.New("command response does not confirm submission")
	}
	return nil
}

func (m model) start(req request) (tea.Model, tea.Cmd) {
	if m.access.authenticated && req.op != "access" && !m.access.ready {
		m.status = "Access unavailable: press r to recheck access."
		return m, nil
	}
	if isMutation(req.op) || isCollectionWrite(req.op) || isAssistanceWrite(req.op) {
		if err := m.requestActionAllowed(req.op); err != nil {
			m.prompt, m.input, m.pending = "", "", request{}
			m.status = "Action blocked: " + err.Error()
			return m, nil
		}
		if m.access.authenticated && req.accessGeneration != m.access.generation {
			m.prompt, m.input, m.pending = "", "", request{}
			m.status = "Action blocked: access changed; inspect and confirm again."
			return m, nil
		}
	}
	if m.cancel != nil {
		m.cancel()
	}
	m.serial++
	req.serial = m.serial
	m.busy = req.op
	m.activeDraft = nil
	if req.op == "create" || req.op == "revise" {
		m.activeDraft = &designRecovery{content: req.content, op: req.op, id: req.id, revision: req.revision, digest: req.digest, guided: m.draftGuided}
	}
	m.prompt, m.input = "", ""
	m.pending = request{}
	if isAssistanceWrite(req.op) {
		m.assistance.active = m.assistance.preview
	}
	cmd, cancel := m.execute(req)
	m.cancel = cancel
	return m, cmd
}

func (m model) list(index int) (tea.Model, tea.Cmd) {
	if index == 0 {
		m.cursors = []string{""}
	}
	m.pageIndex = index
	// Do not leave a previous page navigable if loading the selected cursor fails.
	// Its next cursor would otherwise be combined with the wrong page position.
	m.page = domain.ChangePage{}
	return m.start(request{op: "list", cursor: m.cursors[index], repository: m.options.Repository})
}

func (m model) confirm(op string) (tea.Model, tea.Cmd) {
	if err := m.actionAllowed(op); err != nil {
		m.status = "Action blocked: " + err.Error()
		return m, nil
	}
	if op == "create" || op == "revise" {
		revision := int64(0)
		if op == "revise" {
			revision = m.pack.Revision.Number
		}
		if err := boundedDesign(m.draft, revision); err != nil {
			m.status = "Cannot save: " + err.Error() + ". e edits the retained preview."
			return m, nil
		}
		if op == "revise" {
			digest, _ := domain.Digest(m.draft)
			current, _ := domain.Digest(m.pack.Revision.Content)
			if digest == current {
				m.status = "This design is already saved; no changes to save. e edits the preview."
				return m, nil
			}
		}
	}
	m.pending = request{op: op, content: m.draft, accessGeneration: m.access.generation}
	if m.pack != nil {
		m.pending.id, m.pending.revision, m.pending.digest = m.pack.ID, m.pack.Revision.Number, m.pack.Revision.Digest
	}
	m.prompt, m.input = op, ""
	return m, nil
}

func (m model) updatePrompt(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.prompt, m.input, m.pending = "", "", request{}
		m.status = "Cancelled; no write sent."
	case "enter":
		if m.tooSmall() {
			return m, nil
		}
		switch m.prompt {
		case "open":
			if strings.TrimSpace(m.input) != "" {
				return m.start(request{op: "open", id: m.input})
			}
		case "collection-open":
			if domain.IsLowerHex(m.input, 32) {
				return m.start(request{op: "collection", id: m.input})
			}
			m.status = "Collection ID must be 32 lowercase hexadecimal characters."
		case "collection-file":
			if m.input != "" {
				return m.start(request{op: "collection-file", path: m.input})
			}
		case "file":
			if m.input != "" {
				return m.start(request{op: "file", path: m.input})
			}
		default:
			if m.input == m.prompt {
				return m.start(m.pending)
			}
			m.status = "Confirmation did not match; type the action name exactly or Esc to cancel."
		}
	case "backspace", "ctrl+h":
		runes := []rune(m.input)
		if len(runes) > 0 {
			m.input = string(runes[:len(runes)-1])
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

func (m *model) move(delta int) {
	if m.assistanceMode {
		if m.assistance.record == nil && m.assistance.form == nil && m.assistance.preview == nil {
			m.assistance.selected = max(0, min(len(m.assistance.page.Requests)-1, m.assistance.selected+delta))
		} else {
			m.offset = max(0, min(max(0, len(m.lines)-m.bodyHeight()), m.offset+delta))
		}
		return
	}
	if m.collectionMode {
		if m.collections.record == nil && m.collections.draft == nil {
			m.collections.selected = max(0, min(len(m.collections.page.Collections)-1, m.collections.selected+delta))
			return
		}
		m.offset = max(0, min(max(0, len(m.lines)-m.bodyHeight()), m.offset+delta))
		return
	}
	if m.pack == nil && m.draft == nil {
		m.selected = max(0, min(len(m.page.Changes)-1, m.selected+delta))
		return
	}
	m.offset = max(0, min(max(0, len(m.lines)-m.bodyHeight()), m.offset+delta))
}

func (m model) bodyHeight() int { return max(1, m.height-len(m.header())-5) }

func (m model) header() []string {
	lines := wrap("Conductor | local actor: "+safe(m.options.Actor)+" (not authentication)", m.width)
	if m.access.authenticated {
		identity := "checking server identity"
		if m.access.principal.ID != "" {
			identity = "principal: " + safe(m.access.principal.ID) + " (" + safe(m.access.principal.Kind) + ")"
		}
		lines = wrap("Conductor | "+identity, m.width)
		lines = append(lines, wrap("Workspace: "+safe(m.access.workspaceID)+" | Repository ID: "+safe(m.access.repositoryID), m.width)...)
		permissions := "Access not confirmed; r rechecks access, q quits."
		if m.access.ready {
			author, approve := "not granted", "not granted"
			r := m.access.repository
			if domain.ValidateRepositoryAction(m.access.principal, r.CanRead, r.CanAuthor, r.CanApprove, "author") == nil {
				author = "allowed"
			}
			if domain.ValidateRepositoryAction(m.access.principal, r.CanRead, r.CanAuthor, r.CanApprove, "approve") == nil {
				approve = "allowed after independent inspection"
			}
			permissions = "Read: allowed | Author: " + author + " | Approve: " + approve
		}
		lines = append(lines, wrap(permissions, m.width)...)
		if m.access.truncated {
			lines = append(lines, wrap("Discovery truncated: selected scope verified; additional entries are not shown.", m.width)...)
		}
	}
	if m.pack != nil && (!m.collectionMode || m.prompt == "attach" || m.busy == "attach") {
		p := m.pack
		revisionLabel, digestLabel, stateLabel, authorLabel := "revision", "Digest", "State", "author"
		if m.draft != nil || m.editor != nil {
			revisionLabel, digestLabel = "base revision", "Base digest"
			stateLabel, authorLabel = "Base state", "base author"
		}
		lines = append(lines, wrap(fmt.Sprintf("ID: %s | %s: %d", safe(p.ID), revisionLabel, p.Revision.Number), m.width)...)
		lines = append(lines, wrap(digestLabel+": "+safe(p.Revision.Digest), m.width)...)
		state := "draft"
		if p.Revision.SubmittedAt != nil {
			state = "submitted for design review"
		}
		if p.Approved {
			state = "design approved"
		}
		lines = append(lines, wrap(stateLabel+": "+state+" | "+authorLabel+": "+safe(p.Revision.Author), m.width)...)
	}
	if m.draft != nil && m.editor == nil {
		lines = append(lines, wrap("PREVIEW: "+m.draftOp+" design (not saved)", m.width)...)
	}
	if m.editor != nil {
		label := "New Change"
		if m.draftOp == "revise" {
			label = "Edit Design"
		}
		lines = append(lines, wrap("FORM: "+label+" (not saved)", m.width)...)
	}
	if m.recoveryPreview && m.recovery != nil {
		label := "RECOVERED INPUT: complete replacement; no automatic merge."
		if m.recovery.op == "revise" {
			label += fmt.Sprintf(" Previous attempt used revision %d.", m.recovery.revision)
			lines = append(lines, wrap(label, m.width)...)
			lines = append(lines, wrap("Previous digest: "+safe(m.recovery.digest), m.width)...)
		} else {
			lines = append(lines, wrap(label+" Previous create may have committed.", m.width)...)
		}
	}
	if m.collectionMode {
		lines = append(lines, m.collectionHeader()...)
	}
	if m.assistanceMode {
		lines = append(lines, "ASSISTANCE | Native assistant inbox; no model is started here.")
	}
	return append(switchHeader(m.width, m.height-len(lines)-5-6), lines...)
}
