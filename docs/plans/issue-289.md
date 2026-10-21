# A plan saved outside the session's folder opens in the editor (#289)

The owner, 2026-10-19: a plan the agent writes should open in the session's
editor, even when it is saved outside the folder the session was launched
in. This is the design as built, in plain terms.

## How Claude Code reports a plan (checked against 2.1.304 and 2.1.306)

- The plan is a Markdown file the agent writes with its Write tool, under
  `plans/` in its config directory: `~/.claude/plans/<slug>.md`, or under
  `CLAUDE_CONFIG_DIR` when that is set. Its `plansDirectory` setting puts
  plans inside the project instead, where the session's folder reaches
  them already. So the PostToolUse hook of that Write names the plan's path
  as any file write.
- The plan's approval (ExitPlanMode) fires a PermissionRequest hook whose
  tool input carries `plan` (the text) and `planFilePath` (the path).
- The approval dialog's last row prints the path: `ctrl+g to edit in
  <editor> · <path>`. A narrow screen wraps it, and Claude Code shortens
  a long path with an ellipsis there.

## What the session does

- **The plans directory** comes from the agent's adapter and the
  environment its process was given, never from a page: for Claude Code,
  `plans` under `CLAUDE_CONFIG_DIR` from the process's environment, else
  under its `HOME` (`USERPROFILE` on Windows), else the user's home as the
  system knows it (`agents.PlansDir`, `session.Options.Plans`). A server
  session computes it from the environment it built for the process; a
  host from the environment it starts the agent with.
- **A second root.** The directory is opened as a second `Folder` (an
  `os.Root`, the same checks as the working directory: links refused once
  resolved, the deny list by identity, a moved directory refused) the
  first time the agent reports a file in it. Nothing else of the config
  directory is opened: the credentials beside `plans/` and every project's
  history stay outside both roots.
- **Only this session's plans.** The files the agent reports in that
  directory are the session's plans: its hooks' file events (a write, an
  edit or a read; a delete forgets it), the `planFilePath` its approval
  names (the attention route's `plan`), and the path its dialog prints,
  once the session sees it to be a file there. A plan is kept by its real
  path, so a link in the directory to a file outside it is kept by where
  it leads, which the directory's check then refuses. Only an admin or the
  holder of the session's agent token can report either, so a viewer
  cannot name a plan into the session.
- **Reads, saves and Neovim** go through `folderFor(path)`: the plans
  folder for one of the session's plans, else the working directory's,
  which refuses anything outside it as before. No request names a root;
  a plan is addressed by its path, absolute or through `~`.
- **The listing** is the file request `op: plans`: kind `plans`, `path`
  the plans directory, `entries` the session's plans that are files there
  now. The Files tab shows them as a Plans group at the top of the
  Explorer.
- **The approval** carries the plan: `Attention.plan` (bounded at 1,024
  bytes), stamped on the question shown after the agent names it and gone
  with the next state. The reply bar offers Open plan. A hosted session's
  plan travels to the host with the forwarded question and back with the
  host's attention.

## What is not done

- The sidebar's and the wall's question rows do not offer Open plan: there
  is no editor there. The session page's bar does.
- A plan written before the session started, or by a session resumed
  later, is not known until the agent touches it again.
