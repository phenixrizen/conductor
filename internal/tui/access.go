package tui

import (
	"errors"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/pkg/client"
)

type accessState struct {
	authenticated, ready, restartRequired bool
	workspaceID, repositoryID             string
	principal                             domain.Principal
	repository                            domain.ManagedRepository
	generation                            int
	truncated                             bool
}

var (
	errPrincipalChanged = errors.New("server principal changed; exit and start a new terminal session")
	errResponseScope    = errors.New("server response does not match the requested work or selected workspace and repository")
)

// Discovery informs the interface; the service remains responsible for checking
// the current grant in the transaction that commits each command.
func (a accessState) inspect(session domain.Session, page domain.RepositoryPage) (accessState, error) {
	if domain.ValidateAccessID(session.Principal.ID) != nil || (session.Principal.Kind != "human" && session.Principal.Kind != "agent") {
		return a, errors.New("server returned an invalid principal")
	}
	if a.principal.ID != "" && a.principal != session.Principal {
		return a, errPrincipalChanged
	}
	if len(session.Workspaces) > domain.MaxAccessPageSize || len(page.Repositories) > domain.MaxAccessPageSize {
		return a, errors.New("server access discovery exceeds the supported page bounds")
	}
	workspaces := make(map[string]bool, len(session.Workspaces))
	for _, workspace := range session.Workspaces {
		if domain.ValidateAccessID(workspace.ID) != nil || !accessText(workspace.Name, 256) || workspaces[workspace.ID] {
			return a, errors.New("server returned invalid or ambiguous workspace discovery")
		}
		workspaces[workspace.ID] = true
	}
	if !workspaces[a.workspaceID] {
		if session.Truncated {
			return a, errors.New("workspace discovery is truncated; access to the selected workspace could not be established")
		}
		return a, errors.New("selected workspace is not in the server's active memberships")
	}
	repositories := make(map[string]bool, len(page.Repositories))
	var selected domain.ManagedRepository
	for _, repository := range page.Repositories {
		config := domain.RepositoryConfig{ID: repository.ID, WorkspaceID: repository.WorkspaceID,
			Provider: repository.Provider, Host: repository.Host, ProviderID: repository.ProviderID, Name: repository.Name}
		if repository.WorkspaceID != a.workspaceID || !repository.CanRead || repositories[repository.ID] ||
			domain.ValidateAccessConfig(domain.AccessConfig{Repositories: []domain.RepositoryConfig{config}}) != nil {
			return a, errors.New("server returned invalid or ambiguous repository discovery")
		}
		repositories[repository.ID] = true
		if repository.ID == a.repositoryID {
			selected = repository
		}
	}
	if selected.ID == "" {
		if page.Truncated {
			return a, errors.New("repository discovery is truncated; access to the selected repository could not be established")
		}
		return a, errors.New("selected repository is not in the server's readable repositories")
	}
	a.principal, a.repository, a.ready = session.Principal, selected, true
	a.truncated = session.Truncated || page.Truncated
	return a, nil
}

func accessText(value string, max int) bool {
	return value != "" && len(value) <= max && utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl)
}

func accessDenied(err error) bool {
	var apiErr *client.APIError
	return errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden)
}

func (m *model) clearInspection() {
	m.pack, m.draft = nil, nil
	id := m.collections.inspectID
	m.collections = collectionState{cursors: []string{""}, inspectID: id}
	m.page = domain.ChangePage{}
	m.cursors, m.pageIndex = []string{""}, 0
	m.selected, m.offset = 0, 0
	m.lines = nil
	m.prompt, m.input, m.draftOp = "", "", ""
	m.pending = request{}
	m.blocked = true
}

func (m *model) invalidateAccess() {
	if m.cancel != nil {
		m.cancel()
	}
	m.cancel, m.busy = nil, ""
	m.serial++
	m.access.generation++
	m.access.ready, m.access.truncated = false, false
	m.access.repository = domain.ManagedRepository{}
	m.clearInspection()
}

func (m model) recheckAccess(id string) (tea.Model, tea.Cmd) {
	m.invalidateAccess()
	m.inspectID = id
	if m.access.restartRequired {
		m.status = "Access unavailable: " + errPrincipalChanged.Error()
		return m, nil
	}
	m.status = "Checking server identity and repository permissions..."
	return m.start(request{op: "access", id: id})
}

func (m model) actor() string {
	if m.access.authenticated {
		return m.access.principal.ID
	}
	return m.options.Actor
}

func (m model) actionAllowed(op string) error {
	if m.access.authenticated {
		if !m.access.ready {
			return errors.New("press r to recheck access and inspect shared state")
		}
		action := "author"
		if op == "approve" {
			action = "approve"
		}
		r := m.access.repository
		if err := domain.ValidateRepositoryAction(m.access.principal, r.CanRead, r.CanAuthor, r.CanApprove, action); err != nil {
			return err
		}
	}
	if m.blocked {
		return errors.New("refresh and inspect with r before another write")
	}
	return nil
}

func (m model) validateScope(res result) error {
	if !m.access.authenticated {
		return nil
	}
	if isCollectionOperation(res.op) {
		return m.validateCollectionScope(res)
	}
	if res.op == "list" {
		if len(res.page.Changes) > pageSize || len(res.page.NextBefore) > 1024 ||
			!utf8.ValidString(res.page.NextBefore) || strings.ContainsFunc(res.page.NextBefore, unicode.IsControl) {
			return errResponseScope
		}
		seen := make(map[string]bool, len(res.page.Changes))
		for _, change := range res.page.Changes {
			if domain.ValidateAccessID(change.ID) != nil || seen[change.ID] || change.Revision < 1 ||
				change.WorkspaceID != m.access.workspaceID || change.RepositoryID != m.access.repositoryID {
				return errResponseScope
			}
			seen[change.ID] = true
		}
	} else if res.op == "open" || isMutation(res.op) {
		if domain.ValidateAccessID(res.pack.ID) != nil || res.pack.Revision.ChangeID != res.pack.ID || res.pack.Revision.Number < 1 ||
			(res.op != "create" && res.pack.ID != res.id) ||
			res.pack.WorkspaceID != m.access.workspaceID || res.pack.RepositoryID != m.access.repositoryID {
			return errResponseScope
		}
	}
	return nil
}
