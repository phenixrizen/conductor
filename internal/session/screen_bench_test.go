package session

import (
	"strings"
	"testing"
)

// The approval watcher's look at a full, large screen: what one quiet
// period costs.
func BenchmarkAskedLookAtALargeScreen(b *testing.B) {
	sc := NewScreen(300, 100)
	line := strings.Repeat("text proceed (y) words (esc) and more ", 8)[:290]
	for r := 0; r < 100; r++ {
		sc.Write([]byte(line + "\r\n"))
	}
	re := anyOf(approvalQuestions())
	text := sc.Text()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = re.MatchString(sc.Text())
	}
	_ = text
}
