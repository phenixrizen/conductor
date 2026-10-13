package session

import (
	"bytes"
	"regexp"
)

// maxReport bounds what Input looks at to tell a terminal's automatic report
// from typing: a longer write is typing.
const maxReport = 256

// terminalReports are the alternatives of terminalReport.
const terminalReports = `\x1b\[\??\d{1,4};\d{1,4}R` + // CPR, DECXCPR
	`|\x1b\[[0-3]n` + // DSR status
	`|\x1b\[[?>][\d;]{0,64}c` + // DA1, DA2
	`|\x1b\[\??\d{1,5};\d{1,2}\$y` + // DECRPM
	`|\x1b\[\d{1,2}(?:;\d{1,5}){0,2}t` + // window reports
	`|\x1b\[[IO]` + // focus in, out
	`|\x1b\](?:4;\d{1,3}|1[0-2]);rgb:[0-9a-fA-F/]{1,64}(?:\x07|\x1b\\)` + // colour replies
	`|\x1bP[>!|0-9$+]{1,4}[^\x1b]{0,128}\x1b\\` // DCS replies

// terminalReport matches what a terminal sends by itself, never a key press:
// the replies to a cursor position query (CPR, DECXCPR), a status report, the
// device attributes (DA1, DA2), a mode report (DECRPM), a window report, a
// colour query's reply (OSC 4, 10, 11, 12), a DCS reply (XTVERSION, DECRQSS),
// and the focus in and out reports. One write may carry several. A write of
// nothing else does not answer the prompt a session waits on (Input): xterm
// answers Codex's cursor position query after every turn. A mouse report is
// a person's click and is not one of them.
var terminalReport = regexp.MustCompile(`^(?:` + terminalReports + `)+$`)

// isTerminalReport reports whether data is only terminal reports.
func isTerminalReport(data []byte) bool {
	return len(data) > 0 && len(data) <= maxReport && data[0] == 0x1b && terminalReport.Match(data)
}

// oneReport matches a report that starts data: a terminal report, or a mouse
// report as SGR (ESC[<b;x;y then M or m) or urxvt (ESC[b;x;yM) sends it.
var oneReport = regexp.MustCompile(`^(?:` + terminalReports +
	`|\x1b\[<\d{1,4};\d{1,5};\d{1,5}[Mm]` +
	`|\x1b\[\d{1,4};\d{1,5};\d{1,5}M)`)

// legacyMouse starts a legacy mouse report: three bytes of 32 or more
// follow (button, column, row).
var legacyMouse = []byte("\x1b[M")

// reportBegun matches the start of a report that data ends before it is
// done, cut anywhere: the rest comes with the connection's next write (a
// read of a terminal of the person's own cut it there). A lone ESC may be
// the Escape key, or a report's first byte: it is taken as begun, and the
// next write tells (a key after it is typing).
var reportBegun = regexp.MustCompile(`^\x1b(?:` +
	`\[[<?>]?[\d;]{0,64}\$?` + // CSI: CPR, DSR, DA, DECRPM, window, focus; SGR and urxvt mouse
	`|\][\d;]{0,8}(?:r(?:g(?:b(?::[0-9a-fA-F/]{0,64}\x1b?)?)?)?)?` + // OSC colour reply, its ST begun
	`|P[>!|0-9$+]{0,4}[^\x1b]{0,128}\x1b?` + // DCS reply, its ST begun
	`)?$`)

// maxReportCarry bounds what of a report begun a write leaves for the next.
const maxReportCarry = 160

// reportLen is the length of the report data starts with, 0 for none.
func reportLen(data []byte) int {
	if bytes.HasPrefix(data, legacyMouse) && len(data) >= 6 && data[3] >= 32 && data[4] >= 32 && data[5] >= 32 {
		return 6
	}
	if loc := oneReport.FindIndex(data); loc != nil {
		return loc[1]
	}
	return 0
}

// typesSomething reports whether data, written after carry (a report the
// connection's last write began and did not finish), holds anything a
// person typed: anything but a terminal's own reports and its mouse reports
// (the pointer, moved or clicked, types nothing), whole, or begun at its end
// (a lone ESC among them, the Escape key or a report's first byte, which the
// next write tells apart); any number of them, mixed. rest is the report data
// ends before it is done, for the next write's carry (nil for none). It looks
// at no more than the write, at most proto.MaxInput bytes.
func typesSomething(carry, data []byte) (typed bool, rest []byte) {
	buf := data
	if len(carry) > 0 {
		buf = append(append(make([]byte, 0, len(carry)+len(data)), carry...), data...)
	}
	for len(buf) > 0 {
		if buf[0] != 0x1b {
			return true, nil
		}
		if n := reportLen(buf); n > 0 {
			buf = buf[n:]
			continue
		}
		if len(buf) <= maxReportCarry && (reportBegun.Match(buf) || legacyBegun(buf)) {
			return false, bytes.Clone(buf)
		}
		return true, nil
	}
	return false, nil
}

// legacyBegun reports whether data is a legacy mouse report not done: ESC[M
// and fewer than three bytes of 32 or more.
func legacyBegun(data []byte) bool {
	if !bytes.HasPrefix(data, legacyMouse) || len(data) >= 6 {
		return false
	}
	for _, b := range data[3:] {
		if b < 32 {
			return false
		}
	}
	return true
}
