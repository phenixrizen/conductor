package agents

// Skill is the Conductor skill, a SKILL.md: it tells an agent working in a
// Conductor session when and how to report progress, artifacts, blockers
// and handoffs with conductor notify. `conductor skill` prints it,
// WriteAssets writes it to the hooks dir as skills/conductor/SKILL.md, and
// Install copies it for the agents that read skills: Claude Code and Codex
// into their own skills directory, pi and Goose into the shared
// ~/.agents/skills. It names no binary path: the agent runs the conductor on
// its PATH.
const Skill = `---
name: conductor
description: Report progress, artifacts, blockers and handoffs to Conductor while working in a Conductor session
---

# Conductor

Conductor runs coding agents in terminals that people watch and share from a
browser. In a Conductor session, conductor notify tells them what you are
doing without anyone reading the terminal: each report appears in the
session's activity and on the Events page. You are in a Conductor session
when CONDUCTOR_NOTIFY_URL and CONDUCTOR_NOTIFY_TOKEN are set.

## When to report

- Progress on a long task, at milestones rather than at every step:

      conductor notify --event progress --message "4/7 handlers"

- An artifact someone should look at, such as a pull request, a preview
  deployment or a report (add --message to say what it is):

      conductor notify --event artifact --url <url>

- A handoff, when the next part of the work belongs to another member of the
  crew:

      conductor notify --event handoff --to <member> --message "…"

- A decision you cannot make yourself, such as a choice between designs, a
  missing credential or an approval: ask, then wait for the answer in the
  terminal. The session shows as needing input until someone replies.

      conductor notify --state needs_input --message "…"

## Rules

- Never call notify outside a Conductor session; the command exits silently there.
- Keep a message to one short line, at most 500 characters. A URL is at most
  2048 bytes, a member name at most 40 characters.
- Do not report every tool call or file edit: Conductor's hooks report those
  when the session asks for them.
`

// skillAsset is where WriteAssets puts the skill under the hooks dir.
const skillAsset = "skills/conductor/SKILL.md"

var skillAssets = map[string]string{skillAsset: Skill}

// Where the agents that read skills find the Conductor skill, under home.
const (
	claudeSkill = ".claude/skills/conductor/SKILL.md"
	codexSkill  = ".codex/skills/conductor/SKILL.md"
	// agentsSkill is in the skills directory agents share; pi and Goose read
	// it.
	agentsSkill = ".agents/skills/conductor/SKILL.md"
)

// skillStep is the step that makes the file rel under home a copy of the
// skill, a file Conductor owns.
func skillStep(hooksDir, rel string) step {
	return copyAsset(skillAssets, hooksDir, skillAsset, rel)
}
