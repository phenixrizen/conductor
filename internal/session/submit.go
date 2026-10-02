package session

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/phenixrizen/conductor/internal/proto"
)

// Submitting a line. What Conductor types itself (a crew member's role
// prompt, a handoff, a broadcast) and what a person sends from a reply box
// reach the program the way a terminal sends a paste followed by a key press:
// the text, wrapped in the bracketed-paste markers while the program has that
// mode on, then, SubmitPause later, a carriage return written on its own. A
// TUI that reads the text and its carriage return in one read takes the
// carriage return as part of a paste (Codex's paste-burst detector turns it
// into a newline; Claude Code collapses a read of over 800 bytes into a
// pasted block); written apart, it is Enter. A person's own keystrokes (Input)
// stay raw.

// Defaults of the submission routine (Options.SubmitPause, Options.ConfirmWait).
const (
	// SubmitPause is the pause between the text and its Enter.
	SubmitPause = 250 * time.Millisecond
	// ConfirmWait is how long Submit waits, for a prompt whose agent reports
	// taking it (Options.ConfirmSubmit and Submission.Confirm), before it
	// presses Enter once more.
	ConfirmWait = 3 * time.Second
)

// The bracketed-paste markers a terminal wraps a paste in.
const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// MaxSubmitText bounds the text of a submission, in bytes: with the paste
// markers around it, it fits one INPUT frame.
const MaxSubmitText = proto.MaxInput - len(pasteStart) - len(pasteEnd)

// Submission is a line to submit (Local.Submit).
type Submission struct {
	// Text is the line. Submit makes each line break, carriage return and
	// tab a space and drops the other control characters, so that the text
	// can neither end the paste early nor press a key of its own. Empty, only
	// the Enter is written.
	Text string
	// By is the controller who sends it, from a reply box; nil for what
	// Conductor types itself, recorded as ByName (cleaned as a display name).
	By     *Subscription
	ByName string
	// UnlessWaiting writes nothing while the session waits on a prompt: for
	// what must never answer one (handoffs, broadcasts).
	UnlessWaiting bool
	// Confirm presses Enter once more when the session's agent reports
	// taking a prompt (Options.ConfirmSubmit) and has not done so within
	// ConfirmWait: a role prompt typed while the program was still starting
	// can sit in its input box. Never for a reply.
	Confirm bool
	// BeforeEnter, when set, runs just before the Enter is written, outside
	// the session's lock: the run engine stamps a member prompted there, so
	// that a done reported before the Enter never counts for the prompt.
	BeforeEnter func()
}

// SubmitResult says how far a submission went.
type SubmitResult struct {
	// Typed is set once the text was written.
	Typed bool
	// Entered is set once its Enter was written. Typed without Entered: a
	// prompt appeared during the pause, and the text waits in the program's
	// input without its Enter; nothing is ever written again for it.
	Entered bool
	// Reentered is set when a second Enter was written (Confirm).
	Reentered bool
}

