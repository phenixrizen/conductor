package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/execution"
)

func (m releaseModel) releaseHeader() []string {
	lines := wrap("Conductor | "+safe(m.view)+" | principal: "+safe(m.access.principal.ID)+" ("+safe(m.access.principal.Kind)+")", m.width)
	lines = append(lines, wrap("Workspace: "+safe(m.access.workspaceID)+" | Repository ID: "+safe(m.access.repositoryID), m.width)...)
	lines = append(lines, wrap("1 graphs | 2 runs | 3 deliveries | 4 tracker | 5 runtime | q exit", m.width)...)
	if id, d := m.recordIdentity(); id != "" {
		lines = append(lines, wrap("ID: "+safe(id), m.width)...)
		lines = append(lines, wrap("Digest: "+safe(d), m.width)...)
	}
	if m.draft != nil {
		lines = append(lines, wrap("PREVIEW "+safe(m.draft.Kind)+" | key: "+safe(m.draft.IdempotencyKey), m.width)...)
		lines = append(lines, wrap("Preview digest: "+m.draft.Digest, m.width)...)
	}
	if m.view == "runs" || m.view == "deliveries" {
		lines = append(lines, wrap(fmt.Sprintf("Selected repository: execute=%t publish=%t. Every source grant is checked by the server.", m.caps.CanExecute, m.caps.CanPublish), m.width)...)
	}
	var observation *domain.CollectionExecution
	switch r := m.record.(type) {
	case domain.CoordinationRun:
		observation = r.Execution
	case domain.Delivery:
		observation = r.Execution
	case domain.RuntimeEvidence:
		observation = r.Execution
		lines = append(lines, wrap(safe(runtimeWindow(r, time.Now())), m.width)...)
	}
	if m.record != nil && (m.view == "runs" || m.view == "deliveries" || m.view == "runtime") {
		label := "Execution observation: unavailable; stopping and cleanup unknown."
		if m.view == "runtime" {
			label = "Runtime execution observation: unavailable; retained receipt is a separate fact."
		}
		if observation != nil {
			age := max(0, int(time.Since(observation.ObservedAt).Seconds()))
			state := "aged"
			if age <= 30 {
				state = "recent"
			}
			label = fmt.Sprintf("Execution: %s | observed %s | %s (%ds); display does not poll", observation.State, observation.ObservedAt.UTC().Format(time.RFC3339), state, age)
		}
		lines = append(lines, wrap(safe(label), m.width)...)
	}
	return lines
}
func (m releaseModel) small() bool   { return m.width < 50 || m.height < len(m.releaseHeader())+8 }
func (m releaseModel) bodyRows() int { return max(1, m.height-len(m.releaseHeader())-6) }
func (m releaseModel) View() string {
	if m.small() {
		return strings.Join(fit(wrap("Conductor: enlarge terminal to at least 50 columns and enough rows for exact identity/digest. q exits; Ctrl+C always exits.", m.width), m.height, m.width), "\n")
	}
	header := m.releaseHeader()
	body := []string{}
	if !m.access.ready {
		body = []string{"Access unavailable. Press r to recheck the same credential and scope."}
	} else if m.record != nil || m.draft != nil || m.presentation != nil {
		end := min(len(m.lines), m.offset+m.bodyRows())
		body = m.lines[min(m.offset, end):end]
	} else if len(m.rows) == 0 {
		body = []string{"No shared records on this page. c imports a request; o opens an exact ID."}
	} else {
		for i := m.selected; i < len(m.rows) && len(body) < m.bodyRows(); i++ {
			marker := "  "
			if i == m.selected {
				marker = "> "
			}
			body = append(body, wrap(marker+safe(m.rows[i].ID)+" | "+safe(m.rows[i].Label), m.width)...)
		}
	}
	status := m.status
	if m.busy != "" {
		status = "Loading " + m.busy + "... Esc cancels; a cancelled write can have committed."
	}
	state := fmt.Sprintf("Page %d | n/p page", m.pageIndex+1)
	if m.next == "" {
		state += " | end"
	}
	if m.pageTruncated {
		state += " | truncated; more records may exist"
	}
	if m.record != nil || m.draft != nil || m.presentation != nil {
		state = fmt.Sprintf("Lines %d-%d/%d | arrows/PgUp/PgDn/Home/End", min(m.offset+1, len(m.lines)), min(len(m.lines), m.offset+m.bodyRows()), len(m.lines))
	}
	help := "o ID | c import | r refresh access | b browse"
	switch m.view {
	case "graphs":
		help += " | / search | t node | f source"
	case "runs":
		help += " | v task artifact"
		help += " | e profiles | a authorize | x cancel"
	case "deliveries":
		help += " | v artifact | a authorize | u reconcile"
	case "tracker":
		help += " | e settings | u sync file | i latest sync"
	}
	if m.draft != nil {
		help = "s confirm exact preview | Esc discard | r clear and refresh"
	}
	last := "Missing, stale or unexecuted evidence is never passing."
	if m.prompt != "" {
		switch m.prompt {
		case "file":
			help = "Request JSON file path; Enter previews, Esc cancels"
		case "open":
			help = "Record ID; Enter inspects, Esc cancels"
		case "task-artifact":
			help = "Task key or opaque ID from inspected receipts; Enter reads exact artifact"
		case "source-repository":
			help = "Repository ID from inspected graph sources; Enter selects"
		case "source-path":
			help = "Exact relative source path; Enter reads retained artifact"
		case "search":
			help = "Graph search (up to 256 characters); Enter searches"
		case "node":
			help = "Graph node ID (64 lowercase hex); Enter traverses depth 1"
		default:
			help = "Type " + m.prompt + " to confirm the displayed facts; Enter sends"
		}
		last = "> " + tail(safe(m.input), m.width-3) + "_"
	}
	lines := append(header, fit(body, m.bodyRows(), m.width)...)
	lines = append(lines, clip(state, m.width))
	lines = append(lines, fit(wrap(safe(status), m.width), 3, m.width)...)
	lines = append(lines, clip(help, m.width), clip(last, m.width))
	return strings.Join(lines, "\n")
}
func (m *releaseModel) rebuildRelease() {
	m.lines = nil
	add := func(text string) {
		for _, line := range strings.Split(text, "\n") {
			m.lines = append(m.lines, wrap(safe(line), m.width)...)
		}
	}
	value := m.record
	if m.presentation != nil {
		value = m.presentation
	}
	if m.draft != nil {
		value = m.draft
	}
	if runtime, ok := value.(domain.RuntimeEvidence); ok {
		add(runtimeSummary(runtime))
	}
	var artifact json.RawMessage
	switch a := value.(type) {
	case domain.DeliveryArtifact:
		artifact = a.Artifact
	case domain.CoordinationArtifact:
		artifact = a.Artifact
		if a.Verification != nil {
			add("Criterion support applies only to this exact artifact; it is not overall acceptance or approval.")
			for _, c := range a.Verification.Criteria {
				add(c.Requirement.ChangeID + " / " + c.Requirement.CriterionID + ": " + c.State + " (" + c.Reason + ")")
				add(c.Description)
			}
		}
	}
	if artifact != nil {
		var result execution.Result
		if json.Unmarshal(artifact, &result) == nil {
			add("Readable patch copies follow; the complete encoded artifact remains below.")
			for _, p := range result.Patches {
				add("Repository: " + p.RepositoryID + " | patch digest " + p.Digest)
				add(string(p.Patch))
			}
		}
	}
	if value != nil {
		data, err := json.Marshal(value)
		if err != nil {
			m.status = "Cannot render the complete record; commands blocked."
			m.blocked = true
		} else {
			add(prettyJSON(data))
		}
	}
	m.offset = min(m.offset, max(0, len(m.lines)-m.bodyRows()))
}
