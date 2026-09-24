package session

import (
	"context"
	"errors"
	"time"
)

// Kind says where the PTY lives.
type Kind string

const (
	KindServer Kind = "server"
	KindHosted Kind = "hosted"
)

// Status is the session lifecycle state.
type Status string

const (
	StatusStarting         Status = "starting"
	StatusRunning          Status = "running"
	StatusExited           Status = "exited"
	StatusStopped          Status = "stopped"
	StatusHostDisconnected Status = "host_disconnected"
)

// Ended reports whether the process is gone for good.
func (s Status) Ended() bool { return s == StatusExited || s == StatusStopped }

// Role is what an attached client may do.
type Role string

const (
	RoleView    Role = "view"
	RoleControl Role = "control"
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool { return r == RoleView || r == RoleControl }

// Info is the public description of a session.
type Info struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Kind      Kind       `json:"kind"`
	AgentID   string     `json:"agentId"`
	Command   []string   `json:"command"`
	Cwd       string     `json:"cwd"`
	Status    Status     `json:"status"`
	ExitCode  *int       `json:"exitCode,omitempty"`
	Cols      uint16     `json:"cols"`
	Rows      uint16     `json:"rows"`
	Viewers   int        `json:"viewers"`
	HostName  string     `json:"hostName,omitempty"`
	Attention Attention  `json:"attention"`
	CreatedAt time.Time  `json:"createdAt"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
}

// Driver is the minimal interface the registry and API need from any session
// implementation (local PTY or hosted).
type Driver interface {
	Info() Info
	Stop(ctx context.Context) error
	DisconnectLink(linkID string)
}

// Typed errors surfaced to transports, which map them to close codes.
var (
	ErrReadOnly        = errors.New("session: read-only role")
	ErrSessionEnded    = errors.New("session: ended")
	ErrSlowConsumer    = errors.New("session: slow consumer")
	ErrRevoked         = errors.New("session: link revoked")
	ErrBadDimension    = errors.New("session: invalid terminal dimension")
	ErrTooManyViewers  = errors.New("session: too many viewers")
	ErrFileDenied      = errors.New("session: file access denied")
	ErrTooManyRequests = errors.New("session: too many in-flight requests")
)
