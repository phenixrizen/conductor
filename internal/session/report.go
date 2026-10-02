package session

import "regexp"

// maxReport bounds what Input looks at to tell a terminal's automatic report
// from typing: a longer write is typing.
const maxReport = 256

// terminalReport matches what a terminal sends by itself, never a key press:
// the replies to a cursor position query (CPR, DECXCPR), a status report, the
// device attributes (DA1, DA2), a mode report (DECRPM), a window report, a
// colour query's reply (OSC 4, 10, 11, 12), a DCS reply (XTVERSION, DECRQSS),
// and the focus in and out reports. One write may carry several. A write of
// nothing else does not answer the prompt a session waits on (Input): xterm
// answers Codex's cursor position query after every turn. A mouse report is
// a person's click and is not one of them.
var terminalReport = regexp.MustCompile(`^(?:` +
	`\x1b\[\??\d{1,4};\d{1,4}R` + // CPR, DECXCPR
	`|\x1b\[[0-3]n` + // DSR status
	`|\x1b\[[?>][\d;]{0,64}c` + // DA1, DA2
	`|\x1b\[\??\d{1,5};\d{1,2}\$y` + // DECRPM
	`|\x1b\[\d{1,2}(?:;\d{1,5}){0,2}t` + // window reports
	`|\x1b\[[IO]` + // focus in, out
	`|\x1b\](?:4;\d{1,3}|1[0-2]);rgb:[0-9a-fA-F/]{1,64}(?:\x07|\x1b\\)` + // colour replies
	`|\x1bP[>!|0-9$+]{1,4}[^\x1b]{0,128}\x1b\\` + // DCS replies
	`)+$`)

// isTerminalReport reports whether data is only terminal reports.
func isTerminalReport(data []byte) bool {
	return len(data) > 0 && len(data) <= maxReport && data[0] == 0x1b && terminalReport.Match(data)
}
