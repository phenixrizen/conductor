package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/phenixrizen/conductor/internal/domain"
)

// Collection controls form a separate mode: their keys cannot fall through to
// package submission or approval, even when an attachment target is retained.
type collectionState struct {
	page                     domain.CollectionPage
	cursors                  []string
	pageIndex, selected      int
	record                   *domain.Collection
	draft                    *collectionDraft
	inspectID                string
	uncertainCreate, blocked bool
}

type collectionDraft struct {
	Commit         string   `json:"commit"`
	Paths          []string `json:"paths"`
	IdempotencyKey string   `json:"idempotencyKey"`
	FullSource     bool     `json:"fullSource,omitempty"`
}

func (d collectionDraft) input() domain.CollectionInput {
	return domain.CollectionInput{Commit: d.Commit, Paths: d.Paths, FullSource: d.FullSource}
}

func isCollectionOperation(op string) bool {
	return op == "collections" || op == "collection" || op == "collection-file" || isCollectionWrite(op)
}
func isCollectionWrite(op string) bool { return op == "collect" || op == "cancel-collection" }
func (m model) requestActionAllowed(op string) error {
	if isAssistanceWrite(op) {
		return m.assistanceActionAllowed(op)
	}
	if isCollectionWrite(op) || op == "attach" {
		return m.collectionActionAllowed(op)
	}
	return m.actionAllowed(op)
}
func (m model) collectionActionAllowed(op string) error {
	if !m.access.authenticated || !m.access.ready {
		return errors.New("authenticated workspace and repository access is required")
	}
	r := m.access.repository
	if err := domain.ValidateRepositoryAction(m.access.principal, r.CanRead, r.CanAuthor, r.CanApprove, "author"); err != nil {
		return err
	}
	switch op {
	case "collect":
		if m.collections.draft == nil {
			return errors.New("load and inspect a collection request file first")
		}
	case "cancel-collection":
		c := m.collections.record
		if c == nil || m.collections.blocked {
			return errors.New("refresh and inspect the collection first")
		}
		if c.RequesterID != m.access.principal.ID {
			return errors.New("only the recorded requester can request cancellation")
		}
		if c.Receipt != nil {
			return errors.New("a committed receipt is already available")
		}
		if c.CancelRequestedAt != nil {
			return errors.New("cancellation was already requested; refresh to inspect its observation")
		}
	case "attach":
		if m.collections.record == nil || m.collections.record.Receipt == nil || m.collections.blocked {
			return errors.New("inspect a committed collection receipt first")
		}
		if m.pack == nil || m.blocked || m.draft != nil {
			return errors.New("switch with g and inspect the target package before attaching")
		}
	}
	return nil
}

