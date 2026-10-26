package crew

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/phenixrizen/conductor/internal/crew/ledger"
)

// TaskFile is .conductor/task.json: an attempt's task as a tool reads it.
// task.md says the same for the agent to read.
type TaskFile struct {
	Run          string `json:"run"`
	Crew         string `json:"crew"`
	CrewName     string `json:"crewName"`
	Goal         string `json:"goal"`
	Task         string `json:"task"`
	Attempt      int    `json:"attempt"`
	MaxAttempts  int    `json:"maxAttempts"`
	Member       string `json:"member"`
	Role         string `json:"role,omitempty"`
	Kind         string `json:"kind"`
	Instructions string `json:"instructions"`
	Worktree     string `json:"worktree"`
	Branch       string `json:"branch"`
	Prefix       string `json:"prefix,omitempty"`
	Start        string `json:"start"`
	// StartFrom says what the start commit is, in words: the run's base, a
	// task's result, the commit a review approved, the attempt before's
	// result, a checkpoint, or the commit a review asked changes of (#355).
	StartFrom string        `json:"startFrom,omitempty"`
	Inputs    []TaskInput   `json:"inputs,omitempty"`
	Checks    []Check       `json:"checks,omitempty"`
	Previous  *TaskPrevious `json:"previous,omitempty"`
	// Repair is set when a review asked for changes: the review, the
	// commit it reviewed (where this attempt starts) and its findings.
	Repair *TaskRepair `json:"repair,omitempty"`
	// Checkpoint is where the task's last attempt got to (checkpoints.go),
	// when one was saved: what is done, what remains, notes, its commit.
	Checkpoint *Checkpoint `json:"checkpoint,omitempty"`
	// More names the file that has what was cut to keep the task files
	// within their bound (taskMoreFile), and Cut says what: the inputs'
	// summaries, the checkpoint, findings, the review's summary.
	More      string    `json:"more,omitempty"`
	Cut       []string  `json:"cut,omitempty"`
	WrittenAt time.Time `json:"writtenAt"`
}

// taskMoreFile is the task file beside task.md and task.json that has, in
// full, what was cut from them to keep them within maxTaskFile.
const taskMoreFile = "task-more.json"

// TaskMore is .conductor/task-more.json: what the task files cut.
type TaskMore struct {
	Inputs        []TaskInput   `json:"inputs,omitempty"`
	Checkpoint    *Checkpoint   `json:"checkpoint,omitempty"`
	RepairSummary string        `json:"repairSummary,omitempty"`
	Findings      []Finding     `json:"findings,omitempty"`
	Checks        []Check       `json:"checks,omitempty"`
	Previous      *TaskPrevious `json:"previous,omitempty"`
}

// maxPreviousError is the attempt before's error as a cut task file keeps
// it, in characters; task-more.json has it whole.
const maxPreviousError = 2000

// TaskRepair is what a review asked of the task.
type TaskRepair struct {
	Review   string    `json:"review"`
	Attempt  int       `json:"attempt"`
	Commit   string    `json:"commit"`
	Summary  string    `json:"summary,omitempty"`
	Findings []Finding `json:"findings"`
}

// TaskInput is a result the task starts from or has at hand.
type TaskInput struct {
	Task    string `json:"task"`
	Attempt int    `json:"attempt"`
	Commit  string `json:"commit,omitempty"`
	Branch  string `json:"branch,omitempty"`
	Summary string `json:"summary,omitempty"`
	Verdict string `json:"verdict,omitempty"`
}

// TaskPrevious is the attempt before this one at the same task.
type TaskPrevious struct {
	Attempt int    `json:"attempt"`
	State   string `json:"state"`
	Stage   string `json:"stage,omitempty"`
	Error   string `json:"error,omitempty"`
	// Result and Branch name the attempt's result when this one does not
	// start there (refused before its checks ran): its work, kept on its
	// branch for what holds up.
	Result string `json:"result,omitempty"`
	Branch string `json:"branch,omitempty"`
}

