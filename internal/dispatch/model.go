package dispatch

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// The row types and every bound of the schema (docs/plans/dispatch.md,
// the schema table). Each bound is a named constant here and nowhere
// else; every text field's rule is one entry of fieldRules, which
// checkField applies and model_test.go walks at each bound and one past
// it. A bound is checked on a local write alone: a merge never refuses a
// replicated add (the field rules), so a set may stand past its bound
// after one, and it refuses further local adds until it is under the
// bound again (checkCount).

// The bounds of a project. A character is a Unicode code point; a KiB is
// 1,024 bytes of UTF-8.
const (
	// MaxProjectName bounds a project's name, in characters.
	MaxProjectName = 60
	// MaxProjectDescription bounds a project's description (Markdown), in
	// bytes.
	MaxProjectDescription = 16 << 10
	// MaxCwd bounds a project's folder, in bytes: a path kept as text and
	// resolved by nothing here, only when something launches from it
	// (Program item DS2).
	MaxCwd = 4096
	// MaxTrackerURL bounds an external tracker's URL, in bytes.
	MaxTrackerURL = 2 << 10
)

// The bound of a person.
const (
	// MaxPersonName bounds a person's name, in characters.
	MaxPersonName = 60
)

// The bounds of a ticket.
const (
	// MaxTicketTitle bounds a ticket's title, in characters.
	MaxTicketTitle = 200
	// MaxTicketDescription bounds a ticket's description (Markdown), in
	// bytes.
	MaxTicketDescription = 64 << 10
	// MaxAcceptance bounds a ticket's acceptance criteria (Markdown), in
	// bytes.
	MaxAcceptance = 16 << 10
	// MaxAcceptedNote bounds an acceptance's note (Markdown), in
	// characters.
	MaxAcceptedNote = 1000
	// MaxPriority is the highest priority (urgent); none is 0.
	MaxPriority = 4
	// MaxAssignees bounds a ticket's assignees.
	MaxAssignees = 8
	// MaxTicketLabels bounds a ticket's labels.
	MaxTicketLabels = 20
)

// The bounds of a label, a milestone, a relation and a comment.
const (
	// MaxLabelName bounds a label's name, in characters.
	MaxLabelName = 40
	// MaxLabels bounds a project's live labels.
	MaxLabels = 100
	// MaxMilestoneName bounds a milestone's name, in characters.
	MaxMilestoneName = 60
	// MaxMilestoneDescription bounds a milestone's description
	// (Markdown), in bytes.
	MaxMilestoneDescription = 4 << 10
	// MaxMilestones bounds a project's live milestones.
	MaxMilestones = 100
	// MaxRelations bounds a ticket's live relations whose other end is
	// live: one to a deleted ticket is inactive and counts for nothing.
	MaxRelations = 100
	// MaxCommentBody bounds a comment's body (Markdown), in bytes.
	MaxCommentBody = 16 << 10
	// MaxComments bounds a ticket's comments.
	MaxComments = 2000
)

// The bounds of an attachment, a link and a view.
const (
	// MaxAttachmentName bounds an attachment's name, in bytes.
	MaxAttachmentName = 255
	// MaxContentType bounds an attachment's content type as given, in
	// bytes.
	MaxContentType = 100
	// MaxAttachmentSize bounds an attachment's content, in bytes.
	MaxAttachmentSize = 16 << 20
	// MaxAttachments bounds a ticket's attachments.
	MaxAttachments = 50
	// MaxLinkRef bounds a link's reference, in bytes.
	MaxLinkRef = 2 << 10
	// MaxLinkURL bounds a link's URL, in bytes.
	MaxLinkURL = 2 << 10
	// MaxLinkNote bounds a link's note (Markdown), in characters.
	MaxLinkNote = 1000
	// MaxLinks bounds a ticket's live links.
	MaxLinks = 50
	// MaxViewName bounds a saved view's name, in characters.
	MaxViewName = 60
	// MaxViewQuery bounds a saved view's query (JSON), in bytes, as
	// stored.
	MaxViewQuery = 8 << 10
	// MaxViews bounds the live saved views.
	MaxViews = 200
)

