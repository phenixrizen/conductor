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
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Kind     Kind     `json:"kind"`
	AgentID  string   `json:"agentId"`
	Command  []string `json:"command"`
	Cwd      string   `json:"cwd"`
	Status   Status   `json:"status"`
	ExitCode *int     `json:"exitCode,omitempty"`
	Cols     uint16   `json:"cols"`
	Rows     uint16   `json:"rows"`
	Viewers  int      `json:"viewers"`
	HostName string   `json:"hostName,omitempty"`
	// HostUser is the OS user running `conductor host` (hosted sessions).
	HostUser string `json:"hostUser,omitempty"`
	// Branch is the git branch of Cwd when it is inside a repository.
	Branch string `json:"branch,omitempty"`
	// Crew names the crew run the session is a member of; nil for a session
	// launched on its own.
	Crew *CrewRef `json:"crew,omitempty"`
	// Yolo says the session was launched with its agent's yolo recipe
	// applied (the agent has one, and yolo was on for the launch).
	Yolo bool `json:"yolo,omitempty"`
	// AgentSession is the agent's own session, when known (Resume).
	AgentSession *AgentSession `json:"agentSession,omitempty"`
	// ResumedFrom is the session this one resumed or relaunched.
	ResumedFrom string    `json:"resumedFrom,omitempty"`
	Attention   Attention `json:"attention"`
	// LastAnswer is who most recently cleared a needs-input prompt by typing.
	LastAnswer *Answer    `json:"lastAnswer,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	EndedAt    *time.Time `json:"endedAt,omitempty"`
}

// CrewRef ties a server session to the crew run it is a member of: the run,
// the saved crew it was launched from and the member's name in it.
type CrewRef struct {
	RunID  string `json:"runId"`
	CrewID string `json:"crewId"`
	Member string `json:"member"`
}

// Driver is the minimal interface the registry and API need from any session
// implementation (local PTY or hosted).
type Driver interface {
	Info() Info
	Stop(ctx context.Context) error
	DisconnectLink(linkID string)
	// LinkViewers counts attached clients per share link id.
	LinkViewers() map[string]int
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