func (m model) listCollections(index int) (tea.Model, tea.Cmd) {
	if index == 0 {
		m.collections.cursors = []string{""}
	}
	m.collections.pageIndex = index
	m.collections.page = domain.CollectionPage{}
	m.collections.record = nil
	m.collections.inspectID = ""
	m.offset = 0
	m.rebuild()
	return m.start(request{op: "collections", cursor: m.collections.cursors[index]})
}
func (m model) recheckCollectionAccess() (tea.Model, tea.Cmd) {
	id := m.collections.inspectID
	retained := m.recovery
	m.invalidateAccess()
	m.recovery = retained
	m.collections.inspectID = id
	if m.access.restartRequired {
		m.recovery = nil
		m.status = "Access unavailable: " + errPrincipalChanged.Error()
		return m, nil
	}
	m.status = "Checking server identity and repository permissions..."
	return m.start(request{op: "access", id: id, collectionView: true})
}
func (m model) collectionKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "c":
		if m.collections.draft != nil {
			m.status = "Save or discard the existing request preview before selecting another file."
			break
		}
		// Loading a file has no effect, but avoid presenting a write workflow to readers.
		r := m.access.repository
		if err := domain.ValidateRepositoryAction(m.access.principal, r.CanRead, r.CanAuthor, r.CanApprove, "author"); err != nil {
			m.status = "Action blocked: " + err.Error()
			break
		}
		m.prompt, m.input = "collection-file", ""
	case "s":
		return m.confirmCollection("collect")
	case "x":
		return m.confirmCollection("cancel-collection")
	case "t":
		return m.confirmCollection("attach")
	case "o":
		if m.collections.draft != nil {
			m.status = "Save or discard the request preview before inspecting another collection."
			break
		}
		m.prompt, m.input = "collection-open", ""
	case "r":
		return m.recheckCollectionAccess()
	case "b":
		if m.collections.draft != nil {
			m.status = "Save or discard the request preview before browsing."
			break
		}
		return m.listCollections(0)
	case "n":
		if m.collections.record == nil && m.collections.draft == nil && m.collections.page.NextBefore != "" {
			if len(m.collections.cursors) >= 1000 {
				m.status = "Page navigation limit reached; r restarts discovery."
				break
			}
			m.collections.cursors = append(m.collections.cursors[:m.collections.pageIndex+1], m.collections.page.NextBefore)
			return m.listCollections(m.collections.pageIndex + 1)
		}
	case "p":
		if m.collections.record == nil && m.collections.draft == nil && m.collections.pageIndex > 0 {
			return m.listCollections(m.collections.pageIndex - 1)
		}
	case "enter":
		if m.collections.record == nil && m.collections.draft == nil && len(m.collections.page.Collections) > 0 {
			return m.start(request{op: "collection", id: m.collections.page.Collections[m.collections.selected].ID})
		}
	case "esc":
		if m.collections.draft != nil {
			uncertain := m.collections.uncertainCreate
			m.collections.draft = nil
			m.collections.uncertainCreate = false
			m.offset = 0
			m.rebuild()
			m.status = "Request preview discarded; no new write sent."
			if uncertain {
				m.status = "Preview discarded. The prior request may have committed; inspect shared collections before creating different work."
			}
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
		m.move(-len(m.lines) - len(m.collections.page.Collections))
	case "end":
		m.move(len(m.lines) + len(m.collections.page.Collections))
	}
	return m, nil
}
func (m model) confirmCollection(op string) (tea.Model, tea.Cmd) {
	if err := m.collectionActionAllowed(op); err != nil {
		m.status = "Action blocked: " + err.Error()
		return m, nil
	}
	if m.collections.draft != nil && op != "collect" {
		m.status = "Save or discard the request preview before another command."
		return m, nil
	}
	req := request{op: op, accessGeneration: m.access.generation}
	if op == "collect" {
		req.collectionDraft = *m.collections.draft
		req.collectionDraft.Paths = append([]string(nil), m.collections.draft.Paths...)
	} else {
		req.id = m.collections.record.ID
		if op == "attach" {
			// Compute the exact expected replacement before confirmation; only the
			// immutable receipt identity/digest and inspected revision cross the API.
			req.id = m.pack.ID
			req.revision = m.pack.Revision.Number
			req.collectionID = m.collections.record.ID
			req.receiptDigest = m.collections.record.Receipt.Digest
			req.content = make(domain.Content, len(m.pack.Revision.Content)+1)
			for key, value := range m.pack.Revision.Content {
				req.content[key] = value
			}
			req.content["repositoryContext"] = m.collections.record.Receipt.Snapshot
			req.digest = m.pack.Revision.Digest
		}
	}
	m.pending = req
	m.prompt = op
	m.input = ""
	return m, nil
}
func (m *model) collectionCancelled(op string) {
	if op == "collect" {
		m.collections.uncertainCreate = true
		m.status = "Collection write outcome unknown. The same request and key are retained; s explicitly retries that request."
	} else if op == "cancel-collection" {
		m.collections.blocked = true
		m.status = "Cancellation outcome unknown. Press r to inspect; no cancellation is confirmed."
	} else {
		m.status = "Read cancelled. Press r to retry inspection."
	}
}
func (m model) collectionResult(res result) (tea.Model, tea.Cmd) {
	if res.err != nil {
		switch res.op {
		case "collect":
			m.collections.uncertainCreate = true
			m.status = "Collection write not confirmed. Same request and key retained; s retries explicitly. " + res.err.Error()
		case "cancel-collection":
			m.collections.blocked = true
			m.status = "Cancellation not confirmed. Press r to inspect shared facts. " + res.err.Error()
		case "collection", "collections":
			m.collections.record = nil
			m.collections.page = domain.CollectionPage{}
			m.collections.blocked = true
			m.rebuild()
		}
		return m, nil
	}
	switch res.op {
	case "collection-file":
		draft := res.requestDraft
		m.collections.draft = &draft
		m.collections.record = nil
		m.collections.inspectID = ""
		m.collections.uncertainCreate = false
		m.status = "Request file loaded for preview. No collection sent; s confirms this exact input and key."
	case "collections":
		m.collections.page = res.collectionPage
		m.collections.selected = 0
		m.collections.record = nil
		m.collections.inspectID = ""
		m.collections.blocked = false
		m.status = "Shared collections loaded. Accepted intent and observed execution are separate facts."
	default:
		value := res.collection
		m.collections.record = &value
		m.collections.inspectID = value.ID
		m.collections.draft = nil
		m.collections.blocked = false
		m.collections.uncertainCreate = false
		m.status = "Collection inspected. Collected source is not passing verification."
		if res.op == "collect" {
			m.status = "Collection request accepted. Background execution is not yet confirmed by this acknowledgment."
		}
		if res.op == "cancel-collection" {
			m.status = "Cancellation response received. Only observed Temporal cancellation confirms it stopped; an existing receipt remains fact."
		}
	}
	m.offset = 0
	m.rebuild()
	return m, nil
}

