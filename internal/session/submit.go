package session

import (
	"bytes"
	"context"
	"errors"
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

// TrustQuestionWords say why a person's typed text is refused while the
// agent's trust question shows: its Enter would pick whatever the dialog
// highlights (Claude Code highlights "No, exit"). The question's own choices
// (Options.TrustAnswers) or the terminal answer it.
const TrustQuestionWords = "the agent asks whether to trust the folder: answer with the question's choices, or in its terminal"

// ErrTrustQuestion is Submit's refusal of a person's text while the trust
// question shows.
var ErrTrustQuestion = errors.New(TrustQuestionWords)

// QuestionWords say why a person's typed text is refused while a question
// the agent asks during its work shows (source "question"): its letters
// would reach the dialog's keys (Codex's command approval takes y, p and
// Esc) and its Enter would pick what the dialog highlights (Yes, proceed).
const QuestionWords = "the agent asks a question on its screen: answer with the question's choices, or in its terminal"

// ErrQuestion is Submit's refusal of a person's text while a question asked
// during the work shows.
var ErrQuestion = errors.New(QuestionWords)

// HeldWords say why the session needs input while its turn is held for a
// submission's text typed without its Enter, its link no longer opening the
// session (holdTyped, source SourceSubmit): the text waits in the program's
// input, and the next submission waits for a person to type into the
// terminal.
const HeldWords = "Text from a link that was let go was typed and not entered: type in the terminal to continue"

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
	// From is the connection whose request this is when By is not set (a
	// run's chat sent to another member's agent, recorded as ByName): its
	// link's revoke or expiry withdraws the submission as By's does
	// (revoked). Nil for what Conductor types on its own.
	From *Subscription
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
	// Finished, when set, is told how a submission accepted while its
	// program took it slowly (ErrSubmitPending) ended, once it has: its
	// result (Entered, or typed without its Enter) and the error that
	// ended it, if any.
	Finished func(SubmitResult, error)
}

// ErrSubmitPending says a submission's text was being taken when its time
// ran out: the program has not read it all yet. The submission is accepted:
// it is finished as the program reads (its Enter, as it would have been),
// holding the session's turn until then, and it is never typed again.
var ErrSubmitPending = errors.New("session: the program is taking its input slowly; the line is entered once it has")

// submitState is what a submission carries from its text to its Enter.
type submitState struct {
	promptSince *time.Time
	byName      string
	typedAt     time.Time
	wait        writeWait
}

