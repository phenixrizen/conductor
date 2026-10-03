package agents

import (
	"bytes"
	"path/filepath"
)

// Skill is the Conductor skill, a SKILL.md: it tells an agent working in a
// Conductor session when and how to report progress, artifacts, blockers
// and handoffs with conductor notify, and how to form a crew around its
// session with conductor crew. `conductor skill` prints it, WriteAssets
// writes it to the hooks dir as skills/conductor/SKILL.md (every session
// names that copy in CONDUCTOR_SKILL), and Install and a launch copy it
// for the agents that read skills (Adapter.SkillPath): Claude Code and
// Codex into their own skills directory, the Antigravity CLI into its, the
// others into the shared ~/.agents/skills. It names no binary path: its
// commands run the binary the session names in CONDUCTOR_BIN, else the
// conductor on PATH. The line after the frontmatter marks the file as
// Conductor's (skillMarker).
const Skill = `---
name: conductor
description: Report progress, artifacts, blockers and handoffs to Conductor, and form a crew around your session, while working in a Conductor session
---

<!-- conductor:skill (written by Conductor, which replaces this file when it installs its hooks; delete this line to keep your own changes) -->

# Conductor

Conductor runs coding agents in terminals that people watch and share from a
browser. In a Conductor session, conductor notify tells them what you are
doing without anyone reading the terminal: each report appears in the
session's activity and on the Events page. You are in a Conductor session
when CONDUCTOR_NOTIFY_URL and CONDUCTOR_NOTIFY_TOKEN are set.

With Conductor's MCP server registered (Claude Code and Codex get it at
launch), the same reports and crew actions are tools: report, set_state,
ask, form_crew, add_member, run_status and link. Call those when you have
them; the commands below do the same.

## When to report

- Progress on a long task, at milestones rather than at every step:

      "${CONDUCTOR_BIN:-conductor}" notify --event progress --message "4/7 handlers"

- An artifact someone should look at, such as a pull request, a preview
  deployment or a report (add --message to say what it is):

      "${CONDUCTOR_BIN:-conductor}" notify --event artifact --url <url>

- A handoff, when the next part of the work belongs to another member of the
  crew:

      "${CONDUCTOR_BIN:-conductor}" notify --event handoff --to <member> --message "…"

- A decision you cannot make yourself, such as a choice between designs, a
  missing credential or an approval: ask, then wait for the answer in the
  terminal. The session shows as needing input until someone replies.

      "${CONDUCTOR_BIN:-conductor}" notify --state needs_input --message "…"

- A question with a few answers: name them in the terminal on their own
  line, then ask with the same words as choices, so one click types the
  answer as a line (at most 6, each at most 40 characters):

      "${CONDUCTOR_BIN:-conductor}" notify --state needs_input --message "Which database?" --choices "Postgres|SQLite|Keep both"

## Form a crew

When the work splits into parts that can run side by side, such as a feature
and its tests or a migration and its docs, form a crew around this session:
every other member gets a session of its own, and you stay in yours as the
lead. Write the crew as JSON, then create it; --open offers the run to the
people watching.

      "${CONDUCTOR_BIN:-conductor}" crew create crew.json --self lead --open

    {
      "name": "users api",
      "goal": "ship /v1/users with tests and docs",
      "members": [
        {"name": "lead", "agentId": "", "prompt": "", "start": {"when": "manual"}},
        {"name": "tests", "agentId": "claude", "prompt": "Write the tests for $GOAL; lead has the handler", "start": {"when": "after", "member": "lead"}},
        {"name": "docs", "agentId": "codex", "prompt": "Document $GOAL", "start": {"when": "immediately"}}
      ]
    }

- The lead is you: its agentId and prompt stay empty and its start is manual.
  Every other member names an agent of Conductor's catalog (your own id is in
  CONDUCTOR_AGENT; use it unless told otherwise) and gets a prompt, in which
  $GOAL is replaced by the goal.
- A member that starts "after" you begins when you next finish a turn, so do
  your part first; one that starts "immediately" begins at once; "manual"
  waits for a person. Hand details to a member with a handoff (above).
- Never form a crew for work you can finish alone, and at most 2 a session.
  A crew has at most 12 members and works in this session's directory.
- Later, crew status lists the members and their states, crew add member.json
  adds one member (the shape of one entry above), and crew link prints a
  view-only link to this session for a pull request or a message:

      "${CONDUCTOR_BIN:-conductor}" crew status
      "${CONDUCTOR_BIN:-conductor}" crew add member.json
      "${CONDUCTOR_BIN:-conductor}" crew link --label "for the PR"

## Rules

- Never call notify or crew outside a Conductor session; the commands exit silently there.
- Keep a message to one short line, at most 500 characters. A URL is at most
  2048 bytes, a member name at most 40 characters.
- Do not report every tool call or file edit: Conductor's hooks report those
  when the session asks for them.
`

