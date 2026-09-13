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
	execute                         executor
	cancel                          context.CancelFunc
	serial                          int
	busy                            string
	status                          string
	blocked                         bool
	width, height, selected, offset int
	page                            domain.ChangePage
	// Cursors are bounded to visited pages; the server owns opaque cursor meaning.
	cursors       []string
	pageIndex     int
	pack          *domain.Package
	draft         domain.Content
	draftOp       string
	lines         []string
	prompt, input string
	pending       request
}

func newModel(options Options, execute executor) model {
	return model{options: options, execute: execute, width: 80, height: 24,
		cursors: []string{""}, status: "Loading shared work..."}
}

func (m model) Init() tea.Cmd {
	// Starting via Update gives every operation a serial and cancel function,
	// including the initial read, so a late response can never replace inspection.
	return func() tea.Msg { return startMsg{} }
}

type startMsg struct{}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case startMsg:
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
		if msg.err == nil && isMutation(msg.op) {
			msg.err = validateMutationResult(msg.request, msg.pack)
		}
		m.cancel = nil
		m.busy = ""
		if msg.err != nil {
			m.status = "Unavailable: " + msg.err.Error()
			if isMutation(msg.op) {
				m.blocked = true
				var apiErr *client.APIError
				if errors.As(msg.err, &apiErr) && apiErr.StatusCode == 409 {
					m.status = "Stale inspection: press r to load and inspect the latest revision. " + msg.err.Error()
				} else {
					m.status = "Write not confirmed. Inspect shared state with r before another write; do not assume it failed. " + msg.err.Error()
				}
			}
			return m, nil
		}
		switch msg.op {
		case "list":
			m.page, m.selected = msg.page, 0
			m.pack, m.draft = nil, nil
			m.blocked = false
			m.status = "Shared work loaded. Repository labels are discovery filters only."
		case "file":
			m.draft = msg.content
			if m.draftOp == "create" {
				m.pack = nil
			}
			m.status = "File loaded for preview. This is the complete replacement content; s confirms saving."
		default:
			m.pack = &msg.pack
			m.draft = nil
			m.blocked = false
			m.status = "Loaded latest revision. Inspect the complete content before an action."
			if msg.op == "approve" {
				m.status = "Design approval recorded. Implementation verification, merge and deployment are separate."
			} else if isMutation(msg.op) {
				m.status = msg.op + " recorded. Inspect the returned revision before another action."
			}
		}
		m.offset = 0
		m.rebuild()
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" || (msg.String() == "q" && m.prompt == "") {
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
				if isMutation(m.busy) {
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
		switch msg.String() {
		case "o":
			m.prompt, m.input = "open", ""
		case "c":
			if m.draft != nil {
				m.status = "Save or discard the current preview before loading another file."
				break
			}
			if m.blocked {
				m.status = "Blocked: refresh shared state with r before another write."
				break
			}
			m.prompt, m.input, m.draftOp = "file", m.options.File, "create"
		case "e":
			if m.pack == nil || m.draft != nil || m.blocked {
				m.status = "Open and inspect a latest revision before revising."
				break
			}
			m.prompt, m.input, m.draftOp = "file", m.options.File, "revise"
		case "b":
			return m.list(0)
		case "r":
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
			if err := domain.ValidateApproval(m.pack.Revision, m.pack.Revision.Number, m.pack.Revision.Digest, m.options.Actor); err != nil {
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
	return op == "create" || op == "revise" || op == "submit" || op == "approve"
}

// A command can commit and then read a concurrently edited package. Treat that
// response as uncertain instead of claiming the newly returned revision received
// the approval or submission the user sent for their inspected tuple.
func validateMutationResult(req request, p domain.Package) error {
	wantRevision, wantDigest := req.revision, req.digest
	if req.op == "create" || req.op == "revise" {
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
	if req.op == "approve" && (!p.Approved || p.Approval == nil || p.Approval.Revision != req.revision || p.Approval.Digest != req.digest) {
		return errors.New("command response does not confirm the inspected design approval")
	}
	if req.op == "submit" && p.Revision.SubmittedAt == nil {
		return errors.New("command response does not confirm submission")
	}
	return nil
}

func (m model) start(req request) (tea.Model, tea.Cmd) {
	if m.cancel != nil {
		m.cancel()
	}
	m.serial++
	req.serial = m.serial
	m.busy = req.op
	m.prompt, m.input = "", ""
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
	m.pending = request{op: op, content: m.draft}
	if m.pack != nil {
		m.pending.id, m.pending.revision, m.pending.digest = m.pack.ID, m.pack.Revision.Number, m.pack.Revision.Digest
	}
	m.prompt, m.input = op, ""
	return m, nil
}

func (m model) updatePrompt(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.prompt, m.input = "", ""
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
	if m.pack == nil && m.draft == nil {
		m.selected = max(0, min(len(m.page.Changes)-1, m.selected+delta))
		return
	}
	m.offset = max(0, min(max(0, len(m.lines)-m.bodyHeight()), m.offset+delta))
}

func (m model) bodyHeight() int { return max(1, m.height-len(m.header())-5) }

func (m model) header() []string {
	lines := wrap("Conductor | local actor: "+safe(m.options.Actor)+" (not authentication)", m.width)
	if m.pack != nil {
		p := m.pack
		revisionLabel, digestLabel, stateLabel, authorLabel := "revision", "Digest", "State", "author"
		if m.draft != nil {
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
	if m.draft != nil {
		lines = append(lines, wrap("PREVIEW: "+m.draftOp+" complete JSON replacement (not saved)", m.width)...)
	}
	return lines
}
