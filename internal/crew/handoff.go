package crew

import (
	"slices"
	"strings"

	"github.com/phenixrizen/conductor/internal/session"
)

// A handoff a member of a run reports (a session.ActivityHandoff entry) is
// typed into the member of the run it names, as one line, once that member
// runs and does not wait for input. One goroutine per member types the
// handoffs waiting for it (deliver). It looks again when the member's session
// records an entry (OnActivity), changes (OnChange: a prompt cleared records
// no entry) or the member starts running, never on a timer.

// maxHandoffs bounds the handoffs waiting for a member: another drops the
// oldest.
const maxHandoffs = 10

// handoff is a handoff waiting to be typed into a member: from names the
// member that reported it, text is the line typed, less its carriage return.
// queued is set once the run log says it waits.
type handoff struct {
	from, text string
	queued     bool
}

// oneLine makes one line of what the engine types, a handoff's message or a
// role prompt: the session keeps line breaks and tabs in a message, a crew
// keeps them in a prompt as written, and a line break or a carriage return
// typed would end the line early. Each becomes one space, so the line is as
// long as the text.
var oneLine = strings.NewReplacer("\r", " ", "\n", " ", "\t", " ")

// handoffText is the line a handoff types, less its carriage return. The
// message is at most session.MaxAttentionMessage bytes, as the session that
// recorded it cleaned it, so the line fits what a session takes at once.
func handoffText(from, message string) string {
	return strings.TrimSpace("Handoff from " + from + ": " + oneLine.Replace(message))
}

// OnChange takes the changes of every server session, as the server's change
// hook hands them. The change of a member's session wakes the handoffs
// waiting for that member: a prompt cleared, by the agent or an admin,
// records no entry, only a change. A session outside the runs costs nothing.
func (e *Engine) OnChange(info session.Info) {
	if info.Crew == nil {
		return
	}
	if v, ok := e.bySession.Load(info.ID); ok {
		v.(sessionMember).m.poke()
	}
}

// poke wakes the goroutine typing m's handoffs, if one runs. It takes no lock.
func (m *member) poke() {
	if !m.delivering.Load() {
		return
	}
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// handoff takes a handoff that from, a member of r, reports in entry: it
// waits for the member entry.To names and is typed into it (deliver). At most
// maxHandoffs wait for a member: another drops the oldest. A handoff to a name
// no member of r has, to from itself, or to a member with no session or whose
// session has ended, is only noted. A stopping run takes none. The caller
// holds e.mu; it reads the target's session, which never calls into the engine
// while it holds its lock.
func (e *Engine) handoff(r *run, from *member, entry session.ActivityEntry) {
	if r.stopping {
		return
	}
	to := r.member(entry.To)
	switch {
	case to == nil:
		r.note(session.ActivityError, "handoff to unknown member %s from %s", quote(entry.To), from.def.Name)
		return
	case to == from:
		r.note(session.ActivityError, "handoff to itself from %s", from.def.Name)
		return
	}
	var local *session.Local
	if to.state.SessionID != "" && to.state.Status != MemberEnded {
		if l, ok := e.lookup(to.state.SessionID); ok && !l.Info().Status.Ended() {
			local = l
		}
	}
	if local == nil {
		r.note(session.ActivityError, "handoff to a member that is not running, from %s to %s", from.def.Name, to.def.Name)
		return
	}
	if len(to.handoffs) >= maxHandoffs {
		r.dropOldest(to)
	}
	to.handoffs = append(to.handoffs, handoff{from: from.def.Name, text: handoffText(from.def.Name, entry.Message)})
	if to.delivering.Load() {
		to.poke()
		return
	}
	to.delivering.Store(true)
	r.deliveries.Add(1)
	go e.deliver(r, to, local)
}

// dropOldest drops the oldest handoff waiting for m, to make room. The caller
// holds e.mu.
func (r *run) dropOldest(m *member) {
	r.note(session.ActivityError, "handoff dropped from %s to %s: %d already waiting", m.handoffs[0].from, m.def.Name, maxHandoffs)
	m.handoffs = slices.Delete(m.handoffs, 0, 1)
}

// noteQueued notes, once each, the handoffs waiting for m that the log has
// not said wait, with why they wait. The caller holds e.mu.
func (r *run) noteQueued(m *member, why string) {
	for i := range m.handoffs {
		if h := &m.handoffs[i]; !h.queued {
			h.queued = true
			r.note(session.ActivityStatus, "handoff queued from %s to %s: %s %s", h.from, m.def.Name, m.def.Name, why)
		}
	}
}

// deliver types the handoffs waiting for m into l, its session, in order and
// each with a carriage return, once m runs, one at a time with
// session.Local.TypeUnlessWaiting: a handoff l takes while it waits for input
// goes back to the head of the queue, and every handoff waiting is noted as
// queued. Otherwise it waits to be woken (poke). It ends once none waits;
// when the run stops, or l or m ends, the handoffs left are dropped, each
// noted. It never holds e.mu while it types.
func (e *Engine) deliver(r *run, m *member, l *session.Local) {
	defer r.deliveries.Done()
	for {
		e.mu.Lock()
		if len(m.handoffs) == 0 {
			m.stopDelivering()
			e.mu.Unlock()
			return
		}
		why := ""
		switch {
		case r.ctx.Err() != nil:
			why = "the run is stopped"
		case m.state.Status == MemberEnded || isClosed(l.Ended()):
			why = m.def.Name + " is not running"
		}
		if why != "" {
			for _, h := range m.handoffs {
				r.note(session.ActivityError, "handoff dropped from %s to %s: %s", h.from, m.def.Name, why)
			}
			m.handoffs = nil
			m.stopDelivering()
			e.mu.Unlock()
			return
		}
		if m.state.Status != MemberRunning {
			r.noteQueued(m, "has not had its prompt yet")
			e.mu.Unlock()
			waitWake(r, m, l)
			continue
		}
		h := m.handoffs[0]
		m.handoffs = slices.Delete(m.handoffs, 0, 1)
		e.mu.Unlock()

		typed, err := l.TypeUnlessWaiting(h.text+"\r", typedBy)
		if e.tried != nil {
			e.tried(m.def.Name, typed)
		}
		e.mu.Lock()
		switch {
		case err != nil:
			r.note(session.ActivityError, "handoff dropped from %s to %s: %v", h.from, m.def.Name, err)
		case typed:
			r.note(session.ActivityStatus, "handoff delivered from %s to %s", h.from, m.def.Name)
		default:
			// Waiting for input: back to the head, the oldest again.
			m.handoffs = slices.Insert(m.handoffs, 0, h)
			if len(m.handoffs) > maxHandoffs {
				r.dropOldest(m)
			}
			r.noteQueued(m, "is waiting for input")
		}
		e.mu.Unlock()
		if err == nil && !typed {
			waitWake(r, m, l)
		}
	}
}

// stopDelivering marks that no goroutine types m's handoffs any more, and
// forgets a wake meant for it. The caller holds e.mu.
func (m *member) stopDelivering() {
	m.delivering.Store(false)
	select {
	case <-m.wake:
	default:
	}
}

// waitWake waits until m's handoffs may go: a wake, the run stopping or l
// ending.
func waitWake(r *run, m *member, l *session.Local) {
	select {
	case <-r.ctx.Done():
	case <-l.Ended():
	case <-m.wake:
	}
}

// isClosed reports whether c is closed, without waiting.
func isClosed(c <-chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}
