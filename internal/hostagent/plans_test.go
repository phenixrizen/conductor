package hostagent

import (
	"testing"

	"github.com/rocksolidlabs/conductor/internal/session"
)

// A plan set on the question showing, its id, words and choices the same
// (the dialog's footer read after the hook's report, #289), is a change
// the host tells its server: the server's copy, which the Yard reads, is
// how it offers Open plan.
func TestAPlanSetOnTheQuestionShowingIsAChange(t *testing.T) {
	x := session.Attention{State: session.AttentionNeedsInput, Message: "Would you like to proceed?", Source: session.SourceQuestion, Kind: session.KindPermission, ID: "0123456789abcdef", Options: []session.Option{{Label: "Yes", Input: "1"}}}
	y := x
	if !sameAttention(x, y) {
		t.Fatal("the same question")
	}
	y.Plan = "/home/dev/.claude/plans/plan.md"
	if sameAttention(x, y) {
		t.Fatal("the plan set on it is a change")
	}
}