// taskFile renders a's task files: task.md for the agent, task.json for
// tools, and, when they would pass maxTaskFile, task-more.json with what
// was cut from them (fitTaskFile). It takes e.mu.
func (e *Engine) taskFile(r *run, a *attempt) (md, js, more []byte, err error) {
	e.mu.Lock()
	t := a.t
	f := TaskFile{Run: r.id, Crew: r.crewID, CrewName: r.name, Goal: r.goal, Task: t.def.ID, Attempt: a.n, MaxAttempts: maxAttemptsPerTask + t.extra, Member: a.m.def.Name, Role: a.m.def.Prompt, Kind: t.def.Kind,
		Instructions: t.def.Instructions, Worktree: a.worktree, Branch: a.branch, Prefix: r.prefix, Start: a.start, Checks: append([]Check(nil), t.def.Checks...), WrittenAt: time.Now().UTC()}
	for _, in := range a.inputs {
		f.Inputs = append(f.Inputs, TaskInput{Task: in.task, Attempt: in.attempt, Commit: in.commit, Branch: in.branch, Summary: in.summary, Verdict: in.verdict})
	}
	for _, p := range t.attempts {
		if p.n == a.n-1 {
			f.Previous = &TaskPrevious{Attempt: p.n, State: p.state, Stage: p.stage, Error: p.err}
			if p.result != "" && p.result != a.start {
				f.Previous.Result, f.Previous.Branch = p.result, p.branch
			}
		}
	}
	if rf := t.refused; f.Previous == nil && rf != nil && rf.n == a.n-1 {
		f.Previous = &TaskPrevious{Attempt: rf.n, State: ledger.AttemptFailed, Stage: rf.stage, Error: rf.err, Result: rf.result, Branch: rf.branch}
	}
	f.StartFrom = startFrom(r, t, a)
	if rp := t.repair; rp != nil {
		f.Repair = &TaskRepair{Review: rp.review, Attempt: rp.attempt, Commit: rp.commit, Summary: rp.summary, Findings: append([]Finding(nil), rp.findings...)}
	}
	if c := t.checkpoint; c != nil && (a.checkpoint == nil || a.checkpoint != c) {
		f.Checkpoint = c.api()
	}
	e.mu.Unlock()
	return fitTaskFile(f)
}