// Submit types sub.Text and presses Enter, as two writes Options.SubmitPause
// apart, one submission at a time per session. It holds no lock of the
// session across the pause, and a person's keystrokes are not held back
// meanwhile.
//
//   - With UnlessWaiting it writes nothing while the session waits on a
//     prompt, and returns a zero result.
//   - The Enter answers the prompt that was showing when Submit began, as
//     typing does; a prompt raised during the pause (one with a new Since) is
//     never answered: the Enter is left out. In a session with a trust
//     watcher the pause ends only once the watcher has looked at what was
//     drawn since the text (awaitTrustLook), so a trust question drawn
//     during the pause holds the Enter back too.
//   - It records one input entry: always, with the text, for what Conductor
//     types; for a controller's reply, the question it answered, when it
//     answered one.
//   - ctx ends the wait for the session's turn and the pause; an error after
//     the text was written comes with Typed set, and the text is never
//     written again.
//
// Errors: ErrReadOnly for a view-role By, ErrTextTooLong for a text over
// MaxSubmitText bytes once cleaned, ErrSessionEnded, ctx's error, or the
// process's write error.
func (s *Local) Submit(ctx context.Context, sub Submission) (SubmitResult, error) {
	var res SubmitResult
	if sub.By != nil && sub.By.Role != RoleControl {
		return res, ErrReadOnly
	}
	text := SubmitLine(sub.Text)
	if len(text) > MaxSubmitText {
		return res, ErrTextTooLong
	}
	select {
	case s.submitting <- struct{}{}:
	case <-ctx.Done():
		return res, ctx.Err()
	case <-s.ended:
		return res, ErrSessionEnded
	}
	defer func() { <-s.submitting }()

	s.mu.Lock()
	ended := s.info.Status.Ended()
	promptSince := s.info.Attention.Since
	showing := s.info.Attention.State == AttentionNeedsInput
	s.mu.Unlock()
	if ended {
		return res, ErrSessionEnded
	}
	if sub.UnlessWaiting && showing {
		return res, nil
	}
	byName := CleanName(sub.ByName)
	typedAt := time.Now()
	if text != "" {
		data := text
		if s.paste.on() {
			data = pasteStart + text + pasteEnd
		}
		if _, err := s.proc.Write([]byte(data)); err != nil {
			return res, err
		}
		res.Typed = true
		if sub.By == nil {
			s.Record(ActivityEntry{Type: ActivityInput, ByName: byName, Message: text})
		}
		t := time.NewTimer(s.opts.SubmitPause)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return res, ctx.Err()
		case <-s.ended:
			t.Stop()
			return res, ErrSessionEnded
		}
	}
	if res.Typed && s.trust != nil {
		// A trust question drawn during the pause is found by the screen
		// watcher only once the screen has been quiet for patternQuiet: let
		// it look at what was drawn since the text before the Enter.
		if err := s.awaitTrustLook(ctx, typedAt); err != nil {
			return res, err
		}
	}
	s.mu.Lock()
	ended = s.info.Status.Ended()
	att := s.info.Attention
	s.mu.Unlock()
	if ended {
		return res, ErrSessionEnded
	}
	if att.State == AttentionNeedsInput && att.Since != promptSince {
		return res, nil
	}
	if sub.BeforeEnter != nil {
		sub.BeforeEnter()
	}
	enterAt := time.Now().UTC()
	if _, err := s.proc.Write([]byte{'\r'}); err != nil {
		return res, err
	}
	res.Entered = true
	if s.trust != nil {
		// A trust question comes before the first prompt, never after.
		s.trust.Stop()
	}
	if sub.By != nil {
		s.stampTyping(sub.By)
		s.answer(promptSince, sub.By.ID, sub.By.Name, true, true)
	} else {
		s.answer(promptSince, "", byName, false, true)
	}
	if sub.Confirm && s.opts.ConfirmSubmit && !s.confirmed(ctx, enterAt) {
		res.Reentered = s.enterAgain()
	}
	return res, nil
}

// trustLookBudget bounds how long Submit waits, before its Enter, for the
// trust watcher to look at what the program drew since the text: the echo of
// a paste moves the output, and a program that keeps drawing is not held up
// for longer than this.
const trustLookBudget = 2 * time.Second

// awaitTrustLook waits until the trust watcher has had its quiet period
// (patternQuiet) after the last output that came since typedAt, so that a
// trust question drawn during the pause has raised needs_input before the
// Enter is written; at most trustLookBudget, or until ctx or the session ends.
func (s *Local) awaitTrustLook(ctx context.Context, typedAt time.Time) error {
	deadline := time.Now().Add(trustLookBudget)
	for {
		last := s.LastOutputAt()
		if !last.After(typedAt) {
			return nil
		}
		wait := time.Until(last.Add(patternQuiet + 50*time.Millisecond))
		if wait <= 0 {
			return nil
		}
		if left := time.Until(deadline); left < wait {
			if left <= 0 {
				return nil
			}
			wait = left
		}
		t := time.NewTimer(wait)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-s.ended:
			t.Stop()
			return ErrSessionEnded
		}
	}
}