// The bounds of the log, the receipts, the hierarchy and an operation.
const (
	// MaxChangeValue bounds a change row's value and prior, in bytes: a
	// text value meets it by its field's bound, a structured one by its
	// shape (the largest, a link's add, is under 16 KiB with every field
	// at its bound, which model_test.go measures), and a remove's prior,
	// the tags of the adds it removes, by going into as many rows as it
	// needs (recordRemove).
	MaxChangeValue = 64 << 10
	// MaxReceipt bounds a receipt's JSON, a repeat's answer included.
	MaxReceipt = 4 << 10
	// maxReceiptWords bounds a refusal's words in a receipt, in
	// characters; they are cut further by their size as JSON (fitWords)
	// when the receipt would not fit MaxReceipt, so a refusal that quotes
	// its input, or a read-only store's why, stays inside.
	maxReceiptWords = 1000
	// maxReceipts is how many receipts the store keeps: the newest,
	// prune dropping the rest. A resend older than that is answered by
	// the changes log, which keeps the operation's identity for good.
	maxReceipts = 20000
	// MaxParentDepth is how long a parent chain may be, in tickets, from
	// the deepest ticket of a subtree to the root.
	MaxParentDepth = 8
	// MaxByName bounds the display name an operation records as by, and
	// a row as created_by, in characters: a person's name's bound.
	MaxByName = 60
	// MaxActor bounds an operation's actor, in bytes: admin, merge, and
	// agent:<member> at most 46 (a crew member's name is at most 40
	// characters). Every change row records it, and a member's add
	// carries it in the tag a later remove names in its prior, so an
	// actor with no bound could make that remove too large to write, and
	// its member impossible to remove. It is at most MaxByName, so an
	// actor a row records as created_by in place of a display name
	// (Op.author) meets that field's bound too.
	MaxActor = 60
)

// A ticket's statuses (Owner decisions, 4: six, fixed in this item).
// Any status may follow any other, but done is entered only when the
// project's close policy is met (EditTicket's gate, acceptance.go); a
// move to done or cancelled sets closed_at, a move out of them clears
// it.
const (
	TicketBacklog    = "backlog"
	TicketReady      = "ready"
	TicketInProgress = "in_progress"
	TicketInReview   = "in_review"
	TicketDone       = "done"
	TicketCancelled  = "cancelled"
)

// Statuses is a ticket's statuses in a board's order.
var Statuses = []string{TicketBacklog, TicketReady, TicketInProgress, TicketInReview, TicketDone, TicketCancelled}

// closed reports whether a status closes its ticket.
func closed(status string) bool { return status == TicketDone || status == TicketCancelled }

// Priorities names a ticket's priorities by their value, 0 (none) to
// MaxPriority (urgent).
var Priorities = []string{"none", "low", "medium", "high", "urgent"}

// A ticket's horizons, the roadmap's columns: Now, Next, Later, and ""
// for not planned.
const (
	HorizonNone  = ""
	HorizonNow   = "now"
	HorizonNext  = "next"
	HorizonLater = "later"
)

// Horizons is the horizons in a roadmap's order, not planned first.
var Horizons = []string{HorizonNone, HorizonNow, HorizonNext, HorizonLater}

// A project's trackers: native, or a pointer at an external one by its
// URL (Owner decisions, 12: a kind and a URL in this item).
const (
	TrackerNative   = "native"
	TrackerExternal = "external"
)

// What a project's close policy may require before a ticket is done:
// the owner's acceptance, a delivery link. verified is refused until
// Program item DS2 defines it (Owner decisions, 6).
const (
	RequireAcceptance = "acceptance"
	RequireDelivery   = "delivery"
	requireVerified   = "verified"
)

// CloseRequirements is the requirements in their order in a policy.
var CloseRequirements = []string{RequireAcceptance, RequireDelivery}

// The kinds of a relation: "depends on" is blocks read from the other
// end.
const (
	RelationBlocks  = "blocks"
	RelationRelates = "relates"
)

// RelationKinds is the kinds of a relation.
var RelationKinds = []string{RelationBlocks, RelationRelates}

// LabelColors is the twelve colours a label may have, by name: the UI
// maps each to one of the brand's scales (docs/design/brand.md) at a
// shade, so the store keeps a name and never a value a client chose.
var LabelColors = []string{"gray", "brown", "red", "orange", "amber", "yellow", "lime", "green", "teal", "cyan", "blue", "indigo"}

// The kinds of a link (DS2 adds plan, candidate, check and finding, DR1
// decision).
const (
	LinkDelivery = "delivery"
	LinkRun      = "run"
	LinkExternal = "external"
)

