package hostagent

import (
	"strings"
	"testing"
	"time"

	"github.com/rocksolidlabs/conductor/internal/proto"
	"github.com/rocksolidlabs/conductor/internal/session"
)

// A host's session answers a submit naming a request (#322) as the server's
// does: submit_ok once the line and its Enter are written; not_sent with the
// question's words when the guard held it back from a session that asks,
// nothing written; a submit naming none is written and told nothing; one
// past its bounds is bad_frame.
func TestAHostAnswersASubmitNamingItsRequest(t *testing.T) {
	local, proc, nate, _ := answerHost(t)
	written := func(d time.Duration) string {
		var b strings.Builder
		deadline := time.After(d)
		for {
			select {
			case w := <-proc.Input:
				b.Write(w)
			case <-deadline:
				return b.String()
			}
		}
	}
	nate.submit("s1", "hello", true)
	if m := nate.verdict("s1"); m["t"] != proto.CtlSubmitOK {
		t.Fatalf("into a running session: %v", m)
	}
	if got := written(200 * time.Millisecond); !strings.Contains(got, "hello") || !strings.Contains(got, "\r") {
		t.Fatalf("written %q", got)
	}

	local.SetAttentionFull(session.AttentionNeedsInput, "Which database?", session.SourceAPI, session.KindPrompt, nil)
	nate.submit("s2", "SQLite", true)
	if m := nate.verdict("s2"); m["code"] != proto.ErrCodeNotSent || m["message"] != session.QuestionWords {
		t.Fatalf("held back: %v", m)
	}
	if got := written(200 * time.Millisecond); got != "" {
		t.Fatalf("written while asking: %q", got)
	}
	local.SetAttention(session.AttentionNone, "", "")

	nate.submit("", "plain", false)
	if got := written(300 * time.Millisecond); !strings.Contains(got, "plain") {
		t.Fatalf("an unnamed submit: %q", got)
	}
	nate.submit("s3", strings.Repeat("x", proto.MaxSubmit+1), false)
	if m := nate.verdict("s3"); m["code"] != proto.ErrCodeBadFrame {
		t.Fatalf("a long line: %v", m)
	}
}
