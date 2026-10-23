package ledger

import "time"

// The states a row can be in. A run's state is the durable one: the engine
// derives what it shows from the rows and the sessions. An attempt's
// states run reserved → launched → running → submitting → verifying →
// succeeded | failed, or end in cancelled, stopped, interrupted (its process
// went with the owner) or uncertain (the owner stopped before it knew
// whether a side effect happened). A check's run started → running →
// finished, or interrupted. A command is accepted, then done, failed or
// uncertain.
const (
	RunRunning     = "running"
	RunStopped     = "stopped"
	RunFinished    = "finished"
	RunInterrupted = "interrupted"

	OutcomeSucceeded = "succeeded"
	OutcomeFailed    = "failed"
	OutcomeCancelled = "cancelled"

	TaskBlocked   = "blocked"
	TaskReady     = "ready"
	TaskRunning   = "running"
	TaskVerifying = "verifying"
	TaskSucceeded = "succeeded"
	TaskFailed    = "failed"
	TaskCancelled = "cancelled"

	AttemptReserved    = "reserved"
	AttemptLaunched    = "launched"
	AttemptRunning     = "running"
	AttemptSubmitting  = "submitting"
	AttemptVerifying   = "verifying"
	AttemptSucceeded   = "succeeded"
	AttemptFailed      = "failed"
	AttemptCancelled   = "cancelled"
	AttemptStopped     = "stopped"
	AttemptInterrupted = "interrupted"
	AttemptUncertain   = "uncertain"

	CheckStarted     = "started"
	CheckRunning     = "running"
	CheckFinished    = "finished"
	CheckInterrupted = "interrupted"

	CommandAccepted  = "accepted"
	CommandDone      = "done"
	CommandFailed    = "failed"
	CommandUncertain = "uncertain"
)

// RunRow is a managed run.
type RunRow struct {
	ID       string
	CrewID   string
	Crew     string // the crew as launched, JSON
	Base     string // the commit the run started from
	State    string
	Outcome  string
	Paused   bool
	Revision int64 // bumped by every transition; the run's revision on the API
	// ContinuedFrom names the run this one continues (a resume after a
	// restart), ContinuedBy the run that continues this one.
	ContinuedFrom string
	ContinuedBy   string
	CreatedAt     time.Time
	ClosedAt      time.Time // zero while the run is open
}

// TaskRow is a task of a run.
type TaskRow struct {
	RunID         string
	ID            string
	Member        string
	Kind          string
	State         string
	Reason        string
	Attempts      int    // attempts made so far, continuations' included
	ResultAttempt int    // the attempt whose result stands, 0 for none
	ResultCommit  string // its commit
	Verdict       string // a review's
	ImportedFrom  string // "<run>/<task>/<n>" when a continuation imported the result
}

// AttemptRow is one try at a task.
type AttemptRow struct {
	RunID        string
	TaskID       string
	N            int
	Member       string
	SessionID    string
	Worktree     string
	Branch       string
	StartCommit  string
	ResultCommit string
	State        string
	Stage        string // what the attempt was doing when it failed or was cut short
	Error        string
	ReservedAt   time.Time
	LaunchedAt   time.Time
	PromptedAt   time.Time
	ClosedAt     time.Time
}

// CheckRow is one run of a check against an attempt's result: its receipt.
type CheckRow struct {
	RunID            string
	TaskID           string
	N                int
	ID               string
	K                int // the k-th run of this check on this attempt
	Argv             []string
	Dir              string
	Subject          string // the commit checked
	State            string
	PID              int
	Group            int
	ProcessStartedAt time.Time
	StartedAt        time.Time
	FinishedAt       time.Time
	Exit             int
	Signal           string
	TimedOut         bool
	Killed           bool
	Stable           bool
	Truncated        bool
	Total            int64
	Log              []byte // at most MaxLog bytes
	LogSHA256        string
	Tail             string
}

// CommandRow is a command's receipt: what was asked, by whom, and what
// became of it.
type CommandRow struct {
	RunID       string
	Actor       string
	ID          string
	Intent      string
	Fingerprint string
	Status      string
	Receipt     string // JSON, what the caller is told
	CreatedAt   time.Time
}

// EventRow is one event of a run's sequence.
type EventRow struct {
	RunID   string
	Seq     int64
	At      time.Time
	Kind    string
	Payload string // JSON
}

// Bounds.
const (
	// MaxLog bounds a check's log blob.
	MaxLog = 1 << 20
	// MaxPayload bounds an event's payload and a command's receipt.
	MaxPayload = 64 << 10
	// MaxCrew bounds the crew JSON kept with a run.
	MaxCrew = 1 << 20
)

// maxCommands bounds the commands kept per run: past it a new one is
// refused, since dropping an old receipt would let its resend run twice.
// A variable for the tests.
var maxCommands = 4096

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func unstamp(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