// LinkKinds is the kinds of a link.
var LinkKinds = []string{LinkDelivery, LinkRun, LinkExternal}

// The kinds of a saved view.
const (
	ViewList    = "list"
	ViewBoard   = "board"
	ViewRoadmap = "roadmap"
)

// The states of a milestone.
const (
	MilestoneOpen   = "open"
	MilestoneClosed = "closed"
)

// targetLayout is a milestone's target date: YYYY-MM-DD.
const targetLayout = "2006-01-02"

// Project is a project's row: its key (CND), chosen at creation and
// never changed; its name, its description (Markdown) and its folder;
// its tracker, native or a pointer at an external one by its URL; its
// close policy (CloseRequires); the number its next ticket takes and the
// range reserved for replication (Program item CS1: 0 and 0 here, the
// whole range this store's); ArchivedAt when it is archived, which
// refuses every write under it. Projects are archived, never deleted.
type Project struct {
	ID            string    `json:"id"`
	Key           string    `json:"key"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	Cwd           string    `json:"cwd"`
	Tracker       string    `json:"tracker"`
	TrackerURL    string    `json:"trackerUrl"`
	CloseRequires []string  `json:"closeRequires"`
	NextNumber    int64     `json:"nextNumber"`
	NumberFrom    int64     `json:"numberFrom"`
	NumberTo      int64     `json:"numberTo"`
	ArchivedAt    time.Time `json:"archivedAt,omitzero"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
	Rev           int64     `json:"rev"`
}

// Ticket is a ticket's row and its two sets: its key (CND-12), never
// reassigned and kept by a delete; its title, description and
// acceptance criteria (Markdown); its status, priority (0 to
// MaxPriority) and horizon; its accountable owner (a person's name, or
// ""), apart from its assignees (person:<name> or agent:<catalog id>);
// its parent and milestone (ids of its project's rows, or ""); the
// acceptance recorded for its owner against its criteria as they stood
// (AcceptTicket); its source (native; Program item IN1a adds its own
// and fills the four columns it reserves); when it was closed and
// deleted (a tombstone, whose references stay, inactive, until its
// undelete).
// Labels are the ids of its live labels.
type Ticket struct {
	ID               string    `json:"id"`
	ProjectID        string    `json:"projectId"`
	Number           int64     `json:"number"`
	Key              string    `json:"key"`
	Title            string    `json:"title"`
	Description      string    `json:"description"`
	Acceptance       string    `json:"acceptance"`
	Status           string    `json:"status"`
	Priority         int       `json:"priority"`
	Horizon          string    `json:"horizon"`
	Owner            string    `json:"owner"`
	ParentID         string    `json:"parentId"`
	MilestoneID      string    `json:"milestoneId"`
	AcceptedAt       time.Time `json:"acceptedAt,omitzero"`
	AcceptedBy       string    `json:"acceptedBy,omitempty"`
	AcceptedFor      string    `json:"acceptedFor,omitempty"`
	AcceptedCriteria string    `json:"acceptedCriteria,omitempty"`
	AcceptedNote     string    `json:"acceptedNote,omitempty"`
	Source           string    `json:"source"`
	ExternalID       string    `json:"externalId,omitempty"`
	ExternalURL      string    `json:"externalUrl,omitempty"`
	ExternalSnapshot string    `json:"externalSnapshot,omitempty"`
	Authority        string    `json:"authority,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
	CreatedBy        string    `json:"createdBy"`
	UpdatedAt        time.Time `json:"updatedAt"`
	ClosedAt         time.Time `json:"closedAt,omitzero"`
	DeletedAt        time.Time `json:"deletedAt,omitzero"`
	Rev              int64     `json:"rev"`
	Assignees        []string  `json:"assignees"`
	Labels           []string  `json:"labels"`
}

// Label is a label's row: a name unique among its project's live labels
// on a local write, and a colour, one of the names the UI maps to the
// brand's status scales.
type Label struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"projectId"`
	Name      string    `json:"name"`
	Color     string    `json:"color"`
	CreatedAt time.Time `json:"createdAt"`
	DeletedAt time.Time `json:"deletedAt,omitzero"`
	Rev       int64     `json:"rev"`
}

