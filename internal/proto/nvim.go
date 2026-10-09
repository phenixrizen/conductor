package proto

import "encoding/json"

// The editor's Neovim bridge (design round 12, F8): a viewer with control
// opens the real `nvim` on the machine that runs the session for one file,
// sends it keys in Neovim notation, and gets the buffer's changes, the
// cursor, the mode, the command line and the messages back. Every message
// is a control frame under MaxControl; a `lines` event is cut to fit.
const (
	CtlNvimOpen  = "nvim_open"
	CtlNvimInput = "nvim_input"
	CtlNvimClose = "nvim_close"
	CtlNvimEvent = "nvim_event"
	// CtlNvimSwap answers a swap file the editor found (round 13, G3): one
	// of the NvimSwap* choices, never a command.
	CtlNvimSwap = "nvim_swap"

	// ErrCodeNvimUnavailable says the machine has no `nvim`, or the session
	// does not offer the editor to this connection.
	ErrCodeNvimUnavailable = "nvim_unavailable"

	// MaxNvimKeys bounds one nvim_input's keys (Neovim notation).
	MaxNvimKeys = 256
	// MaxNvimLine bounds one line in a `lines` event: a longer line is cut
	// and the event says so (the buffer then differs from the editor's).
	MaxNvimLine = 4096
	// MaxNvimPerSub and MaxNvimPerSession bound the editors a connection and
	// a session keep open at once.
	MaxNvimPerSub     = 2
	MaxNvimPerSession = 8
	// nvimLinesBudget is what a `lines` event's lines may take once escaped,
	// leaving room for the envelope under MaxControl.
	nvimLinesBudget = MaxControl - 1024
)

// The kinds of an nvim_event.
const (
	NvimOpened  = "opened"
	NvimLines   = "lines"
	NvimCursor  = "cursor"
	NvimMode    = "mode"
	NvimCmdline = "cmdline"
	NvimMessage = "message"
	NvimWritten = "written"
	NvimClosed  = "closed"
	NvimError   = "error"
	// NvimSwapFound says the file has another editor's swap file: the
	// editor opened it read-only, and Swap says whose (round 13, G3).
	NvimSwapFound = "swap"
)

// The choices of an nvim_swap: edit the file anyway (the editor leaves
// read-only); recover the swap file's text into the buffer (only when the
// process that wrote it is gone); delete the swap file and edit (the same).
const (
	NvimSwapEdit    = "edit"
	NvimSwapRecover = "recover"
	NvimSwapDelete  = "delete"
)

// NvimSwap answers the swap file editor ID found with Choice.
type NvimSwap struct {
	T      string `json:"t"`
	ID     string `json:"id"`
	Choice string `json:"choice"`
}

// NvimSwapInfo is another editor's swap file for the file opened: the swap
// file's path, the process that wrote it, whether that process still runs
// on this machine, its user and host, whether it holds changes not written
// to the file, and when it was last written (RFC 3339).
type NvimSwapInfo struct {
	File     string `json:"file"`
	Pid      int    `json:"pid,omitempty"`
	Running  bool   `json:"running"`
	User     string `json:"user,omitempty"`
	Host     string `json:"host,omitempty"`
	Modified bool   `json:"modified,omitempty"`
	Mtime    string `json:"mtime,omitempty"`
}

// NvimOpen asks for a Neovim on Path (relative to the working directory, or
// absolute inside it). The reply is an nvim_event of kind `opened` carrying
// ReqID and the editor's id, or kind `error` carrying ReqID.
type NvimOpen struct {
	T     string `json:"t"`
	ReqID string `json:"reqId"`
	Path  string `json:"path"`
}

// NvimInput sends Keys (Neovim notation, `<Esc>`, `<C-x>`, `<lt>` for `<`)
// to the editor ID. Seq, when set, is the page's number for these keys,
// rising: the `cursor` event that follows once Neovim has handled them
// carries it as Ack (round 13, G5, the local echo).
type NvimInput struct {
	T    string `json:"t"`
	ID   string `json:"id"`
	Keys string `json:"keys"`
	Seq  uint32 `json:"seq,omitempty"`
}

// NvimClose ends the editor ID.
type NvimClose struct {
	T  string `json:"t"`
	ID string `json:"id"`
}

// NvimEvent is what the editor reports. Kind says which fields are set:
// `opened` (ReqID, Path); `lines` (First, Last, Lines, Truncated: the buffer
// lines [First, Last) become Lines, Last -1 meaning to the end); `cursor`
// (Line, Col 1-based, Mode, VisualLine and VisualCol the other end of a
// visual selection); `mode` (Mode); `cmdline` (Show, Content, Pos, Prompt);
// `message` (Text, MessageKind); `written` (Path); `closed` (Reason);
// `error` (ReqID, Code, Message); `swap` (Swap).
type NvimEvent struct {
	T     string `json:"t"`
	ID    string `json:"id,omitempty"`
	Kind  string `json:"kind"`
	ReqID string `json:"reqId,omitempty"`
	Path  string `json:"path,omitempty"`

	First     int      `json:"first,omitempty"`
	Last      int      `json:"last,omitempty"`
	Lines     []string `json:"lines,omitempty"`
	Truncated bool     `json:"truncated,omitempty"`

	Line       int    `json:"line,omitempty"`
	Col        int    `json:"col,omitempty"`
	Mode       string `json:"mode,omitempty"`
	VisualLine int    `json:"visualLine,omitempty"`
	VisualCol  int    `json:"visualCol,omitempty"`

	Show    bool   `json:"show,omitempty"`
	Content string `json:"content,omitempty"`
	Pos     int    `json:"pos,omitempty"`
	Prompt  string `json:"prompt,omitempty"`

	Text        string `json:"text,omitempty"`
	MessageKind string `json:"messageKind,omitempty"`
	Reason      string `json:"reason,omitempty"`
	Code        string `json:"code,omitempty"`
	Message     string `json:"message,omitempty"`

	Swap *NvimSwapInfo `json:"swap,omitempty"`
	// Ack, on a `cursor` event, says Neovim has handled the keys of the
	// nvim_input with that Seq and every one before, every buffer change
	// they made sent already (round 13, G5).
	Ack uint32 `json:"ack,omitempty"`
}

// NvimLineEvents turns one buffer change (the lines [first, last) become
// lines; last -1 means to the end) into `lines` events that each fit
// MaxControl: the first carries the change's own range, each later one
// inserts where the previous left off. A line past MaxNvimLine is cut and
// its event marked Truncated. An empty change (a deletion) is one event.
func NvimLineEvents(id string, first, last int, lines []string) []NvimEvent {
	var out []NvimEvent
	ev := NvimEvent{T: CtlNvimEvent, ID: id, Kind: NvimLines, First: first, Last: last}
	used := 0
	at := first // where the next event inserts
	for _, l := range lines {
		truncated := false
		if len(l) > MaxNvimLine {
			l, truncated = cutUTF8(l, MaxNvimLine), true
		}
		b, _ := json.Marshal(l)
		if used+len(b) > nvimLinesBudget && len(ev.Lines) > 0 {
			out = append(out, ev)
			at += len(ev.Lines)
			ev = NvimEvent{T: CtlNvimEvent, ID: id, Kind: NvimLines, First: at, Last: at}
			used = 0
		}
		ev.Lines = append(ev.Lines, l)
		ev.Truncated = ev.Truncated || truncated
		used += len(b) + 1
	}
	return append(out, ev)
}

// cutUTF8 cuts s to at most n bytes on a rune boundary.
func cutUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}