// skillAsset is where WriteAssets puts the skill under the hooks dir.
const skillAsset = "skills/conductor/SKILL.md"

// skillMarker marks a SKILL.md as Conductor's own, which Install replaces; a
// file without it is the user's.
const skillMarker = "conductor:skill"

var skillAssets = map[string]string{skillAsset: Skill}

// Where the agents that read skills find the Conductor skill, under home.
const (
	claudeSkill = ".claude/skills/conductor/SKILL.md"
	codexSkill  = ".codex/skills/conductor/SKILL.md"
	// agentsSkill is in the skills directory agents share (the Agent Skills
	// convention): pi, Goose, Cursor, Copilot, OpenCode, oh-my-pi, Amp and
	// DeepSeek Harness read it.
	agentsSkill = ".agents/skills/conductor/SKILL.md"
	// agySkill is the Antigravity CLI's global skills directory.
	agySkill = ".gemini/antigravity-cli/skills/conductor/SKILL.md"
)

// SkillFile is the skill's path under the hooks dir, which every session
// gets as CONDUCTOR_SKILL; empty unless hooksDir is absolute (a relative
// path would be read from the session's working directory).
func SkillFile(hooksDir string) string {
	if !filepath.IsAbs(hooksDir) {
		return ""
	}
	return filepath.Join(hooksDir, filepath.FromSlash(skillAsset))
}

// InstallSkill puts the Conductor skill where the agent of adapter agentID
// reads skills under home (Adapter.SkillPath): the file's path, whether it
// was written (false when it was Conductor's already), and ErrByHand for a
// SKILL.md of the user's own there, which is left to them. An adapter that
// reads no skills, or none, gives "", false, nil. A launch runs it before
// the agent starts; conductor hooks install does the same within Install.
func InstallSkill(agentID, home, hooksDir string) (path string, changed bool, err error) {
	a, ok := Get(agentID)
	if !ok || a.SkillPath == "" {
		return "", false, nil
	}
	path = filepath.Join(home, filepath.FromSlash(a.SkillPath))
	touched, err := install(home, skillStep(hooksDir, a.SkillPath))
	return path, len(touched) > 0, err
}

// skillStep is the step that makes the file rel under home a copy of the
// skill. A file there that does not carry skillMarker is the user's own and
// is left to them (ErrByHand); an empty one counts as none.
func skillStep(hooksDir, rel string) step {
	return step{rel, func(h *homeDir) (bool, error) {
		cur, ok, err := h.read(rel)
		if err != nil {
			return false, err
		}
		if ok && len(bytes.TrimSpace(cur)) > 0 && !bytes.Contains(cur, []byte(skillMarker)) {
			return false, byHand("%s is not Conductor's skill (it has no %s line), and Conductor does not overwrite it; to use Conductor's, replace it with what conductor skill prints", h.path(rel), skillMarker)
		}
		b, err := assetFor(skillAssets, hooksDir, skillAsset)
		if err != nil {
			return false, err
		}
		return h.write(rel, b)
	}}
}