// fitTaskFile renders f, cutting what is needed least until task.md and
// task.json are within maxTaskFile: the inputs' summaries, then the
// checkpoint, then the findings from the last, then the review's summary,
// then the checks' commands, then the inputs after the first (the start's),
// then the attempt before's error past maxPreviousError; what is cut goes,
// in full, into task-more.json, which task.md names. The crew's goal, role
// and instructions are never cut: the crew's validation bounds them
// (maxTaskText), so a task file always fits and no attempt fails at it for
// its size; one that does not after every cut is an error, never written.
func fitTaskFile(f TaskFile) (md, js, more []byte, err error) {
	// What is cut is cut from copies: the caller's slices stay as they are.
	f.Inputs, f.Checks = slices.Clone(f.Inputs), slices.Clone(f.Checks)
	if f.Repair != nil {
		rp := *f.Repair
		rp.Findings = slices.Clone(rp.Findings)
		f.Repair = &rp
	}
	if f.Previous != nil {
		p := *f.Previous
		f.Previous = &p
	}
	var m TaskMore
	cuts := []func() bool{
		func() bool {
			cut := false
			for i := range f.Inputs {
				if f.Inputs[i].Summary != "" {
					in := f.Inputs[i]
					m.Inputs = append(m.Inputs, TaskInput{Task: in.Task, Attempt: in.Attempt, Summary: in.Summary})
					f.Inputs[i].Summary, cut = "", true
				}
			}
			if cut {
				f.Cut = append(f.Cut, "the inputs' summaries")
			}
			return cut
		},
		func() bool {
			if f.Checkpoint == nil {
				return false
			}
			m.Checkpoint, f.Checkpoint = f.Checkpoint, nil
			f.Cut = append(f.Cut, "the last attempt's checkpoint")
			return true
		},
		func() bool {
			if f.Repair == nil || len(f.Repair.Findings) == 0 {
				return false
			}
			n := len(f.Repair.Findings)
			last := f.Repair.Findings[n-1]
			m.Findings = append([]Finding{last}, m.Findings...)
			f.Repair.Findings = f.Repair.Findings[:n-1]
			f.Cut = slices.DeleteFunc(f.Cut, func(s string) bool { return strings.HasSuffix(s, "of the review's findings") })
			f.Cut = append(f.Cut, fmt.Sprintf("the last %d of the review's findings", len(m.Findings)))
			return true
		},
		func() bool {
			if f.Repair == nil || f.Repair.Summary == "" {
				return false
			}
			m.RepairSummary, f.Repair.Summary = f.Repair.Summary, ""
			f.Cut = append(f.Cut, "the review's summary")
			return true
		},
		func() bool {
			if len(f.Checks) == 0 {
				return false
			}
			m.Checks = f.Checks
			f.Checks = nil
			f.Cut = append(f.Cut, fmt.Sprintf("the %d checks' commands", len(m.Checks)))
			return true
		},
		func() bool {
			if len(f.Inputs) <= 1 {
				return false
			}
			for _, in := range f.Inputs[1:] {
				// Its summary may be there already, cut before.
				if i := slices.IndexFunc(m.Inputs, func(x TaskInput) bool { return x.Task == in.Task }); i >= 0 {
					in.Summary = m.Inputs[i].Summary
					m.Inputs[i] = in
				} else {
					m.Inputs = append(m.Inputs, in)
				}
			}
			f.Cut = append(f.Cut, fmt.Sprintf("the %d inputs after the first (merge their branches too)", len(f.Inputs)-1))
			f.Inputs = f.Inputs[:1]
			return true
		},
		func() bool {
			// Once: the cut error is maxPreviousError and a mark, longer
			// than maxPreviousError itself.
			if f.Previous == nil || m.Previous != nil || utf8.RuneCountInString(f.Previous.Error) <= maxPreviousError {
				return false
			}
			whole := *f.Previous
			m.Previous = &whole
			f.Previous.Error = string([]rune(f.Previous.Error)[:maxPreviousError]) + "…"
			f.Cut = append(f.Cut, "the rest of the attempt before's error")
			return true
		},
	}
	// Each cut takes something from the files and says so, once: the
	// loop ends at the last.
	for {
		if len(f.Cut) > 0 {
			f.More = taskMoreFile
		}
		md = []byte(taskMarkdown(f))
		js, err = encodeTaskJSON(f)
		if err != nil {
			return nil, nil, nil, err
		}
		if len(md) <= maxTaskFile && len(js) <= maxTaskFile {
			break
		}
		cut := false
		for _, c := range cuts {
			if cut = c(); cut {
				break
			}
		}
		if !cut {
			return nil, nil, nil, fmt.Errorf("the task files are %d and %d bytes with every part cut that can be, more than %d", len(md), len(js), maxTaskFile)
		}
	}
	if f.More != "" {
		if more, err = encodeTaskJSON(m); err != nil {
			return nil, nil, nil, err
		}
	}
	return md, js, more, nil
}

