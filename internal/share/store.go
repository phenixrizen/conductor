package share

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/phenixrizen/conductor/internal/session"
)

// Link is a share link record. The token itself is never stored. A link is
// either a session's (SessionID) or a crew run's (RunID, SessionID empty): a
// run link grants its role on the session of every member of the run.
type Link struct {
	ID        string       `json:"id"`
	SessionID string       `json:"sessionId"`
	RunID     string       `json:"runId,omitempty"`
	Label     string       `json:"label,omitempty"`
	Role      session.Role `json:"role"`
	CreatedAt time.Time    `json:"createdAt"`
	ExpiresAt *time.Time   `json:"expiresAt,omitempty"`
	Revoked   bool         `json:"revoked"`
	// Owner, on a switchyard, is the hash of the host instance a durable
	// link was minted for (kept on disk, outliving the host's absences);
	// Addr the open host's address it counts against. Never in a reply.
	Owner string `json:"-"`
	Addr  string `json:"-"`

	hash Hash
}

// Errors returned by the store.
var (
	ErrUnknownToken = errors.New("share: unknown token")
	ErrRevoked      = errors.New("share: link revoked")
	ErrExpired      = errors.New("share: link expired")
	ErrInvalidRole  = errors.New("share: invalid role")
	ErrTooManyLinks = errors.New("share: too many links for session")
)

// MaxLinksPerSession bounds link creation, for each session and for each run.
const MaxLinksPerSession = 100

// Store is an in-memory link index.
type Store struct {
	mu        sync.RWMutex
	byHash    map[Hash]*Link
	byID      map[string]*Link
	bySession map[string]map[string]*Link
	byRun     map[string]map[string]*Link
	now       func() time.Time

	// OnRevoke is invoked after a session's link is revoked so live viewers
	// can be closed.
	OnRevoke func(sessionID, linkID string)
	// OnRevokeRun is OnRevoke for a run's link.
	OnRevokeRun func(runID, linkID string)
}

// NewStore creates an empty store.
func NewStore() *Store {
	return &Store{byHash: map[Hash]*Link{}, byID: map[string]*Link{}, bySession: map[string]map[string]*Link{},
		byRun: map[string]map[string]*Link{}, now: func() time.Time { return time.Now().UTC() }}
}

// Create issues a new link to a session and returns the record plus the
// plaintext token.
func (s *Store) Create(sessionID string, role session.Role, label string, ttl time.Duration) (*Link, string, error) {
	return s.create(&Link{SessionID: sessionID}, s.bySession, sessionID, role, label, ttl)
}

// CreateRunLink issues a new link to a crew run and returns the record plus
// the plaintext token.
func (s *Store) CreateRunLink(runID string, role session.Role, label string, ttl time.Duration) (*Link, string, error) {
	return s.create(&Link{RunID: runID}, s.byRun, runID, role, label, ttl)
}

