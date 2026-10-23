package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"regexp"
	"time"

	"github.com/phenixrizen/conductor/internal/crew"
	"github.com/phenixrizen/conductor/internal/crew/ledger"
	"github.com/phenixrizen/conductor/internal/store"
)

// openLedger opens the managed runs' ledger under the data directory's
// runs/ (beside the records) and concludes what the last owner left open
// (ledger.Recover). A ledger that opens read-only (another server holds
// the owner lock, the filesystem has no locks, the database is damaged or
// newer) is logged, and managed launches answer 503 from then on; the
// legacy runs are untouched by it.
func openLedger(ctx context.Context, st *store.Store, log logger) *ledger.Ledger {
	ld, err := ledger.Open(filepath.Join(st.Dir(), "runs"))
	if err != nil {
		// The directory or the file itself: the server runs its legacy
		// crews all the same, with no ledger.
		log.Warn("managed runs are off: the ledger could not be opened", "err", err)
		return nil
	}
	if ro, why := ld.ReadOnly(); ro {
		log.Warn("managed runs are off: the ledger is read-only", "why", why)
		return ld
	}
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	recovered, err := ld.Recover(rctx)
	if err != nil {
		log.Warn("the ledger's recovery failed", "err", err)
	}
	for _, r := range recovered {
		log.Info("a managed run was left open by the last server's end", "run", r.RunID, "interrupted", r.Interrupted, "attempts", r.Attempts, "checks", r.Checks, "commands", r.Commands)
	}
	return ld
}

// logger is what openLedger logs with.
type logger interface {
	Warn(msg string, args ...any)
	Info(msg string, args ...any)
}

// commandIDPattern bounds a command id: what a client may mint
// (docs/plans/agent-harness-order.md, decision 10).
var commandIDPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{1,32}$`)

// adminActor is the actor a workbench token's commands are kept under.
const adminActor = "admin"

// handleGetCommand answers a command's receipt: 200 {command}, 404 when
// the run or the admin's command with that id is unknown. It never acts:
// a client that lost a reply reads here before it sends again.
func (s *Server) handleGetCommand(w http.ResponseWriter, r *http.Request) {
	runID, id := r.PathValue("run"), r.PathValue("command")
	if !crew.ValidRunID(runID) || !commandIDPattern.MatchString(id) {
		writeError(w, http.StatusBadRequest, "invalid_request", "a run id and a command id of 1 to 32 letters, digits, '.', '_', '~' or '-'")
		return
	}
	if s.ledger == nil {
		// No ledger opened: the receipt may exist and cannot be read, which
		// is not its absence.
		writeError(w, http.StatusServiceUnavailable, "ledger_unavailable", "the ledger could not be opened; see the server's log")
		return
	}
	c, ok, err := s.ledger.Receipt(r.Context(), runID, adminActor, id)
	if errors.Is(err, ledger.ErrUnavailable) || errors.Is(err, ledger.ErrClosed) {
		writeError(w, http.StatusServiceUnavailable, "ledger_unavailable", err.Error())
		return
	}
	if err != nil {
		s.log.Warn("command receipt", "run", runID, "command", id, "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "the receipt could not be read")
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such command")
		return
	}
	var receipt json.RawMessage
	if c.Receipt != "" {
		receipt = json.RawMessage(c.Receipt)
	}
	writeJSON(w, http.StatusOK, map[string]any{"command": map[string]any{
		"id": c.ID, "intent": c.Intent, "status": c.Status, "receipt": receipt, "createdAt": c.CreatedAt,
	}})
}
