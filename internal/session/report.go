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
	if len(data) > 1 && data[1] == '[' {
		return csiReportLen(data)
	}
	// No report is longer than maxReportCarry: the pattern is not given more.
	if loc := oneReport.FindIndex(data[:min(len(data), maxReportCarry)]); loc != nil {
		return loc[1]
	}
	return 0
}

// csiReportLen is reportLen for data that starts ESC [: oneReport's CSI
// alternatives, the terminal's (CPR, DSR, DA, DECRPM, window, focus) and
// the SGR and urxvt mouse reports, read by hand, for the pattern costs some
// microseconds a report and a write may hold thousands
// (TestCSIReportLenIsThePatterns keeps the two the same).
func csiReportLen(data []byte) int {
	i := 2
	var lead byte
	if i < len(data) && (data[i] == '?' || data[i] == '>' || data[i] == '<') {
		lead = data[i]
		i++
	}
	// The parameters: at most 64 digits and semicolons, each parameter's
	// digits counted.
	// digits are the digit counts of the first parameters, n how many
	// parameters there are.
	start := i
	var digits [4]int
	n := 1
scan:
	for ; i < len(data) && i-start <= 64; i++ {
		switch c := data[i]; {
		case c >= '0' && c <= '9':
			if n <= len(digits) {
				digits[n-1]++
			}
		case c == ';':
			n++
		default:
			break scan
		}
	}
	if i >= len(data) || i-start > 64 {
		return 0
	}
	params := i - start
	// within reports whether there are len(max) parameters, the k-th of 1
	// to max[k] digits.
	within := func(max ...int) bool {
		if n != len(max) {
			return false
		}
		for k, m := range max {
			if digits[k] < 1 || digits[k] > m {
				return false
			}
		}
		return true
	}
	switch data[i] {
	case 'R': // CPR, DECXCPR
		if (lead == 0 || lead == '?') && within(4, 4) {
			return i + 1
		}
	case 'n': // DSR status
		if lead == 0 && params == 1 && data[start] <= '3' {
			return i + 1
		}
	case 'c': // DA1, DA2
		if lead == '?' || lead == '>' {
			return i + 1
		}
	case '$': // DECRPM
		if (lead == 0 || lead == '?') && within(5, 2) && i+1 < len(data) && data[i+1] == 'y' {
			return i + 2
		}
	case 't': // window reports
		if lead == 0 && (within(2) || within(2, 5) || within(2, 5, 5)) {
			return i + 1
		}
	case 'I', 'O': // focus in, out
		if lead == 0 && params == 0 {
			return i + 1
		}
	case 'M', 'm': // SGR mouse; urxvt's, M alone
		if (lead == '<' || lead == 0 && data[i] == 'M') && within(4, 5, 5) {
			return i + 1
		}
	}
	return 0
}

// typesSomething reports whether data, written after carry (a report the
// connection's last write began and did not finish), holds anything a
// person typed: anything but a terminal's own reports and its mouse reports
// (the pointer, moved or clicked, types nothing), whole, or begun at its end
// (a lone ESC among them, the Escape key or a report's first byte, which the
// next write tells apart); any number of them, mixed. What the last write
// carried and turns out to be no report (an Escape) was typed then, not by
// this write. rest is the report data ends before it is done, for the next
// write's carry (nil for none). It looks at no more than the write, at most
// proto.MaxInput bytes.
func typesSomething(carry, data []byte) (typed bool, rest []byte) {
	buf := data
	carried := len(carry)
	if carried > 0 {
		buf = append(append(make([]byte, 0, carried+len(data)), carry...), data...)
	}
	// The whole write is read, also past what was typed, so that a report
	// begun at its end is carried whatever came before it.
	for pos := 0; pos < len(buf); {
		if buf[pos] == 0x1b {
			if n := reportLen(buf[pos:]); n > 0 {
				pos += n
				continue
			}
			if len(buf)-pos <= maxReportCarry && (reportBegun.Match(buf[pos:]) || legacyBegun(buf[pos:])) {
				return typed, bytes.Clone(buf[pos:])
			}
		}
		// Not a report's byte: one a person typed, the ESC of a key's
		// sequence among them; unless it came with the last write (an
		// Escape typed then, which no report followed), which this one
		// does not type again.
		if pos >= carried {
			typed = true
		}
		pos++
		if !typed {
			continue
		}
		// Typed: all that is left to tell is whether a report begins in
		// the last bytes, which an ESC there starts (a report's body holds
		// none but its terminator's).
		pos = max(pos, len(buf)-maxReportCarry)
		i := bytes.IndexByte(buf[pos:], 0x1b)
		if i < 0 {
			return true, nil
		}
		pos += i
	}
	return typed, nil
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
