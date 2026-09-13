package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

func executionLabel(e *domain.CollectionExecution) string {
	if e == nil {
		return "unknown: no execution observation"
	}
	switch e.State {
	case "unavailable", "unresolved":
		return "progress " + safe(e.State)
	case "running", "completed", "cancelled", "failed", "timed_out", "terminated":
	default:
		return "unknown execution state: " + safe(e.State)
	}
	if !e.Current || e.ObservedAt.IsZero() || time.Since(e.ObservedAt) > 30*time.Second || e.ObservedAt.After(time.Now().Add(time.Second)) {
		return "stale or unavailable observation: " + safe(e.State)
	}
	return "observed " + safe(e.State)
}
func collectionFacts(receipt bool, cancel *time.Time, e *domain.CollectionExecution) string {
	state := "request accepted | " + executionLabel(e)
	if cancel != nil {
		state += " | cancellation requested"
	}
	if receipt {
		state += " | receipt available (inspect coverage)"
	}
	return state
}
func (m model) collectionHeader() []string {
	lines := wrap("CONTEXT COLLECTIONS | g returns to packages", m.width)
	if m.collections.draft != nil {
		lines = append(lines, wrap("PREVIEW: exact collection request (not confirmed)", m.width)...)
		if m.collections.uncertainCreate {
			lines = append(lines, wrap("Previous write outcome unknown; retry retains the same input and key.", m.width)...)
		}
	} else if c := m.collections.record; c != nil {
		lines = append(lines, wrap("Collection: "+safe(c.ID), m.width)...)
		lines = append(lines, wrap("Commit: "+safe(c.Input.Commit), m.width)...)
		if c.Receipt != nil {
			lines = append(lines, wrap("Receipt digest: "+safe(c.Receipt.Digest), m.width)...)
		}
		// Keep concise status in the fixed header so scrolling source cannot hide gaps
		// or make an old execution observation look like a fresh running command.
		lines = append(lines, wrap(collectionFacts(c.Receipt != nil, c.CancelRequestedAt, c.Execution), m.width)...)
		if c.Execution != nil {
			lines = append(lines, wrap("Observed at: "+safe(c.Execution.ObservedAt.UTC().Format(time.RFC3339)), m.width)...)
		}
	}
	return lines
}
func (m model) collectionBody(height int) []string {
	if m.collections.record != nil || m.collections.draft != nil {
		end := min(len(m.lines), m.offset+height)
		return m.lines[min(m.offset, end):end]
	}
	if m.collections.blocked {
		return []string{"Collection data unavailable. Press r to recheck access and inspect shared facts."}
	}
	if len(m.collections.page.Collections) == 0 {
		return []string{"No shared collections on this page.", "c previews a request file; o opens an existing collection ID."}
	}
	var body []string
	for i := m.collections.selected; i < len(m.collections.page.Collections) && len(body) < height; i++ {
		c := m.collections.page.Collections[i]
		marker := "  "
		if i == m.collections.selected {
			marker = "> "
		}
		body = append(body, wrap(marker+safe(c.ID)+" | "+collectionFacts(c.ReceiptDigest != "", c.CancelRequestedAt, c.Execution), m.width)...)
	}
	return body
}
func (m model) collectionFooter() []string {
	position := fmt.Sprintf("Collection page %d | n next, p previous", m.collections.pageIndex+1)
	if m.collections.page.NextBefore == "" {
		position += " | end"
	}
	if m.collections.record != nil || m.collections.draft != nil {
		position = fmt.Sprintf("Lines %d-%d/%d | arrows/PgUp/PgDn/Home/End", min(m.offset+1, len(m.lines)), min(len(m.lines), m.offset+m.bodyHeight()), len(m.lines))
	}
	status := m.status
	if m.busy != "" {
		status = "Loading " + m.busy + "... Esc cancels; a cancelled write may have committed."
	}
	statusLines := fit(wrap(safe(status), m.width), 2, m.width)
	help := "enter open | o ID | r access/refresh | n/p page | g packages | q quit"
	if m.collections.record != nil {
		help = "r access/refresh | b browse | g packages | q quit"
	}
	if m.access.repository.CanAuthor {
		help = "c request file | " + help
		if m.collections.record != nil {
			if m.collectionActionAllowed("cancel-collection") == nil {
				help = "x cancel | " + help
			}
			if m.collectionActionAllowed("attach") == nil {
				help = "t attach | " + help
			}
		}
	}
	if m.collections.draft != nil {
		help = "s confirm collection | Esc discard | g packages | q quit"
	}
	last := "Collected source is not verification; missing evidence is never passing."
	if m.prompt != "" {
		switch m.prompt {
		case "collection-file":
			help = "JSON request file path; Enter previews, Esc cancels"
		case "collection-open":
			help = "Collection ID; Enter inspects, Esc cancels"
		default:
			help = "Type " + m.prompt + " to confirm displayed facts; Enter sends, Esc cancels"
		}
		last = "> " + tail(safe(m.input), max(1, m.width-3)) + "_"
	}
	return []string{clip(position, m.width), statusLines[0], statusLines[1], clip(help, m.width), clip(last, m.width)}
}
func (m *model) rebuildCollection() {
	var value any
	if m.collections.draft != nil {
		value = m.collections.draft
	} else if c := m.collections.record; c != nil {
		// Summaries lead into complete escaped JSON, so all retained source and fields
		// remain inspectable even when the terminal is too small for one screen.
		m.lines = append(m.lines, wrap("Requester: "+safe(c.RequesterID)+" | created: "+c.CreatedAt.UTC().Format(time.RFC3339), m.width)...)
		s := c.Source
		m.lines = append(m.lines, wrap(fmt.Sprintf("Source: %s %s repository %s | profile %s | integration version %d", safe(s.Provider), safe(s.Host), safe(s.ProviderID), safe(s.Profile), s.IntegrationVersion), m.width)...)
		if c.Receipt != nil {
			for _, a := range c.Receipt.Snapshot.Artifacts {
				m.lines = append(m.lines, wrap("Coverage: "+safe(a.Path)+" | "+safe(a.State)+" | "+safe(a.Message), m.width)...)
			}
		} else {
			m.lines = append(m.lines, "No committed receipt; artifact coverage is unavailable.")
		}
		m.lines = append(m.lines, "Complete collection JSON:")
		value = c
	}
	if value != nil {
		data, err := json.Marshal(value)
		if err != nil {
			m.collections.blocked = true
			m.status = "Cannot render collection; actions blocked."
		} else {
			for _, line := range strings.Split(prettyJSON(data), "\n") {
				m.lines = append(m.lines, wrap(safe(line), m.width)...)
			}
		}
	}
	m.offset = min(m.offset, max(0, len(m.lines)-m.bodyHeight()))
}