// SubmitResult says how far a submission went.
type SubmitResult struct {
	// Typed is set once the text was written, or its write begun: with an
	// error, the text may land when the program reads again (see
	// Local.Submit).
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
//     drawn since the text (awaitLook), so a trust question drawn during the
//     pause holds the Enter back too; with a watcher for the questions asked
//     during the work, once it has looked at everything drawn so far.
//   - A submission is refused while a question the session holds shows,
//     a person's and Conductor's own alike (with UnlessWaiting it writes
//     nothing and returns a zero result, as for any prompt):
//     ErrTrustQuestion for a trust or startup question, ErrQuestion for
//     one asked during the work. A session with a watcher for the latter
//     looks at its screen first, so that a dialog drawn and not yet seen
//     quiet counts.
//   - It records one input entry: always, with the text, for what Conductor
//     types; for a controller's reply, the question it answered, when it
//     answered one.
//   - ctx ends the wait for the session's turn, the pause and each write;
//     a write also waits at most Options.InputWait for the program to take
//     it (ErrInputStalled), so a program that stops reading holds the
//     session's turn no longer than that, unless its text's write has
//     begun: then the submission is accepted and finished as the program
//     reads (ErrSubmitPending, Typed set), holding the turn till then; so
//     is one whose text is typed and whose Enter had no turn in time. An
//     Enter whose write began counts as entered, with no error: it lands
//     when the program reads, and Confirm presses nothing more for it.
//     BeforeEnter runs as the writer takes the Enter.
//
// Errors: ErrReadOnly for a view-role By, ErrTextTooLong for a text over
// MaxSubmitText bytes once cleaned, ErrSessionEnded, ctx's error,
// ErrInputStalled, ErrSubmitPending (accepted), ErrTooManyRequests, the
// process's write error, or why the session ended By (or From) for its
// link (ErrRevoked, ErrExpired), or its credential no longer stands,
// before its Enter was written. Before the text is typed, nothing of it
// is written. Once it is typed (or its write begun), its Enter is not
// written either, and the text cannot be taken back out of the program:
// the session's turn stays held (holdTyped), so that no other line is
// typed after the text and entered with it, until a person types into the
// terminal, who deals with the text, or the session ends. A submission
// whose sender only went (a reply box closes its connection once it has
// sent; one that failed or was too slow) or whose time ran out is
// finished as above.
func (s *Local) Submit(ctx context.Context, sub Submission) (SubmitResult, error) {
	var res SubmitResult
	if sub.By != nil && sub.By.Role != RoleControl {
		return res, ErrReadOnly
	}
	text := SubmitLine(sub.Text)
	if len(text) > MaxSubmitText {
		return res, ErrTextTooLong
	}
	// A controller's submission goes on when the controller goes (a reply
	// box closes its connection once it has sent, and one that fails or is
	// too slow is no different), but not once the session has ended the
	// controller for its link (revoked or expired): until its Enter is
	// written, it waits with that in view. Once its text is typed, the
	// session's turn is held for a person instead of its Enter (holdTyped).
	from := sub.By
	if from == nil {
		from = sub.From
	}
	var fromEnded <-chan struct{}
	if from != nil {
		fromEnded = from.done
	}
	for turn := false; !turn; {
		select {
		case s.submitting <- struct{}{}:
			turn = true
		case <-ctx.Done():
			return res, ctx.Err()
		case <-s.ended:
			return res, ErrSessionEnded
		case <-fromEnded:
			if err := revoked(from); err != nil {
				return res, err
			}
			fromEnded = nil
		}
	}
	// The turn is given back as Submit returns, unless the submission goes
	// on without its caller (ErrSubmitPending), which gives it back then.
	release := true
	defer func() {
		if release {
			<-s.submitting
		}
	}()
	if from != nil {
		if err := revoked(from); err != nil {
			return res, err
		}
	}

	// A dialog already drawn and not yet seen quiet takes a paste and drops
	// it (Codex routes a paste to its overlay): look at the screen before the
	// text, so that a handoff waits for the answer instead. A dialog on the
	// screen holds every submission, whatever the session shows.
	onScreen, err := s.lookAsked(ctx)
	if err != nil {
		return res, err
	}
	s.mu.Lock()
	ended := s.info.Status.Ended()
	promptSince := s.info.Attention.Since
	showing := s.info.Attention.State == AttentionNeedsInput
	source := s.info.Attention.Source
	s.mu.Unlock()
	if ended {
		return res, ErrSessionEnded
	}
	if sub.UnlessWaiting && showing {
		return res, nil
	}
	// Nothing is typed into a question the session holds, whoever types:
	// a person's reply, and Conductor's own text too (a crew's prompt, which
	// waits for the answer and is typed then).
	if showing && source == SourceTrust {
		return res, ErrTrustQuestion
	}
	if showing && source == SourceQuestion {
		return res, ErrQuestion
	}
	if onScreen {
		if sub.UnlessWaiting {
			return res, nil
		}
		return res, ErrQuestion
	}
	st := submitState{
		promptSince: promptSince,
		byName:      CleanName(sub.ByName),
		typedAt:     time.Now(),
		// Each write waits Options.InputWait at most, is refused at once
		// while the program is known not to read, and is withdrawn when
		// the session ends its controller (not when the controller only
		// goes): its text, and its Enter once the text is typed.
		wait: writeWait{wait: s.opts.InputWait, stall: s.opts.InputWait, from: from},
	}
	if text != "" {
		data := text
		if s.paste.on() {
			data = pasteStart + text + pasteEnd
		}
		// Looked at again as the writer takes the text: a question come
		// meanwhile, or a dialog drawn, holds it as one seen before does.
		textWait := st.wait
		textWait.guard = s.textGuard(sub)
		landed, err := s.writeProc(ctx, []byte(data), textWait)
		if errors.Is(err, errWaiting) {
			return res, nil
		}
		if err != nil {
			if landed == nil || errors.Is(err, ErrSessionEnded) || s.isEnded() {
				return res, err
			}
			// Begun, the text lands when the program reads, and the
			// submission is finished then as it would have been, holding
			// the session's turn: no later submission's text joins it, and
			// it is never typed again. Its caller is answered now: the
			// session's end of its controller for its link, the turn held
			// for a person once the text lands (finishSubmission); else
			// accepted.
			res.Typed = true
			release = false
			go s.finishSubmission(landed, sub, text, st)
			if errors.Is(err, ErrRevoked) {
				return res, err
			}
			return res, ErrSubmitPending
		}
		res.Typed = true
		s.typedEntry(sub, text, st)
	}
	res, err = s.enter(ctx, sub, res, st)
	if res.Typed && !res.Entered && errors.Is(err, ErrRevoked) {
		// Its text is typed and its link no longer opens the session: no
		// Enter, and the turn held for a person (holdTyped).
		release = false
		go func() {
			defer func() { <-s.submitting }()
			s.holdTyped()
		}()
		return res, err
	}
	if res.Typed && !res.Entered && (errors.Is(err, errEnterLater) || errors.Is(err, context.DeadlineExceeded)) {
		// Its text is typed and its time ran out before its Enter (in its
		// pause, its look, or another's write the program does not take
		// ahead of it): accepted, and the rest of the line finished as
		// the program reads, holding the turn till then, so no other line
		// joins its text.
		release = false
		go s.finishEnter(sub, res, st)
		return res, ErrSubmitPending
	}
	return res, err
}

// errEnterLater says a submission's text is typed and its Enter's write had
// no turn in time and was not made (enter): its Enter is to be pressed
// later, the turn held (finishEnter).
var errEnterLater = errors.New("session: the Enter is to be pressed later")

// finishEnter presses the Enter of a submission whose text is typed and
// whose Enter had no turn in time (errEnterLater): as long as the session
// lasts, holding its turn, with no second Enter, its caller having had its
// answer; then it gives the turn back. The checks before an Enter are made
// again, as for any.
func (s *Local) finishEnter(sub Submission, res SubmitResult, st submitState) {
	defer func() { <-s.submitting }()
	s.finishLine(sub, res, st)
}

// finishLine finishes a line whose text is typed, the session's turn held
// by its caller: its pause and looks again, then its Enter, with no time of
// its own and no second Enter, its caller having had its answer; and tells
// how it ended (finished). One whose link no longer opens the session by
// then has no Enter, and holds the turn for a person (holdTyped).
func (s *Local) finishLine(sub Submission, res SubmitResult, st submitState) {
	sub.Confirm = false
	st.wait.wait, st.wait.stall = 0, 0
	res, err := s.enter(context.Background(), sub, res, st)
	if errors.Is(err, errEnterLater) {
		res, err = s.pressEnterHeld(sub, res, st)
	}
	s.finished(sub, res, err)
	if res.Typed && !res.Entered && errors.Is(err, ErrRevoked) {
		s.holdTyped()
	}
}

// holdTyped holds the session's turn, which its caller holds and gives
// back once it returns, after a submission whose text was typed and whose
// Enter was not written, its link no longer opening the session: no other
// submission (a person's reply, a crew's prompt, a handoff) is typed after
// the text, to be entered with it, until a person types into the terminal
// (input), who deals with what is left in the program's input, or the
// session ends. The session shows needs_input meanwhile, with HeldWords
// (source SourceSubmit), which the keys clear; another attention change is
// recorded and not shown, but for a question on the screen (setAttention).
func (s *Local) holdTyped() {
	s.mu.Lock()
	if s.info.Status.Ended() {
		s.mu.Unlock()
		return
	}
	held := make(chan struct{})
	s.held = held
	s.mu.Unlock()
	s.log.Info("a submission's text was typed and not entered; the next waits for keys typed in the terminal")
	s.setAttention(AttentionNeedsInput, HeldWords, SourceSubmit, "", nil, whileHeld)
	select {
	case <-held:
	case <-s.ended:
	}
	s.mu.Lock()
	if s.held == held {
		s.held = nil
	}
	// Keys that ended the hold as it began, before it showed, answered
	// nothing: what it shows goes with it.
	shown := s.info.Attention.State == AttentionNeedsInput && s.info.Attention.Source == SourceSubmit
	if shown {
		s.info.Attention = Attention{State: AttentionNone, Source: SourceInput}
		s.signalAttention()
		s.hub.Broadcast(proto.MustControl(attentionMessage(s.info.Attention)))
	}
	s.mu.Unlock()
	if shown {
		s.notifyChange()
	}
}

// endHold ends the hold of the session's turn for a typed text, if one
// stands (holdTyped): a person types into the terminal.
func (s *Local) endHold() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.held != nil {
		close(s.held)
		s.held = nil
	}
}