// create completes link, whose scope is set, and indexes it in scopes under
// key: at most MaxLinksPerSession links per key.
func (s *Store) create(link *Link, scopes map[string]map[string]*Link, key string, role session.Role, label string, ttl time.Duration) (*Link, string, error) {
	if !role.Valid() {
		return nil, "", ErrInvalidRole
	}
	if len(label) > 120 {
		label = label[:120]
	}
	tok, hash := NewToken()
	link.ID, link.Label, link.Role, link.CreatedAt, link.hash = session.NewID(), label, role, s.now(), hash
	if ttl > 0 {
		exp := link.CreatedAt.Add(ttl)
		link.ExpiresAt = &exp
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(scopes[key]) >= MaxLinksPerSession {
		return nil, "", ErrTooManyLinks
	}
	s.byHash[hash] = link
	s.byID[link.ID] = link
	if scopes[key] == nil {
		scopes[key] = map[string]*Link{}
	}
	scopes[key][link.ID] = link
	return copyLink(link), tok, nil
}

// Resolve returns the live link for a presented token. It reads the link
// under the lock, as a revoke writes it.
func (s *Store) Resolve(tok string) (*Link, error) {
	h := HashToken(tok)
	s.mu.RLock()
	defer s.mu.RUnlock()
	link, ok := s.byHash[h]
	if !ok {
		return nil, ErrUnknownToken
	}
	if link.Revoked {
		return nil, ErrRevoked
	}
	if link.ExpiresAt != nil && !s.now().Before(*link.ExpiresAt) {
		return nil, ErrExpired
	}
	return copyLink(link), nil
}

// Get returns a link by ID.
func (s *Store) Get(linkID string) (*Link, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	l, ok := s.byID[linkID]
	if !ok {
		return nil, false
	}
	return copyLink(l), true
}

// Revoke marks a session's link unusable and notifies OnRevoke the first
// time. found is false when the link does not belong to sessionID; revoked
// says whether this call revoked it (false when it was revoked before).
func (s *Store) Revoke(sessionID, linkID string) (found, revoked bool) {
	return s.revoke(linkID, func(l *Link) bool { return l.RunID == "" && l.SessionID == sessionID }, func() {
		if s.OnRevoke != nil {
			s.OnRevoke(sessionID, linkID)
		}
	})
}

// RevokeRun is Revoke for a run's link, notifying OnRevokeRun.
func (s *Store) RevokeRun(runID, linkID string) (found, revoked bool) {
	return s.revoke(linkID, func(l *Link) bool { return l.RunID != "" && l.RunID == runID }, func() {
		if s.OnRevokeRun != nil {
			s.OnRevokeRun(runID, linkID)
		}
	})
}

// revoke marks the link with linkID revoked when owned says it is the
// caller's, and calls notify, outside the lock, the first time.
func (s *Store) revoke(linkID string, owned func(*Link) bool, notify func()) (found, revoked bool) {
	s.mu.Lock()
	link, ok := s.byID[linkID]
	if !ok || !owned(link) {
		s.mu.Unlock()
		return false, false
	}
	already := link.Revoked
	link.Revoked = true
	s.mu.Unlock()
	if !already {
		notify()
	}
	return true, !already
}

// ListBySession returns links for a session, newest first.
func (s *Store) ListBySession(sessionID string) []*Link {
	return s.list(s.bySession, sessionID)
}

// ListByRun returns links for a run, newest first.
func (s *Store) ListByRun(runID string) []*Link {
	return s.list(s.byRun, runID)
}

func (s *Store) list(scopes map[string]map[string]*Link, key string) []*Link {
	s.mu.RLock()
	out := make([]*Link, 0, len(scopes[key]))
	for _, l := range scopes[key] {
		out = append(out, copyLink(l))
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// DeleteSession forgets the links of a session and returns the IDs it
// dropped. A durable link (one with an Owner) stays unless durableToo: its
// session left only because its host went away, and comes back under the
// same id when the host does.
func (s *Store) DeleteSession(sessionID string, durableToo bool) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var dropped []string
	for id, l := range s.bySession[sessionID] {
		if l.Owner != "" && !durableToo {
			continue
		}
		delete(s.byHash, l.hash)
		delete(s.byID, id)
		delete(s.bySession[sessionID], id)
		dropped = append(dropped, id)
	}
	if len(s.bySession[sessionID]) == 0 {
		delete(s.bySession, sessionID)
	}
	return dropped
}

// Restore puts back a link kept on disk, under the hash of its token.
func (s *Store) Restore(l Link, hash Hash) {
	link := l
	link.hash = hash
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byHash[hash] = &link
	s.byID[link.ID] = &link
	scopes, key := s.bySession, link.SessionID
	if link.RunID != "" {
		scopes, key = s.byRun, link.RunID
	}
	if scopes[key] == nil {
		scopes[key] = map[string]*Link{}
	}
	scopes[key][link.ID] = &link
}

// SetOwner makes a link durable: minted for the host instance owner, from
// the address addr ("" for a tokened host).
func (s *Store) SetOwner(linkID, owner, addr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if l, ok := s.byID[linkID]; ok {
		l.Owner, l.Addr = owner, addr
	}
}

// Drop forgets one link outright (its file swept), whatever its state.
func (s *Store) Drop(linkID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.byID[linkID]
	if !ok {
		return
	}
	delete(s.byHash, l.hash)
	delete(s.byID, linkID)
	for _, scopes := range []map[string]map[string]*Link{s.bySession, s.byRun} {
		for key, m := range scopes {
			if _, ok := m[linkID]; ok {
				delete(m, linkID)
				if len(m) == 0 {
					delete(scopes, key)
				}
			}
		}
	}
}

// Durable lists the durable links, for the sweep.
func (s *Store) Durable() []*Link {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Link
	for _, l := range s.byID {
		if l.Owner != "" {
			out = append(out, copyLink(l))
		}
	}
	return out
}

// Count is how many links the store holds.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byID)
}

// DeleteRun forgets every link of a run, as DeleteSession does a session's,
// and returns the IDs of those that were not revoked: viewers may still be
// attached through them.
func (s *Store) DeleteRun(runID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var live []string
	for id, l := range s.byRun[runID] {
		if !l.Revoked {
			live = append(live, id)
		}
		delete(s.byHash, l.hash)
		delete(s.byID, id)
	}
	delete(s.byRun, runID)
	return live
}

func copyLink(l *Link) *Link {
	cp := *l
	return &cp
}