// Milestone is a milestone's row: a target date (YYYY-MM-DD) or none,
// open or closed.
type Milestone struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"projectId"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Target      string    `json:"target"`
	State       string    `json:"state"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	DeletedAt   time.Time `json:"deletedAt,omitzero"`
	Rev         int64     `json:"rev"`
}

// Relation is a relation between two tickets of one project: From blocks
// To, or the two relate.
type Relation struct {
	ID        string    `json:"id"`
	FromID    string    `json:"fromId"`
	ToID      string    `json:"toId"`
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"createdAt"`
	CreatedBy string    `json:"createdBy"`
	DeletedAt time.Time `json:"deletedAt,omitzero"`
}

// Comment is a comment's row: its author an actor, By the display name;
// N its 1-based place among its ticket's comments by (created_at,
// author, id), derived at read and never stored.
type Comment struct {
	ID        string    `json:"id"`
	TicketID  string    `json:"ticketId"`
	N         int       `json:"n"`
	Author    string    `json:"author"`
	By        string    `json:"by"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
	EditedAt  time.Time `json:"editedAt,omitzero"`
	DeletedAt time.Time `json:"deletedAt,omitzero"`
	Rev       int64     `json:"rev"`
}

// Attachment is an attachment's row: its content is the file named by
// its hash beside the database.
type Attachment struct {
	ID          string    `json:"id"`
	TicketID    string    `json:"ticketId"`
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256"`
	ContentType string    `json:"contentType"`
	CreatedAt   time.Time `json:"createdAt"`
	CreatedBy   string    `json:"createdBy"`
	DeletedAt   time.Time `json:"deletedAt,omitzero"`
}

// Link is a link of a ticket's: a delivery (a merged pull request's or a
// release's URL), a run, or an external reference; Ref its identity (a
// run's id, a pull request's number, else its URL). As JSON, a live
// link is what its add's change row records:
// {id, kind, ref, url, note, createdAt, createdBy}.
type Link struct {
	ID        string    `json:"id"`
	TicketID  string    `json:"-"`
	Kind      string    `json:"kind"`
	Ref       string    `json:"ref"`
	URL       string    `json:"url"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"createdAt"`
	CreatedBy string    `json:"createdBy"`
	DeletedAt time.Time `json:"deletedAt,omitzero"`
}

// View is a saved view: a list, board or roadmap, its query the filter,
// sort and grouping as JSON; ProjectID "" for every project.
type View struct {
	ID        string          `json:"id"`
	ProjectID string          `json:"projectId"`
	Name      string          `json:"name"`
	Kind      string          `json:"kind"`
	Query     json.RawMessage `json:"query"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`
	DeletedAt time.Time       `json:"deletedAt,omitzero"`
	Rev       int64           `json:"rev"`
}

// textKind is what a text field may hold beside its bound.
type textKind int

const (
	// kindName is a name or a title: no control character.
	kindName textKind = iota
	// kindMarkdown is Markdown: \n and \t, and no other control
	// character.
	kindMarkdown
	// kindText is text with no control character: a path, a reference,
	// a content type.
	kindText
	// kindURL is an http or https URL with a host name (a port alone is
	// none), and no control character.
	kindURL
	// kindFileName is a file's name: no '/', '\' or control character
	// (NUL among them), and neither "." nor "..".
	kindFileName
	// kindJSON is a JSON value.
	kindJSON
)

// textRule is a text field's rule: its bound, in characters or in
// bytes, whether it must hold something, and what it may hold.
type textRule struct {
	max      int
	chars    bool
	required bool
	kind     textKind
}

// fieldRules is every text field's rule, by entity and column: the
// schema table's bounds and shapes. op.actor and op.by are an
// operation's actor and its display name.
var fieldRules = map[string]textRule{
	"project.name":            {max: MaxProjectName, chars: true, required: true, kind: kindName},
	"project.description":     {max: MaxProjectDescription, kind: kindMarkdown},
	"project.cwd":             {max: MaxCwd, kind: kindText},
	"project.tracker_url":     {max: MaxTrackerURL, kind: kindURL},
	"person.name":             {max: MaxPersonName, chars: true, required: true, kind: kindName},
	"ticket.title":            {max: MaxTicketTitle, chars: true, required: true, kind: kindName},
	"ticket.description":      {max: MaxTicketDescription, kind: kindMarkdown},
	"ticket.acceptance":       {max: MaxAcceptance, kind: kindMarkdown},
	"ticket.accepted_note":    {max: MaxAcceptedNote, chars: true, kind: kindMarkdown},
	"label.name":              {max: MaxLabelName, chars: true, required: true, kind: kindName},
	"milestone.name":          {max: MaxMilestoneName, chars: true, required: true, kind: kindName},
	"milestone.description":   {max: MaxMilestoneDescription, kind: kindMarkdown},
	"comment.body":            {max: MaxCommentBody, required: true, kind: kindMarkdown},
	"attachment.name":         {max: MaxAttachmentName, required: true, kind: kindFileName},
	"attachment.content_type": {max: MaxContentType, kind: kindText},
	"link.ref":                {max: MaxLinkRef, kind: kindText},
	"link.url":                {max: MaxLinkURL, kind: kindURL},
	"link.note":               {max: MaxLinkNote, chars: true, kind: kindMarkdown},
	"view.name":               {max: MaxViewName, chars: true, required: true, kind: kindName},
	"view.query":              {max: MaxViewQuery, required: true, kind: kindJSON},
	"op.by":                   {max: MaxByName, chars: true, kind: kindName},
	"op.actor":                {max: MaxActor, required: true, kind: kindName},
}

