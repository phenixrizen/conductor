package session

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/rocksolidlabs/conductor/internal/proto"
)

// The owner's word on a submit that named a request (#322): nothing for one
// that named none, or accepted while the program takes it slowly (its word
// comes when it is finished); submit_ok once entered; not_sent with the
// question's words when the guard held it back; input_failed when typed and
// not entered; the other refusals named as an answer's are.
func TestSubmitWord(t *testing.T) {
	decode := func(f []byte) map[string]any {
		t.Helper()
		if f == nil {
			return nil
		}
		var m map[string]any
		if err := json.Unmarshal(f[1:], &m); err != nil {
			t.Fatalf("decode %q: %v", f, err)
		}
		return m
	}
	if SubmitWord("", SubmitResult{Entered: true}, nil) != nil || SubmitWord("", SubmitResult{}, ErrSessionEnded) != nil {
		t.Fatal("a submit naming no request is told nothing")
	}
	if SubmitWord("s1", SubmitResult{Typed: true}, ErrSubmitPending) != nil {
		t.Fatal("a pending line is told nothing yet")
	}
	if m := decode(SubmitWord("s1", SubmitResult{Typed: true, Entered: true}, nil)); m["t"] != proto.CtlSubmitOK || m["requestId"] != "s1" {
		t.Fatalf("entered: %v", m)
	}
	if m := decode(SubmitWord("s2", SubmitResult{}, nil)); m["t"] != proto.CtlError || m["code"] != proto.ErrCodeNotSent || m["message"] != QuestionWords || m["requestId"] != "s2" {
		t.Fatalf("held back by the guard: %v", m)
	}
	if m := decode(SubmitWord("s3", SubmitResult{Typed: true}, nil)); m["code"] != "input_failed" || m["message"] != LineStrandedWords || m["requestId"] != "s3" {
		t.Fatalf("typed and not entered: %v", m)
	}
	for err, code := range map[error]string{ErrTooManyRequests: proto.ErrCodeTooManyRequests, ErrSessionEnded: proto.ErrCodeSessionEnded, ErrReadOnly: proto.ErrCodeReadOnly, ErrQuestion: proto.ErrCodeNotSent, ErrTrustQuestion: proto.ErrCodeNotSent, ErrBadSubmit: proto.ErrCodeBadFrame, context.DeadlineExceeded: "input_failed", errors.New("other"): "input_failed"} {
		if m := decode(SubmitWord("s4", SubmitResult{}, err)); m["code"] != code || m["requestId"] != "s4" {
			t.Errorf("%v: %v, want %s", err, m, code)
		}
	}
	// A request id past its bounds names nothing.
	if m := decode(SubmitWord("é", SubmitResult{}, ErrBadSubmit)); m["code"] != proto.ErrCodeBadFrame || m["requestId"] != nil {
		t.Fatalf("a bad request id: %v", m)
	}
}