// Held reports whether the session's turn is held for a submission's text
// typed without its Enter (holdTyped), until a person types into the
// terminal.
func (s *Local) Held() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.held != nil
}

// finished tells how a submission finished in the background ended: logged
// when it was not entered, and to its Finished.
func (s *Local) finished(sub Submission, res SubmitResult, err error) {
	if err != nil || !res.Entered {
		s.log.Info("a submission finished as its program read was typed without its Enter", "err", err)
	}
	if sub.Finished != nil {
		sub.Finished(res, err)
	}
}

// pressEnterHeld presses a submission's Enter, the session's turn held by
// its caller, as long as the session lasts: with no time of its own, and
// again a moment later while the writer's queue is full. It returns how
// that ended (not entered: the session's end, a prompt, its link).
func (s *Local) pressEnterHeld(sub Submission, res SubmitResult, st submitState) (SubmitResult, error) {
	sub.Confirm = false
	st.wait.wait, st.wait.stall = 0, 0
	for {
		got, err := s.pressEnter(context.Background(), sub, res, st)
		if errors.Is(err, errEnterLater) {
			select {
			case <-time.After(100 * time.Millisecond):
				continue
			case <-s.ended:
				return got, ErrSessionEnded
			}
		}
		return got, err
	}
}

// typedEntry records what Conductor typed itself (By unset), once its text
// is written.
func (s *Local) typedEntry(sub Submission, text string, st submitState) {
	if sub.By == nil {
		s.Record(ActivityEntry{Type: ActivityInput, ByName: st.byName, Message: text})
	}
}