// encodeTaskJSON is v as a task file keeps it: indented, and with "<",
// ">" and "&" as they are (an agent reads it; escaped, each is six bytes).
func encodeTaskJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// taskMarkdown is task.md: what the task is, where the attempt is, what
// it starts from, what is checked, what went before, and how to finish.
func taskMarkdown(f TaskFile) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Task %q for %s (attempt %d of %d)\n\n", f.Task, f.Member, f.Attempt, f.MaxAttempts)
	fmt.Fprintf(&b, "Conductor runs the crew %q toward this goal:\n\n%s\n\n", f.CrewName, strings.TrimSpace(f.Goal))
	if strings.TrimSpace(f.Role) != "" {
		fmt.Fprintf(&b, "Your role: %s\n\n", strings.TrimSpace(f.Role))
	}
	fmt.Fprintf(&b, "## Instructions\n\n%s\n\n", strings.TrimSpace(f.Instructions))
	fmt.Fprintf(&b, "## Where you are\n\nYour worktree is `%s`, on the branch `%s`, at the commit %s", f.Worktree, f.Branch, f.Start)
	if f.StartFrom != "" {
		fmt.Fprintf(&b, " (%s)", f.StartFrom)
	}
	b.WriteString(".")
	if f.Prefix != "" {
		fmt.Fprintf(&b, " The crew works in `%s` inside it; that is your working directory.", f.Prefix)
	}
	b.WriteString("\n\n")
	if len(f.Inputs) > 0 {
		b.WriteString("## Inputs\n\nThe results this task starts from:\n\n")
		for _, in := range f.Inputs {
			fmt.Fprintf(&b, "- task %q, attempt %d: commit %s", in.Task, in.Attempt, in.Commit)
			if in.Branch != "" {
				fmt.Fprintf(&b, " on the branch `%s`", in.Branch)
			}
			if in.Verdict != "" {
				fmt.Fprintf(&b, ", verdict %s", in.Verdict)
			}
			if s := strings.TrimSpace(in.Summary); s != "" {
				fmt.Fprintf(&b, ": %s", s)
			}
			b.WriteString("\n")
		}
		if f.Kind == TaskWork && len(f.Inputs) > 1 {
			b.WriteString("\nYour worktree starts at the first; merge the branches of the others into it before you submit. Your result must descend from every input.\n")
		}
		b.WriteString("\n")
	}
	if len(f.Checks) == 0 && slices.ContainsFunc(f.Cut, func(s string) bool { return strings.HasSuffix(s, "checks' commands") }) {
		b.WriteString("## Checks\n\nAfter you submit, Conductor runs checks in a fresh checkout of your result commit (their commands are in the file named under \"Cut to fit\"). Each must exit 0 without changing tracked files.\n\n")
	}
	if len(f.Checks) > 0 {
		b.WriteString("## Checks\n\nAfter you submit, Conductor runs these in a fresh checkout of your result commit. Each must exit 0 without changing tracked files:\n\n")
		for _, ch := range f.Checks {
			fmt.Fprintf(&b, "- %s: `%s`", ch.ID, strings.Join(ch.Argv, " "))
			if ch.TimeoutSeconds > 0 {
				fmt.Fprintf(&b, " (%ds)", ch.TimeoutSeconds)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if rp := f.Repair; rp != nil {
		fmt.Fprintf(&b, "## Findings to address\n\nReview %q (attempt %d) asked for changes to the commit %s, where this attempt starts", rp.Review, rp.Attempt, rp.Commit)
		if s := strings.TrimSpace(rp.Summary); s != "" {
			fmt.Fprintf(&b, ": %s", s)
		}
		b.WriteString(".\n\n")
		for _, fd := range rp.Findings {
			fmt.Fprintf(&b, "- %s", fd.Severity)
			if fd.Path != "" {
				fmt.Fprintf(&b, " %s", fd.Path)
				if fd.Line > 0 {
					fmt.Fprintf(&b, ":%d", fd.Line)
				}
			}
			fmt.Fprintf(&b, ": %s\n", strings.TrimSpace(fd.Message))
		}
		b.WriteString("\nAddress each finding, or say in your summary why not; the review runs again on your result.\n\n")
	}
	if c := f.Checkpoint; c != nil {
		fmt.Fprintf(&b, "## Where the last attempt got to\n\nIts checkpoint, at the commit %s", c.Commit)
		if c.Commit == f.Start {
			b.WriteString(", where this attempt starts")
		}
		fmt.Fprintf(&b, ":\n\n- Done: %s\n- Remaining: %s\n", strings.TrimSpace(c.Done), strings.TrimSpace(c.Remaining))
		if n := strings.TrimSpace(c.Notes); n != "" {
			fmt.Fprintf(&b, "- Notes: %s\n", n)
		}
		b.WriteString("\n")
	}
	if p := f.Previous; p != nil {
		fmt.Fprintf(&b, "## The attempt before\n\nAttempt %d %s", p.Attempt, p.State)
		if p.Stage != "" {
			fmt.Fprintf(&b, " at %s", p.Stage)
		}
		if p.Error != "" {
			fmt.Fprintf(&b, ": %s", p.Error)
		}
		if p.Result != "" {
			fmt.Fprintf(&b, ". Its result, the commit %s on the branch %s, is not where you start: take from it what holds up, and fix what was refused", p.Result, p.Branch)
		}
		b.WriteString("\n\n")
	}
	if f.More != "" {
		fmt.Fprintf(&b, "## Cut to fit\n\nThis file keeps within %d KiB: %s are in `%s/%s`, in full. Read it.\n\n", maxTaskFile>>10, strings.Join(f.Cut, ", "), taskDir, f.More)
	}
	b.WriteString("## How to finish\n\n")
	if f.Kind == TaskReview {
		b.WriteString("Do not commit. Review the candidate at HEAD, the input's result. Then submit once: the `task_result` tool with the verdict `approved` or `needs_changes` and your findings (severity blocker, major, minor or nit; path; line; message), or `conductor task result --verdict <verdict> --finding ...`.\n")
	} else {
		b.WriteString("Commit your work on this branch. Then submit once: the `task_result` tool with a summary, or `conductor task result --summary \"...\"`. Conductor takes the commit at HEAD; a worktree with changes not committed (apart from .conductor/) is refused, and so is a result that does not descend from every input.\n")
	}
	b.WriteString("\nA done or a handoff does not finish a task. A question goes through Conductor's ask, and a person answers it.\n")
	return b.String()
}

// writeTaskFiles writes an attempt's task files under <worktree>/<prefix>/
// .conductor through a root at that directory: the directory is made
// when missing, refused when it exists as anything but a directory (a
// symbolic link the commit tracks). Its .gitignore keeps the files out of
// the result.
func writeTaskFiles(worktree, prefix string, md, js, more []byte) error {
	root, err := os.OpenRoot(filepath.Join(worktree, prefix))
	if err != nil {
		return err
	}
	defer root.Close()
	fi, err := root.Lstat(taskDir)
	switch {
	case err == nil && !fi.IsDir():
		return fmt.Errorf("%s exists in the worktree and is not a directory", taskDir)
	case errors.Is(err, fs.ErrNotExist):
		if err := root.Mkdir(taskDir, 0o755); err != nil {
			return err
		}
	case err != nil:
		return err
	}
	files := map[string][]byte{".gitignore": []byte("*\n"), "task.md": md, "task.json": js}
	if more != nil {
		files[taskMoreFile] = more
	}
	for name := range files {
		// A fresh worktree has none; one there (tracked, a link) is not
		// written over.
		if _, err := root.Lstat(path.Join(taskDir, name)); err == nil {
			return fmt.Errorf("%s/%s exists in the worktree already: the task file is not written over it", taskDir, name)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	for name, b := range files {
		if err := root.WriteFile(path.Join(taskDir, name), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// crewAsLaunched is the crew as launched, for the ledger's run row.
func crewAsLaunched(c Crew) string {
	b, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	return string(b)
}

// startFrom says in words what a's start commit is (TaskFile.StartFrom,
// #355): the commit a review asked changes of; the attempt before's result
// or checkpoint; an input's result, or the commit a review approved; the
// run's base; "" for none of those. The caller holds e.mu.
func startFrom(r *run, t *task, a *attempt) string {
	if a.start == "" {
		return ""
	}
	if rp := t.repair; rp != nil && rp.commit == a.start {
		return fmt.Sprintf("the commit review %q asked changes of", rp.review)
	}
	for _, p := range t.attempts {
		if p.n != a.n-1 {
			continue
		}
		if p.result == a.start {
			return fmt.Sprintf("attempt %d's result", p.n)
		}
		if p.checkpoint != nil && p.checkpoint.commit == a.start {
			return fmt.Sprintf("attempt %d's checkpoint", p.n)
		}
		if p.result != "" && refusedBeforeChecks(p.stage) && p.start == a.start {
			return fmt.Sprintf("where attempt %d started: its result was refused at %s", p.n, p.stage)
		}
	}
	if rf := t.refused; rf != nil && rf.n == a.n-1 && rf.start == a.start {
		return fmt.Sprintf("where attempt %d started: its result was refused at %s", rf.n, rf.stage)
	}
	if t.checkpoint != nil && t.checkpoint.commit == a.start {
		return "the checkpoint the last attempt saved"
	}
	for _, in := range a.inputs {
		if in.commit != a.start {
			continue
		}
		if in.verdict != "" {
			return fmt.Sprintf("the commit review %q reviewed, verdict %s", in.task, in.verdict)
		}
		return fmt.Sprintf("task %q's result", in.task)
	}
	if a.start == r.base {
		return "the run's base"
	}
	return ""
}

// taskTextBytes is how many bytes the crew's own text of a task takes in
// its task.json (encodeTaskJSON): each string as JSON, quotes and escapes
// included.
func taskTextBytes(texts ...string) int {
	n := 0
	for _, s := range texts {
		b, _ := encodeTaskJSON(s)
		n += len(b)
	}
	return n
}
