package dispatch

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Every write is a typed operation (Apply): the client mints an id, the
// store keeps the outcome under (actor, id), and the same id sent again
// with the same content gets the same receipt, never a second change;
// other content is a conflict. One transaction: the receipt is looked up,
// the entity's rev is compared with the one expected, the change is
// made, one changes row per field it set, added or removed, rev is
// bumped, the receipt is written, the commit, then OnChange. A refusal
// (the store's rules) is a failed receipt written in a transaction with
// nothing else, so the same id answers it again; a failure of the
// database itself, or a read-only store, writes nothing.

// OpIDPattern bounds an operation id a client mints: the shape of the
// ledger's command ids.
var OpIDPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{1,32}$`)

// Errors of an operation the store would not even look at: the API
// answers 400 invalid_request, as it answers a bad command id.
var (
	ErrBadOpID  = errors.New("dispatch: an operation id is 1 to 32 letters, digits, '.', '_', '~' or '-'")
	ErrBadActor = errors.New("dispatch: an operation names its actor")
)

// Op is what a client sends with a change: who asks (Actor: the admin's
// operations are kept under admin, as the task commands are, and an
// attempt's under agent:<member>), the id it minted, the entity's
// revision it expects (0 for none), and the display name recorded as by
// on the change rows, which travels in the body as byName does for a
// broadcast.
type Op struct {
	Actor       string
	ID          string
	ExpectedRev int64
	By          string
}

// The statuses of a receipt.
const (
	StatusDone   = "done"
	StatusFailed = "failed"
)

// The refusals a failed receipt names: the store's rules. read_only and
// op_conflict are answered without a row, the rest kept.
const (
	RefusalNotFound      = "not_found"
	RefusalInvalid       = "invalid"
	RefusalStaleRevision = "stale_revision"
	RefusalOpConflict    = "op_conflict"
	RefusalClosePolicy   = "close_policy"
	RefusalCycle         = "cycle"
	RefusalBound         = "bound"
	RefusalReadOnly      = "read_only"
	RefusalArchived      = "archived"
	RefusalRunLinked     = "run_linked"
	RefusalExpired       = "expired"
)

// Receipt is what an operation gets and what the store keeps under
// (actor, op id): the id and intent, the status (done, or failed with
// the refusal's name and its words), the subject's rev after the
// operation, the id and key of what a create made, what a close_policy
// refusal lacks (Missing), and whether this answer is the one given
// before (Repeat). It carries no entity, which the caller reads after
// the commit, so no receipt grows with a description; it is at most
// MaxReceipt bytes as JSON.
type Receipt struct {
	OpID    string   `json:"opId"`
	Intent  string   `json:"intent"`
	Status  string   `json:"status"`
	Rev     int64    `json:"rev,omitempty"`
	ID      string   `json:"id,omitempty"`
	Key     string   `json:"key,omitempty"`
	Repeat  bool     `json:"repeat,omitempty"`
	Error   string   `json:"error,omitempty"`
	Refusal string   `json:"refusal,omitempty"`
	Missing []string `json:"missing,omitempty"`
}

// MaxReceipt bounds a receipt's JSON; maxReceiptWords a refusal's words
// in it, in characters, so a refusal that quotes its input stays inside.
const (
	MaxReceipt      = 4 << 10
	maxReceiptWords = 1000
)

// Refusal is an operation refused by the store's rules: the code a
// failed receipt names, the words, and for close_policy what is missing.
// A change returns one; Apply keeps it as the failed receipt. Any other
// error a change returns is the database's, and nothing is written.
type Refusal struct {
	Code    string
	Words   string
	Missing []string
}

func (r *Refusal) Error() string { return r.Code + ": " + r.Words }

// refuse is a Refusal with the given code and words.
func refuse(code, format string, args ...any) error {
	return &Refusal{Code: code, Words: fmt.Sprintf(format, args...)}
}

// The entities a change row names. A set member (an assignee, label,
// relation, link or attachment) is a row of its ticket's field, not an
// entity of its own.
const (
	EntityProject   = "project"
	EntityTicket    = "ticket"
	EntityComment   = "comment"
	EntityLabel     = "label"
	EntityMilestone = "milestone"
	EntityView      = "view"
	EntityPerson    = "person"
)

// The ops of a change row: a scalar field set (value and prior), a set
// member added or removed (prior naming the tags of the adds a remove
// removes), an entity created (value the row's fields) or deleted, a
// merge's repair (Repair; derived, never shipped) and a migration's
// derived value.
const (
	OpSet       = "set"
	OpAdd       = "add"
	OpRemove    = "remove"
	OpCreate    = "create"
	OpDelete    = "delete"
	OpReconcile = "reconcile"
	OpMigrate   = "migrate"
)

// ChangeRow is one row of the changes log: an operation on one field of
// one entity. Seq is local to this database (the event's and the
// history's cursor, never replicated); (Actor, OpID, N) is the row's
// identity across replicas, N the row's place in its operation from 0;
// the one order of the log is (At, Actor, OpID, N). Value is a string
// field's text as it is and a structured field's JSON; Prior the value
// before a set, or for a remove the tags of the adds it removes.
type ChangeRow struct {
	Seq       int64     `json:"seq"`
	Entity    string    `json:"entity"`
	EntityID  string    `json:"entityId"`
	TicketID  string    `json:"ticketId,omitempty"`
	ProjectID string    `json:"projectId,omitempty"`
	Field     string    `json:"field,omitempty"`
	Op        string    `json:"op"`
	Value     string    `json:"value"`
	Prior     string    `json:"prior,omitempty"`
	At        time.Time `json:"at"`
	Actor     string    `json:"actor"`
	By        string    `json:"by,omitempty"`
	OpID      string    `json:"opId"`
	N         int       `json:"n"`
}

// MaxChangeValue bounds a change row's value and prior: a text value
// meets it by its field's bound, a structured one by its shape.
const MaxChangeValue = 64 << 10

// maxReceipts is how many receipts the store keeps: the newest, prune
// dropping the rest. A resend older than that is answered by the changes
// log, which keeps the operation's identity for good.
const maxReceipts = 20000

// revTables names the table of each entity with a rev column, and
// whether the row has an updated_at to move with it.
var revTables = map[string]struct {
	table   string
	updated bool
}{
	EntityProject:   {"projects", true},
	EntityTicket:    {"tickets", true},
	EntityComment:   {"comments", false},
	EntityLabel:     {"labels", false},
	EntityMilestone: {"milestones", true},
	EntityView:      {"views", true},
}

// tx is one operation's transaction: what a change writes through. It
// holds the operation, one time for all it writes, the next change row's
// n, the entities the rows named (for the rev bumps and OnChange), and
// what a create made.
type tx struct {
	ctx    context.Context
	tx     *sql.Tx
	now    time.Time
	op     Op
	intent string
	n      int
	seq    int64
	// touched is every (entity, id) the change rows named, in order; the
	// first is the operation's subject, whose rev the receipt carries.
	touched []touched
	keys    map[string]string // project id → key, resolved once per operation
	id, key string
}

// touched is an entity a change row named, with the ticket and the
// project it belongs to (the project by its key).
type touched struct {
	entity, id, ticket, project string
}

// Now is the transaction's time, one for all it writes.
func (t *tx) Now() time.Time { return t.now }

// expect compares the entity's rev, read inside the transaction, with
// the one the operation expects: stale_revision when they differ and one
// was given. A change calls it before it writes anything.
func (t *tx) expect(rev int64) error {
	if t.op.ExpectedRev > 0 && rev != t.op.ExpectedRev {
		return refuse(RefusalStaleRevision, "the revision expected is %d; it is %d", t.op.ExpectedRev, rev)
	}
	return nil
}

// created names what the operation made, for the receipt.
func (t *tx) created(id, key string) { t.id, t.key = id, key }

// record writes one changes row for a field of an entity, next in the
// operation (n), at the transaction's time and under its actor, by and
// op id, and marks the entity for the rev bump and OnChange. A row whose
// identity the log holds is refused by the index, and nothing of the
// operation is kept.
func (t *tx) record(c ChangeRow) error {
	if c.Entity == "" || c.EntityID == "" || c.Op == "" {
		return errors.New("dispatch: a change row names its entity, its id and its op")
	}
	if len(c.Value) > MaxChangeValue || len(c.Prior) > MaxChangeValue {
		return fmt.Errorf("dispatch: a change row of %s is more than %d bytes", c.Field, MaxChangeValue)
	}
	res, err := t.tx.ExecContext(t.ctx, `INSERT INTO changes (entity, entity_id, ticket_id, project_id, field, op, value, prior, at, actor, by, op_id, n)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Entity, c.EntityID, c.TicketID, c.ProjectID, c.Field, c.Op, c.Value, c.Prior, stamp(t.now), t.op.Actor, t.op.By, t.op.ID, t.n)
	if err != nil {
		return err
	}
	seq, err := res.LastInsertId()
	if err != nil {
		return err
	}
	t.seq, t.n = seq, t.n+1
	key := ""
	if c.ProjectID != "" {
		if key, err = t.projectKey(c.ProjectID); err != nil {
			return err
		}
	}
	for _, e := range t.touched {
		if e.entity == c.Entity && e.id == c.EntityID {
			return nil
		}
	}
	t.touched = append(t.touched, touched{c.Entity, c.EntityID, c.TicketID, key})
	return nil
}