// finishSubmission finishes a submission whose text was being taken when
// its caller was answered (ErrSubmitPending): once the text has landed, the
// pause and its Enter, as enter does, BeforeEnter included, with no second
// Enter, for its caller has had its answer. The Enter waits as long as the
// session lasts, with no time of its own: the submission holds the
// session's turn until then, so no other line joins its text, and gives it
// back. The session's end or a prompt come meanwhile ends it without its
// Enter, which is logged.
func (s *Local) finishSubmission(landed <-chan error, sub Submission, text string, st submitState) {
	defer func() { <-s.submitting }()
	select {
	case err := <-landed:
		if err != nil {
			s.finished(sub, SubmitResult{}, err)
			return
		}
	case <-s.ended:
		s.finished(sub, SubmitResult{}, ErrSessionEnded)
		return
	}
	s.typedEntry(sub, text, st)
	s.finishLine(sub, SubmitResult{Typed: true}, st)
}

// enter is the rest of a submission once its text is written (res.Typed),
// or when it has none: the pause and the look for a trust question after a
// text, then its Enter, unless a prompt came meanwhile (or, with no text,
// its controller's link no longer stands).
func (s *Local) enter(ctx context.Context, sub Submission, res SubmitResult, st submitState) (SubmitResult, error) {
	if res.Typed {
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
	// The screen watchers look before the Enter (awaitLook). A trust
	// question drawn during the pause is found only once the screen has
	// been quiet for patternQuiet: the trust watcher looks at what was drawn
	// since the text. A question asked during the work can be drawn at any
	// time, also just before the text, and an Enter that lands on it picks
	// what it highlights (Codex's "Yes, proceed"): its watcher waits for a
	// quiet screen, then looks at the screen as it is whether quiet or not
	// (a program that never stops drawing runs the wait out), so that a
	// dialog on the screen has raised the question before the Enter, text
	// or none.
	if s.asked != nil {
		if err := s.awaitLook(ctx, time.Time{}); err != nil {
			return res, err
		}
		if onScreen, err := s.lookAsked(ctx); err != nil || onScreen {
			return res, err
		}
	} else if res.Typed && s.trust != nil {
		if err := s.awaitLook(ctx, st.typedAt); err != nil {
			return res, err
		}
	}
	return s.pressEnter(ctx, sub, res, st)
}

// pressEnter writes a submission's Enter, unless the session has ended, its
// controller's link no longer stands (st.wait.from: why, its text typed or
// not) or a prompt came since the submission began; BeforeEnter runs as
// the writer takes it. An Enter whose
// write has begun counts as entered. With its text typed, an Enter whose
// write had no turn in time (its context's deadline, InputWait, a stall)
// and was not made is errEnterLater.
func (s *Local) pressEnter(ctx context.Context, sub Submission, res SubmitResult, st submitState) (SubmitResult, error) {
	promptSince, byName, wait := st.promptSince, st.byName, st.wait
	s.mu.Lock()
	ended := s.info.Status.Ended()
	att := s.info.Attention
	s.mu.Unlock()
	if ended {
		return res, ErrSessionEnded
	}
	if err := wait.withdrawn(); err != nil {
		// The session ended its controller for its link: nothing more of it
		// is written, and a text it typed holds the turn (holdTyped), a
		// prompt come meanwhile or not.
		return res, err
	}
	if att.State == AttentionNeedsInput && att.Since != promptSince {
		return res, nil
	}
	// As the writer takes the Enter it looks again: a prompt raised while
	// the Enter waited behind another write, or a dialog drawn on the
	// screen meanwhile, is never answered by it.
	wait.guard = s.enterGuard(func(att Attention) bool {
		return att.State == AttentionNeedsInput && att.Since != promptSince
	})
	// As the writer takes the Enter, never for one not written: the
	// caller's BeforeEnter, and the turn a done ended is behind (nextTurn).
	wait.before = func() {
		if sub.BeforeEnter != nil {
			sub.BeforeEnter()
		}
		s.nextTurn()
	}
	enterAt := time.Now().UTC()
	landed, err := s.writeProc(ctx, []byte{'\r'}, wait)
	pending := landed != nil
	if errors.Is(err, errPromptCame) {
		// Left out, as one seen before the Enter is.
		return res, nil
	}
	if err != nil && !pending && res.Typed && waitedOut(err) {
		return res, errEnterLater
	}
	if err != nil && (!pending || errors.Is(err, ErrSessionEnded) || s.isEnded()) {
		// Not begun, or the session ended under it: never entered.
		if pending && !errors.Is(err, ErrSessionEnded) {
			err = ErrSessionEnded
		}
		return res, err
	}
	// An Enter whose write has begun lands when the program reads again:
	// the submission is entered, and nothing more is pressed for it.
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
	if pending {
		s.log.Info("a submission's Enter waits for the program to take input")
		return res, nil
	}
	if sub.Confirm && s.opts.ConfirmSubmit && !s.confirmed(ctx, enterAt) {
		res.Reentered = s.enterAgain(ctx)
	}
	return res, nil
}

// lookAsked looks at the screen for a question asked during the work, quiet
// or not, and reports whether one is on it (false without that watcher).
// Right after a key that answers such a dialog (Hold), the screen is still
// the answered one: it waits until the program has drawn again or the end
// of a quiet period has looked, since the key may have done nothing (a
// digit past the dialog's lines); at most patternQuiet and a margin, after
// which it counts the dialog as on the screen.
func (s *Local) lookAsked(ctx context.Context) (bool, error) {
	if s.asked == nil {
		return false, nil
	}
	deadline := time.Now().Add(2 * patternQuiet)
	for s.asked.Held() {
		if time.Now().After(deadline) {
			return true, nil
		}
		t := time.NewTimer(10 * time.Millisecond)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return false, ctx.Err()
		case <-s.ended:
			t.Stop()
			return false, ErrSessionEnded
		}
	}
	return s.asked.CheckNow(), nil
}