// confirmed waits up to Options.ConfirmWait for the agent to show that it
// took what was submitted at enterAt: an attention change stamped after it
// that typing did not make (Claude Code's UserPromptSubmit hook reports
// working). It returns true when ctx ends or the session ends meanwhile:
// nothing is pressed then.
func (s *Local) confirmed(ctx context.Context, enterAt time.Time) bool {
	deadline := time.NewTimer(s.opts.ConfirmWait)
	defer deadline.Stop()
	for {
		s.mu.Lock()
		att := s.info.Attention
		changed := s.attnChanged
		s.mu.Unlock()
		if att.Since != nil && att.Since.After(enterAt) && att.Source != SourceInput {
			return true
		}
		select {
		case <-changed:
		case <-deadline.C:
			return false
		case <-ctx.Done():
			return true
		case <-s.ended:
			return true
		}
	}
}

// enterAgain writes one more carriage return, unless the session has ended or
// waits on a prompt, which the Enter must not answer. It records nothing.
func (s *Local) enterAgain() bool {
	s.mu.Lock()
	skip := s.info.Status.Ended() || s.info.Attention.State == AttentionNeedsInput
	s.mu.Unlock()
	if skip {
		return false
	}
	if _, err := s.proc.Write([]byte{'\r'}); err != nil {
		return false
	}
	s.log.Info("pressed Enter again: the agent did not report taking the prompt", "after", s.opts.ConfirmWait)
	return true
}

// SubmitLine is the text Submit writes for text: valid UTF-8, each line
// break, carriage return and tab a space, every other control character (ESC
// among them, so the text cannot end a paste early) dropped, trimmed.
func SubmitLine(text string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		switch {
		case r == '\r' || r == '\n' || r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, strings.ToValidUTF8(text, "")))
}

// pasteMode follows the bracketed-paste mode a program sets in its output
// (ESC[?2004h on, ESC[?2004l off), as a terminal does, carrying the end of a
// chunk over to the next so a sequence split between two reads still counts.
// Only the pump writes it; anyone reads it.
type pasteMode struct {
	state atomic.Int32 // 0 never set, 1 on, 2 off
	tail  []byte       // the pump's alone
}

var (
	pasteOnSeq  = []byte("\x1b[?2004h")
	pasteOffSeq = []byte("\x1b[?2004l")
)

// feed takes a chunk of output. Only the pump calls it.
func (p *pasteMode) feed(chunk []byte) {
	data := append(p.tail, chunk...)
	on, off := bytes.LastIndex(data, pasteOnSeq), bytes.LastIndex(data, pasteOffSeq)
	switch {
	case on > off:
		p.state.Store(1)
	case off > on:
		p.state.Store(2)
	}
	n := min(len(data), len(pasteOnSeq)-1)
	p.tail = append(p.tail[:0], data[len(data)-n:]...)
}

func (p *pasteMode) on() bool { return p.state.Load() == 1 }

// BracketedPaste reports whether the program has turned bracketed paste on
// at least once (seen) and whether it is on now. A program that turned it on
// and then off again is between screens (Claude Code does while it starts):
// the run engine does not type into it then.
func (s *Local) BracketedPaste() (seen, on bool) {
	st := s.paste.state.Load()
	return st != 0, st == 1
}

// titleOnly matches output that only sets the window title (OSC 0 or 2), as
// an agent that animates a spinner in its title does while it waits (Codex
// with terminal_title set): such output does not move LastOutputAt, so the
// program is seen to be quiet. A title sequence cut between two reads is
// output like any other.
var titleOnly = regexp.MustCompile(`^(?:\x1b\][02];[^\x07\x1b]*(?:\x07|\x1b\\))+$`)