// projectKey is the key of the project with the given id, read once per
// operation.
func (t *tx) projectKey(id string) (string, error) {
	if key, ok := t.keys[id]; ok {
		return key, nil
	}
	var key string
	if err := t.tx.QueryRowContext(t.ctx, "SELECT key FROM projects WHERE id = ?", id).Scan(&key); err != nil {
		if isNoRows(err) {
			return "", fmt.Errorf("dispatch: a change row names project %s, which has no row", id)
		}
		return "", err
	}
	if t.keys == nil {
		t.keys = map[string]string{}
	}
	t.keys[id] = key
	return key, nil
}

// bump bumps the rev of every entity the change rows named, once per
// operation whatever it set, moving its updated_at with it where the row
// has one, and reads back the subject's rev for the receipt.
func (t *tx) bump() (int64, error) {
	var rev int64
	for i, e := range t.touched {
		rt, ok := revTables[e.entity]
		if !ok {
			continue
		}
		stmt := "UPDATE " + rt.table + " SET rev = rev + 1 WHERE id = ?"
		args := []any{e.id}
		if rt.updated {
			stmt = "UPDATE " + rt.table + " SET rev = rev + 1, updated_at = ? WHERE id = ?"
			args = []any{stamp(t.now), e.id}
		}
		res, err := t.tx.ExecContext(t.ctx, stmt, args...)
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return 0, fmt.Errorf("dispatch: a change row names %s %s, which has no row", e.entity, e.id)
		}
		if i == 0 {
			if err := t.tx.QueryRowContext(t.ctx, "SELECT rev FROM "+rt.table+" WHERE id = ?", e.id).Scan(&rev); err != nil {
				return 0, err
			}
		}
	}
	return rev, nil
}