// lookBudget bounds how long Submit waits, before its Enter, for the screen
// watchers to look at what the program drew: the echo of a paste moves the
// output, and a program that keeps drawing is not held up for longer than
// this.
const lookBudget = 2 * time.Second

// errWaiting refuses the text of a submission with UnlessWaiting that a
// prompt or a dialog come meanwhile would take (textGuard): Submit returns
// a zero result then, as for one seen before the text.
var errWaiting = errors.New("session: the session waits on a prompt")

// textGuard is a submission's text's guard (writeWait.guard), asked as the
// writer takes it: what Submit refuses before the text, looked at again,
// for a question or a dialog come while the text waited behind another
// write. With UnlessWaiting, any prompt (errWaiting); else a startup
// question (ErrTrustQuestion), a question asked during the work or a
// dialog on the screen now or Held (ErrQuestion).
func (s *Local) textGuard(sub Submission) func() error {
	return func() error {
		dialog := s.asked != nil && (s.asked.Held() || s.asked.CheckNow())
		s.mu.Lock()
		att := s.info.Attention
		s.mu.Unlock()
		showing := att.State == AttentionNeedsInput
		switch {
		case sub.UnlessWaiting && (showing || dialog):
			return errWaiting
		case showing && att.Source == SourceTrust:
			return ErrTrustQuestion
		case dialog || showing && att.Source == SourceQuestion:
			return ErrQuestion
		}
		return nil
	}
}