// Treat collection source, scope and receipt checks as a single admission gate
// before rendering or enabling attachment. A familiar ID alone is not provenance.
func (m model) validateCollectionScope(res result) error {
	if res.op == "collection-file" {
		return nil
	}
	validID := func(id, workspace, repository, requester, commit string) bool {
		return domain.IsLowerHex(id, 32) && workspace == m.access.workspaceID && repository == m.access.repositoryID && domain.ValidateAccessID(requester) == nil && domain.IsLowerHex(commit, 40)
	}
	if res.op == "collections" {
		p := res.collectionPage
		if len(p.Collections) > pageSize || !validCollectionCursor(p.NextBefore) {
			return errResponseScope
		}
		seen := map[string]bool{}
		for _, c := range p.Collections {
			if !validID(c.ID, c.WorkspaceID, c.RepositoryID, c.RequesterID, c.Commit) || seen[c.ID] || (c.ReceiptDigest != "" && !domain.IsLowerHex(c.ReceiptDigest, 64)) {
				return errResponseScope
			}
			seen[c.ID] = true
		}
		return nil
	}
	c := res.collection
	if !validID(c.ID, c.WorkspaceID, c.RepositoryID, c.RequesterID, c.Input.Commit) || (res.op != "collect" && c.ID != res.id) {
		return errResponseScope
	}
	input, err := domain.NormalizeCollectionInput(c.Input)
	if err != nil || !reflect.DeepEqual(c.Input, input) {
		return errResponseScope
	}
	digest, err := domain.JSONDigest(input)
	if err != nil || digest != c.InputDigest {
		return errResponseScope
	}
	source, r := c.Source, m.access.repository
	if domain.ValidateContextSource(source) != nil || source.WorkspaceID != c.WorkspaceID || source.RepositoryID != c.RepositoryID || source.Provider != r.Provider || source.Host != r.Host || source.ProviderID != r.ProviderID {
		return errResponseScope
	}
	if c.Receipt != nil && domain.ValidateCollectionReceipt(*c.Receipt, c) != nil {
		return errResponseScope
	}
	if res.op == "collect" && (c.RequesterID != m.access.principal.ID || !reflect.DeepEqual(c.Input, res.collectionDraft.input())) {
		return errResponseScope
	}
	if res.op == "cancel-collection" && (c.RequesterID != m.access.principal.ID || (c.CancelRequestedAt == nil && c.Receipt == nil)) {
		return errResponseScope
	}
	return nil
}
func validCollectionCursor(cursor string) bool {
	return len(cursor) <= 1024 && utf8.ValidString(cursor) && !strings.ContainsFunc(cursor, unicode.IsControl)
}

func readCollectionDraft(ctx context.Context, path string) (collectionDraft, error) {
	var draft collectionDraft
	if err := ctx.Err(); err != nil {
		return draft, err
	}
	if path == "" || path == "-" {
		return draft, errors.New("provide a regular JSON request file; terminal input is reserved for review")
	}
	info, err := os.Stat(path)
	if err != nil {
		return draft, fmt.Errorf("inspect request file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return draft, errors.New("collection request must be a regular JSON file")
	}
	f, err := openContentFile(path)
	if err != nil {
		return draft, fmt.Errorf("open request file: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err != nil {
		return draft, fmt.Errorf("read request file: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return draft, err
	}
	if len(data) > 64<<10 || !utf8.Valid(data) {
		return draft, errors.New("request file must contain at most 64 KiB of UTF-8 JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	tok, err := decoder.Token()
	if err != nil || tok != json.Delim('{') {
		return draft, errors.New("request file must be a JSON object")
	}
	seen := map[string]bool{}
	for decoder.More() {
		tok, err = decoder.Token()
		if err != nil {
			return draft, errors.New("invalid request JSON")
		}
		name, ok := tok.(string)
		if !ok || seen[name] || (name != "commit" && name != "paths" && name != "idempotencyKey" && name != "fullSource") {
			return draft, errors.New("request fields are commit, paths, idempotencyKey and optional fullSource, with no duplicates")
		}
		seen[name] = true
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return draft, errors.New("request fields cannot be null")
		}
		switch name {
		case "commit":
			err = json.Unmarshal(raw, &draft.Commit)
		case "paths":
			err = json.Unmarshal(raw, &draft.Paths)
		case "fullSource":
			err = json.Unmarshal(raw, &draft.FullSource)
		case "idempotencyKey":
			err = json.Unmarshal(raw, &draft.IdempotencyKey)
		}
		if err != nil {
			return draft, errors.New("invalid request field type")
		}
	}
	tok, err = decoder.Token()
	if err != nil || tok != json.Delim('}') || !seen["commit"] || !seen["paths"] || !seen["idempotencyKey"] {
		return draft, errors.New("request requires commit, paths and idempotencyKey")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return draft, errors.New("request file must contain one JSON object")
	}
	input, err := domain.NormalizeCollectionInput(draft.input())
	if err != nil || domain.ValidateCollectionKey(draft.IdempotencyKey) != nil {
		return draft, errors.New("request requires a full lowercase 40-character commit, 1-32 unique explicit relative paths and a valid 1-128 character idempotency key")
	}
	draft.Commit, draft.Paths = input.Commit, input.Paths
	return draft, nil
}

type collectionClockMsg time.Time

func collectionClock() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return collectionClockMsg(t) })
}