// checkField checks s, a value of the field key names (entity.column),
// by its rule in fieldRules, the words naming it as label: a shape wrong
// is invalid, past the bound is bound. A key with no rule is the code's
// error, not a refusal.
func checkField(key, label, s string) error {
	r, ok := fieldRules[key]
	if !ok {
		return fmt.Errorf("dispatch: no rule for the field %s", key)
	}
	return r.check(label, s)
}

// check is s by the rule.
func (r textRule) check(label, s string) error {
	if s == "" {
		if r.required {
			return refuse(RefusalInvalid, "%s is empty", label)
		}
		return nil
	}
	if !r.chars && len(s) > r.max {
		return refuse(RefusalBound, "%s is more than %d bytes", label, r.max)
	}
	if !utf8.ValidString(s) {
		return refuse(RefusalInvalid, "%s is not valid UTF-8", label)
	}
	if r.chars && utf8.RuneCountInString(s) > r.max {
		return refuse(RefusalBound, "%s is more than %d characters", label, r.max)
	}
	if r.kind != kindJSON {
		for _, c := range s {
			if unicode.IsControl(c) && (r.kind != kindMarkdown || (c != '\n' && c != '\t')) {
				return refuse(RefusalInvalid, "%s holds a control character", label)
			}
		}
	}
	switch r.kind {
	case kindURL:
		u, err := url.Parse(s)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			return refuse(RefusalInvalid, "%s is not an http or https URL", label)
		}
	case kindFileName:
		if s == "." || s == ".." || strings.ContainsAny(s, `/\`) {
			return refuse(RefusalInvalid, "%s is not a file's name", label)
		}
	case kindJSON:
		if !json.Valid([]byte(s)) {
			return refuse(RefusalInvalid, "%s is not JSON", label)
		}
	}
	return nil
}

// checkCount refuses a set or a table that a local write would leave
// past its bound, n being what the write leaves; a write that only
// removes is never refused by it (its caller checks only an add).
func checkCount(what string, n, max int) error {
	if n > max {
		return refuse(RefusalBound, "at most %d %s; this would make %d", max, what, n)
	}
	return nil
}

// checkStatus checks a ticket's status.
func checkStatus(status string) error {
	if !slices.Contains(Statuses, status) {
		return refuse(RefusalInvalid, "the status %q is none of %s", status, strings.Join(Statuses, ", "))
	}
	return nil
}

// checkPriority checks a ticket's priority.
func checkPriority(p int) error {
	if p < 0 || p > MaxPriority {
		return refuse(RefusalInvalid, "the priority %d is not 0 to %d", p, MaxPriority)
	}
	return nil
}

// checkHorizon checks a ticket's horizon.
func checkHorizon(h string) error {
	if !slices.Contains(Horizons, h) {
		return refuse(RefusalInvalid, "the horizon %q is none of now, next, later or empty", h)
	}
	return nil
}

// checkOwner checks a ticket's owner: a person's name, or "" for none.
// A name need not be in the directory: people are local labels until
// identity exists (decision D4).
func checkOwner(owner string) error {
	if owner == "" {
		return nil
	}
	return checkField("person.name", "the owner", owner)
}

// agentIDPattern is a catalog agent's id, the shape internal/catalog's
// ValidID takes (the store imports nothing of the application's).
var agentIDPattern = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// The prefixes of an assignee's actor.
const (
	actorPerson = "person:"
	actorAgent  = "agent:"
)

// checkAssignee checks an assignee: person:<name>, the name a person's
// is, or agent:<catalog id>.
func checkAssignee(actor string) error {
	if name, ok := strings.CutPrefix(actor, actorPerson); ok {
		return checkField("person.name", "an assignee's name", name)
	}
	if id, ok := strings.CutPrefix(actor, actorAgent); ok && agentIDPattern.MatchString(id) {
		return nil
	}
	return refuse(RefusalInvalid, "an assignee is person:<name> or agent:<catalog id>, not %q", actor)
}

// checkColor checks a label's colour: one of LabelColors.
func checkColor(color string) error {
	if !slices.Contains(LabelColors, color) {
		return refuse(RefusalInvalid, "the colour %q is none of %s", color, strings.Join(LabelColors, ", "))
	}
	return nil
}

// checkTarget checks a milestone's target: a date as YYYY-MM-DD, or ""
// for none.
func checkTarget(target string) error {
	if target == "" {
		return nil
	}
	if _, err := time.Parse(targetLayout, target); err != nil {
		return refuse(RefusalInvalid, "the target %q is not a date as YYYY-MM-DD", target)
	}
	return nil
}

// checkMilestoneState checks a milestone's state: open or closed.
func checkMilestoneState(state string) error {
	if state != MilestoneOpen && state != MilestoneClosed {
		return refuse(RefusalInvalid, "the state %q is neither open nor closed", state)
	}
	return nil
}

// checkTracker checks a project's tracker and its URL together: a native
// project has no URL, an external one an http or https URL.
func checkTracker(tracker, trackerURL string) error {
	switch tracker {
	case TrackerNative:
		if trackerURL != "" {
			return refuse(RefusalInvalid, "a native project has no tracker URL")
		}
		return nil
	case TrackerExternal:
		if trackerURL == "" {
			return refuse(RefusalInvalid, "an external project names its tracker's URL")
		}
		return checkField("project.tracker_url", "the tracker URL", trackerURL)
	}
	return refuse(RefusalInvalid, "the tracker %q is neither native nor external", tracker)
}

// closePolicy checks a close policy and gives it in CloseRequirements'
// order: acceptance and delivery, each at most once; verified is refused
// until Program item DS2 defines it.
func closePolicy(requires []string) ([]string, error) {
	out := []string{}
	for _, want := range CloseRequirements {
		n := 0
		for _, r := range requires {
			if r == want {
				n++
			}
		}
		if n > 1 {
			return nil, refuse(RefusalInvalid, "the close policy names %s twice", want)
		}
		if n == 1 {
			out = append(out, want)
		}
	}
	for _, r := range requires {
		if r == requireVerified {
			return nil, refuse(RefusalInvalid, "the close policy cannot require verified until verification is defined")
		}
		if !slices.Contains(CloseRequirements, r) {
			return nil, refuse(RefusalInvalid, "the close policy's %q is neither acceptance nor delivery", r)
		}
	}
	return out, nil
}

// closePolicyValue checks a close policy (closePolicy) and gives it as
// its column and its change rows hold it: a JSON array in
// CloseRequirements' order.
func closePolicyValue(requires []string) (string, error) {
	policy, err := closePolicy(requires)
	if err != nil {
		return "", err
	}
	return encodeValue(policy)
}

// defaultClosePolicy is a new project's close policy when none is given
// (Owner decisions, 6): the owner's acceptance for a native project,
// nothing for an external one, whose tracker decides.
func defaultClosePolicy(tracker string) []string {
	if tracker == TrackerExternal {
		return []string{}
	}
	return []string{RequireAcceptance}
}

// encodeValue is v as a structured change value: JSON with HTML escaping
// off, so a '<', '>' or '&' stays one byte (encoding/json's default
// writes six), each character at most doubled but U+2028 and U+2029,
// which encoding/json writes as six bytes whatever SetEscapeHTML says:
// the escaping a structured value's bound is measured at.
func encodeValue(v any) (string, error) {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}

// stampLayout is a time as the store writes it: RFC 3339 in UTC with
// nine fractional digits always, so that stamps order as text (RFC3339Nano
// drops trailing zeros, and ".5Z" would sort after ".50000001Z").
const stampLayout = "2006-01-02T15:04:05.000000000Z"

// stamp is t as the store writes it; the zero time is "".
func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(stampLayout)
}

// unstamp reads a stamp back; "" and anything unreadable are the zero
// time.
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

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