// errPromptCame refuses a submission's Enter that a prompt raised since the
// submission began would take (pressEnter's guard).
var errPromptCame = errors.New("session: a prompt came before the Enter")

// enterGuard is an Enter's guard (writeWait.guard), asked as the writer
// takes it: errPromptCame when the session's attention is one waits says
// the Enter must not answer, or when a dialog asked during the work is on
// the screen as it is now (looked at whether quiet or not, and fired
// then), or a key that answered one has not been followed by a look yet
// (Held): the Enter would pick what the dialog highlights.
func (s *Local) enterGuard(waits func(Attention) bool) func() error {
	return func() error {
		if s.asked != nil && (s.asked.Held() || s.asked.CheckNow()) {
			return errPromptCame
		}
		s.mu.Lock()
		att := s.info.Attention
		s.mu.Unlock()
		if waits(att) {
			return errPromptCame
		}
		return nil
	}
}

// waitedOut says a write was not made for want of a turn in time: its
// context's deadline, InputWait, a stall, a full queue; not its sender's
// withdrawal, its context's cancel or the session's end.
func waitedOut(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrInputStalled) || errors.Is(err, ErrTooManyRequests)
}

// awaitLook waits until the screen watchers have had their quiet period
// (patternQuiet) after the last output, when that output came after since,
// so that a question drawn meanwhile has raised needs_input before the
// Enter is written; at most lookBudget, or until ctx or the session ends.
func (s *Local) awaitLook(ctx context.Context, since time.Time) error {
	deadline := time.Now().Add(lookBudget)
	for {
		last := s.LastOutputAt()
		if !last.After(since) {
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
func (s *Local) enterAgain(ctx context.Context) bool {
	s.mu.Lock()
	skip := s.info.Status.Ended() || s.info.Attention.State == AttentionNeedsInput
	s.mu.Unlock()
	if skip {
		return false
	}
	// Looked at again as the writer takes it: no prompt or dialog come
	// meanwhile is answered by it.
	again := writeWait{wait: s.opts.InputWait, stall: s.opts.InputWait, guard: s.enterGuard(func(att Attention) bool {
		return att.State == AttentionNeedsInput
	})}
	if _, err := s.writeProc(ctx, []byte{'\r'}, again); err != nil {
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