// changes is what the operation changed, for OnChange: one per entity,
// with the commit's last seq.
func (t *tx) changes() []Change {
	out := make([]Change, 0, len(t.touched))
	for _, e := range t.touched {
		out = append(out, Change{Seq: t.seq, Entity: e.entity, ID: e.id, Ticket: e.ticket, Project: e.project})
	}
	return out
}

// fingerprint is an operation's content, for the conflict check: the
// SHA-256 of the intent and the arguments as JSON.
func fingerprint(intent string, args any) (string, error) {
	body, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write([]byte(intent))
	h.Write([]byte{0})
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Apply runs one operation: op names its actor and id, intent and args
// are its content (the fingerprint), and change makes it inside the
// transaction, through the tx's record, expect and created. The receipt
// comes back as the store keeps it: a repeat when the same id and content
// were answered before (Repeat set), op_conflict for the same id with
// other content (no row), a refusal change returned (a failed receipt,
// kept), else done with the subject's rev after the operation and what a
// create made. A resend whose receipt is pruned is answered by the
// changes log: a row (actor, op id, 0) means the operation ran, and the
// resend is answered done and Repeat with no rev, id or key whatever its
// content; none means the receipt was a refusal's, and the resend runs
// as a new operation. An error is the database's own, a bad op id or
// actor, or the store closed: nothing was written, and nothing is
// answered; a read-only store answers a read_only refusal, written
// nowhere. OnChange's listeners run after the commit, on this goroutine.
func (s *Store) Apply(ctx context.Context, op Op, intent string, args any, change func(*tx) error) (Receipt, error) {
	if !OpIDPattern.MatchString(op.ID) {
		return Receipt{}, ErrBadOpID
	}
	if op.Actor == "" {
		return Receipt{}, ErrBadActor
	}
	fp, err := fingerprint(intent, args)
	if err != nil {
		return Receipt{}, err
	}
	s.mu.Lock()
	rc, changed, err := s.apply(ctx, op, intent, fp, change)
	s.mu.Unlock()
	if err != nil {
		return Receipt{}, err
	}
	if len(changed) > 0 {
		s.notify(changed)
	}
	return rc, nil
}

// apply is Apply under the store's lock.
func (s *Store) apply(ctx context.Context, op Op, intent, fp string, change func(*tx) error) (Receipt, []Change, error) {
	if s.closed {
		return Receipt{}, nil, ErrClosed
	}
	if s.readOnly || s.db == nil {
		return Receipt{OpID: op.ID, Intent: intent, Status: StatusFailed, Refusal: RefusalReadOnly, Error: words(s.why)}, nil, nil
	}
	dbtx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Receipt{}, nil, cancelledAs(ctx, err)
	}
	defer dbtx.Rollback()
	// The receipt, when the id was answered before.
	var priorIntent, priorFp, priorBody string
	switch err := dbtx.QueryRowContext(ctx, "SELECT intent, fingerprint, receipt FROM ops WHERE actor = ? AND id = ?", op.Actor, op.ID).Scan(&priorIntent, &priorFp, &priorBody); {
	case err == nil:
		if priorIntent != intent || priorFp != fp {
			return Receipt{OpID: op.ID, Intent: intent, Status: StatusFailed, Refusal: RefusalOpConflict, Error: "the operation id was used before with other content"}, nil, nil
		}
		var rc Receipt
		if err := json.Unmarshal([]byte(priorBody), &rc); err != nil {
			return Receipt{}, nil, fmt.Errorf("dispatch: the receipt of %s: %w", op.ID, err)
		}
		rc.Repeat = true
		return rc, nil, nil
	case isNoRows(err):
	default:
		return Receipt{}, nil, cancelledAs(ctx, err)
	}
	// The log, when the receipt was pruned: the operation ran.
	var ran int
	if err := dbtx.QueryRowContext(ctx, "SELECT COUNT(*) FROM changes WHERE actor = ? AND op_id = ? AND n = 0", op.Actor, op.ID).Scan(&ran); err != nil {
		return Receipt{}, nil, cancelledAs(ctx, err)
	}
	if ran > 0 {
		return Receipt{OpID: op.ID, Intent: intent, Status: StatusDone, Repeat: true}, nil, nil
	}
	t := &tx{ctx: ctx, tx: dbtx, now: time.Now().UTC(), op: op, intent: intent}
	if err := change(t); err != nil {
		var ref *Refusal
		if !errors.As(err, &ref) {
			return Receipt{}, nil, cancelledAs(ctx, err)
		}
		// Refused: nothing of the change is kept, and the failed receipt
		// is written in a transaction with nothing else.
		if err := dbtx.Rollback(); err != nil {
			return Receipt{}, nil, cancelledAs(ctx, err)
		}
		rc := Receipt{OpID: op.ID, Intent: intent, Status: StatusFailed, Refusal: ref.Code, Error: words(ref.Words), Missing: ref.Missing}
		if err := s.writeReceipt(ctx, op, intent, fp, t.now, rc); err != nil {
			return Receipt{}, nil, err
		}
		return rc, nil, nil
	}
	rev, err := t.bump()
	if err != nil {
		return Receipt{}, nil, cancelledAs(ctx, err)
	}
	rc := Receipt{OpID: op.ID, Intent: intent, Status: StatusDone, Rev: rev, ID: t.id, Key: t.key}
	if err := insertReceipt(ctx, dbtx, op, intent, fp, t.now, rc); err != nil {
		return Receipt{}, nil, cancelledAs(ctx, err)
	}
	if s.beforeCommit != nil {
		if err := s.beforeCommit(); err != nil {
			return Receipt{}, nil, cancelledAs(ctx, err)
		}
	}
	if err := dbtx.Commit(); err != nil {
		return Receipt{}, nil, cancelledAs(ctx, err)
	}
	return rc, t.changes(), nil
}

