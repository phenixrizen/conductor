package crew

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// TaskFile is .conductor/task.json: an attempt's task as a tool reads it.
// task.md says the same for the agent to read.
type TaskFile struct {
	Run          string        `json:"run"`
	Crew         string        `json:"crew"`
	CrewName     string        `json:"crewName"`
	Goal         string        `json:"goal"`
	Task         string        `json:"task"`
	Attempt      int           `json:"attempt"`
	MaxAttempts  int           `json:"maxAttempts"`
	Member       string        `json:"member"`
	Role         string        `json:"role,omitempty"`
	Kind         string        `json:"kind"`
	Instructions string        `json:"instructions"`
	Worktree     string        `json:"worktree"`
	Branch       string        `json:"branch"`
	Prefix       string        `json:"prefix,omitempty"`
	Start        string        `json:"start"`
	Inputs       []TaskInput   `json:"inputs,omitempty"`
	Checks       []Check       `json:"checks,omitempty"`
	Previous     *TaskPrevious `json:"previous,omitempty"`
	// Repair is set when a review asked for changes: the review, the
	// commit it reviewed (where this attempt starts) and its findings.
	Repair *TaskRepair `json:"repair,omitempty"`
	// Checkpoint is where the task's last attempt got to (checkpoints.go),
	// when one was saved: what is done, what remains, notes, its commit.
	Checkpoint *Checkpoint `json:"checkpoint,omitempty"`
	WrittenAt  time.Time   `json:"writtenAt"`
}

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
}

// taskFile renders a's task files: task.md for the agent, task.json for
// tools. It takes e.mu.
func (e *Engine) taskFile(r *run, a *attempt) (md, js []byte, err error) {
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
		}
	}
	if rp := t.repair; rp != nil {
		f.Repair = &TaskRepair{Review: rp.review, Attempt: rp.attempt, Commit: rp.commit, Summary: rp.summary, Findings: append([]Finding(nil), rp.findings...)}
	}
	if c := t.checkpoint; c != nil && (a.checkpoint == nil || a.checkpoint != c) {
		f.Checkpoint = c.api()
	}
	e.mu.Unlock()
	js, err = json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	md = []byte(taskMarkdown(f))
	if len(md) > maxTaskFile {
		md = append(md[:maxTaskFile-64], []byte("\n\n(cut here: the task file is at its bound)\n")...)
	}
	if len(js) > maxTaskFile {
		return nil, nil, fmt.Errorf("the task's JSON is more than %d bytes", maxTaskFile)
	}
	return md, js, nil
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
	if len(f.Inputs) > 0 && f.Inputs[0].Commit == f.Start {
		fmt.Fprintf(&b, " (task %q's result)", f.Inputs[0].Task)
	} else {
		b.WriteString(" (the run's base)")
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
		b.WriteString("\n\n")
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
func writeTaskFiles(worktree, prefix string, md, js []byte) error {
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
