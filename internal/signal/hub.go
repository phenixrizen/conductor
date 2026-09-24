package signal

import (
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
	"github.com/phenixrizen/conductor/internal/share"
)

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

// Hub indexes hosted sessions by ID.
type Hub struct {
	mu       sync.Mutex
	registry *session.Registry
	sessions map[string]*HostedSession
	log      *slog.Logger
	// MaxViewers caps viewers per hosted session.
	MaxViewers int
	// OnChange is called after a hosted session's Info changes.
	OnChange func(session.Info)
}

// NewHub creates a hub that registers hosted sessions in registry.
func NewHub(registry *session.Registry, log *slog.Logger) *Hub {
	if log == nil {
		log = slog.Default()
	}
	return &Hub{registry: registry, sessions: map[string]*HostedSession{}, log: log, MaxViewers: 32}
}

// Errors returned by Register.
var (
	ErrBadRegister = errors.New("signal: invalid registration")
	ErrBadResume   = errors.New("signal: cannot resume session")
)

// Register creates a hosted session (or resumes one) and attaches conn.
func (hub *Hub) Register(reg proto.Register, conn *HostConn) (*HostedSession, bool, error) {
	if reg.Proto != proto.ProtoVersion {
		return nil, false, ErrBadRegister
	}
	if len(reg.Session.Command) == 0 || strings.TrimSpace(reg.Session.Command[0]) == "" {
		return nil, false, ErrBadRegister
	}
	if len(reg.Session.Name) > 120 || len(reg.Host.Name) > 120 || len(reg.Session.Cwd) > 4096 {
		return nil, false, ErrBadRegister
	}
	if reg.Resume != nil {
		hub.mu.Lock()
		hs, ok := hub.sessions[reg.Resume.SessionID]
		hub.mu.Unlock()
		if !ok || !share.Equal(reg.Resume.Secret, hs.secret) {
			return nil, false, ErrBadResume
		}
		hs.mu.Lock()
		if hs.conn != nil {
			hs.mu.Unlock()
			return nil, false, ErrBadResume
		}
		hs.conn = conn
		if !hs.info.Status.Ended() {
			hs.info.Status = session.StatusRunning
		}
		hs.info.Cols, hs.info.Rows = reg.Session.Cols, reg.Session.Rows
		hs.relayOnly = reg.Session.RelayOnly
		if reg.Session.AgentToken != "" {
			hs.setAgentToken(reg.Session.AgentToken)
		}
		hs.mu.Unlock()
		hs.notifyChange()
		return hs, true, nil
	}
	secret, _ := share.NewToken()
	name := strings.TrimSpace(reg.Session.Name)
	if name == "" {
		name = reg.Session.Command[0] + " @ " + reg.Host.Name
	}
	hs := &HostedSession{
		hub:        hub,
		secret:     secret,
		log:        hub.log,
		conn:       conn,
		relayOnly:  reg.Session.RelayOnly,
		viewers:    map[string]*Viewer{},
		maxViewers: hub.MaxViewers,
		info: session.Info{
			ID:        session.NewID(),
			Name:      name,
			Kind:      session.KindHosted,
			AgentID:   reg.Session.AgentID,
			Command:   reg.Session.Command,
			Cwd:       reg.Session.Cwd,
			Status:    session.StatusRunning,
			Cols:      reg.Session.Cols,
			Rows:      reg.Session.Rows,
			HostName:  reg.Host.Name,
			CreatedAt: time.Now().UTC(),
		},
	}
	hs.setAgentToken(reg.Session.AgentToken)
	if err := hub.registry.Add(hs); err != nil {
		return nil, false, err
	}
	hub.mu.Lock()
	hub.sessions[hs.info.ID] = hs
	hub.mu.Unlock()
	hs.notifyChange()
	return hs, false, nil
}

// Get returns the hosted session with the given ID.
func (hub *Hub) Get(id string) (*HostedSession, bool) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hs, ok := hub.sessions[id]
	return hs, ok
}

// Expire removes sessions whose host has been gone longer than the grace
// period, and forgets ended sessions that the registry already dropped.
func (hub *Hub) Expire(now time.Time) {
	hub.mu.Lock()
	var expired []string
	for id, hs := range hub.sessions {
		hs.mu.Lock()
		gone := hs.conn == nil && !hs.disconnectedAt.IsZero() && now.Sub(hs.disconnectedAt) >= DisconnectGrace
		hs.mu.Unlock()
		if _, inRegistry := hub.registry.Get(id); gone || !inRegistry {
			expired = append(expired, id)
		}
	}
	for _, id := range expired {
		delete(hub.sessions, id)
	}
	hub.mu.Unlock()
	for _, id := range expired {
		if _, ok := hub.registry.Get(id); ok {
			hub.log.Info("hosted session expired", "session", id)
			hub.registry.Remove(id)
		}
	}
}

// CloseAll disconnects every host and viewer (server shutdown).
func (hub *Hub) CloseAll() {
	hub.mu.Lock()
	list := make([]*HostedSession, 0, len(hub.sessions))
	for _, hs := range hub.sessions {
		list = append(list, hs)
	}
	hub.mu.Unlock()
	for _, hs := range list {
		hs.CloseViewers(errors.New("server shutting down"))
		hs.mu.Lock()
		conn := hs.conn
		hs.mu.Unlock()
		if conn != nil {
			conn.Close()
		}
	}
}