// writeReceipt keeps a refusal's receipt in a transaction of its own.
func (s *Store) writeReceipt(ctx context.Context, op Op, intent, fp string, now time.Time, rc Receipt) error {
	dbtx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return cancelledAs(ctx, err)
	}
	defer dbtx.Rollback()
	if err := insertReceipt(ctx, dbtx, op, intent, fp, now, rc); err != nil {
		return cancelledAs(ctx, err)
	}
	if err := dbtx.Commit(); err != nil {
		return cancelledAs(ctx, err)
	}
	return nil
}

// insertReceipt writes the ops row of an operation.
func insertReceipt(ctx context.Context, dbtx *sql.Tx, op Op, intent, fp string, now time.Time, rc Receipt) error {
	body, err := json.Marshal(rc)
	if err != nil {
		return err
	}
	if len(body) > MaxReceipt {
		return fmt.Errorf("dispatch: the receipt of %s is %d bytes, more than %d", op.ID, len(body), MaxReceipt)
	}
	_, err = dbtx.ExecContext(ctx, `INSERT INTO ops (actor, id, intent, fingerprint, status, receipt, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		op.Actor, op.ID, intent, fp, rc.Status, string(body), stamp(now))
	return err
}

// words cuts a refusal's words to what a receipt may carry.
func words(s string) string {
	rs := []rune(s)
	if len(rs) <= maxReceiptWords {
		return s
	}
	return string(rs[:maxReceiptWords]) + "…"
}

// cancelledAs is err as the operation's caller should see it: the
// context's own error when the context ended under the transaction,
// whatever database/sql said of it (its rollback of a cancelled
// transaction races the commit, which then says sql.ErrTxDone), so that
// a stop is told from a store that failed; err itself otherwise.
func cancelledAs(ctx context.Context, err error) error {
	if cerr := ctx.Err(); cerr != nil && (errors.Is(err, sql.ErrTxDone) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return fmt.Errorf("dispatch: %w", cerr)
	}
	return err
}

// Receipt reads an operation's receipt without acting: found is false
// when the store holds none (never given, or pruned).
func (s *Store) Receipt(ctx context.Context, actor, id string) (rc Receipt, found bool, err error) {
	db, err := s.conn()
	if err != nil {
		return Receipt{}, false, err
	}
	var body string
	switch err := db.QueryRowContext(ctx, "SELECT receipt FROM ops WHERE actor = ? AND id = ?", actor, id).Scan(&body); {
	case isNoRows(err):
		return Receipt{}, false, nil
	case err != nil:
		return Receipt{}, false, err
	}
	if err := json.Unmarshal([]byte(body), &rc); err != nil {
		return Receipt{}, false, fmt.Errorf("dispatch: the receipt of %s: %w", id, err)
	}
	return rc, true, nil
}

// prune drops every receipt but the newest maxReceipts, by their time,
// and reports how many went; it runs at open and, later, on the store's
// own schedule (PR-4). now is the maintenance clock those callers share
// with the sweep (PR-7); the pruning is by count and reads nothing of it.
func (s *Store) prune(ctx context.Context, now time.Time) (int, error) {
	_ = now
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.closed:
		return 0, ErrClosed
	case s.db == nil || s.readOnly:
		return 0, fmt.Errorf("%w: %s", ErrReadOnly, s.why)
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM ops WHERE rowid IN (SELECT rowid FROM ops ORDER BY created_at DESC, actor DESC, id DESC LIMIT -1 OFFSET ?)`, maxReceipts)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	s.pruned.Add(n)
	return int(n), nil
}
