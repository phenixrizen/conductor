package share

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/phenixrizen/conductor/internal/session"
)

// Link is a share link record. The token itself is never stored.
type Link struct {
	ID        string       `json:"id"`
	SessionID string       `json:"sessionId"`
	Label     string       `json:"label,omitempty"`
	Role      session.Role `json:"role"`
	CreatedAt time.Time    `json:"createdAt"`
	ExpiresAt *time.Time   `json:"expiresAt,omitempty"`
	Revoked   bool         `json:"revoked"`

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

// MaxLinksPerSession bounds link creation.
const MaxLinksPerSession = 100

// Store is an in-memory link index.
type Store struct {
	mu        sync.RWMutex
	byHash    map[Hash]*Link
	byID      map[string]*Link
	bySession map[string]map[string]*Link
	now       func() time.Time

	// OnRevoke is invoked after a link is revoked so live viewers can be closed.
	OnRevoke func(sessionID, linkID string)
}

// NewStore creates an empty store.
func NewStore() *Store {
	return &Store{byHash: map[Hash]*Link{}, byID: map[string]*Link{}, bySession: map[string]map[string]*Link{}, now: func() time.Time { return time.Now().UTC() }}
}

// Create issues a new link and returns the record plus the plaintext token.
func (s *Store) Create(sessionID string, role session.Role, label string, ttl time.Duration) (*Link, string, error) {
	if !role.Valid() {
		return nil, "", ErrInvalidRole
	}
	if len(label) > 120 {
		label = label[:120]
	}
	tok, hash := NewToken()
	link := &Link{ID: session.NewID(), SessionID: sessionID, Label: label, Role: role, CreatedAt: s.now(), hash: hash}
	if ttl > 0 {
		exp := link.CreatedAt.Add(ttl)
		link.ExpiresAt = &exp
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.bySession[sessionID]) >= MaxLinksPerSession {
		return nil, "", ErrTooManyLinks
	}
	s.byHash[hash] = link
	s.byID[link.ID] = link
	if s.bySession[sessionID] == nil {
		s.bySession[sessionID] = map[string]*Link{}
	}
	s.bySession[sessionID][link.ID] = link
	return copyLink(link), tok, nil
}

// Resolve returns the live link for a presented token.
func (s *Store) Resolve(tok string) (*Link, error) {
	h := HashToken(tok)
	s.mu.RLock()
	link, ok := s.byHash[h]
	s.mu.RUnlock()
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

// Revoke marks a link unusable and notifies OnRevoke. It returns false when
// the link does not belong to sessionID.
func (s *Store) Revoke(sessionID, linkID string) bool {
	s.mu.Lock()
	link, ok := s.byID[linkID]
	if !ok || link.SessionID != sessionID {
		s.mu.Unlock()
		return false
	}
	already := link.Revoked
	link.Revoked = true
	s.mu.Unlock()
	if !already && s.OnRevoke != nil {
		s.OnRevoke(sessionID, linkID)
	}
	return true
}

// ListBySession returns links for a session, newest first.
func (s *Store) ListBySession(sessionID string) []*Link {
	s.mu.RLock()
	out := make([]*Link, 0, len(s.bySession[sessionID]))
	for _, l := range s.bySession[sessionID] {
		out = append(out, copyLink(l))
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// DeleteSession forgets every link of a session.
func (s *Store) DeleteSession(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, l := range s.bySession[sessionID] {
		delete(s.byHash, l.hash)
		delete(s.byID, id)
	}
	delete(s.bySession, sessionID)
}

func copyLink(l *Link) *Link {
	cp := *l
	return &cp
}
