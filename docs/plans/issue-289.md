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
  folder for a path that leads into the plans directory, else the working
  directory's, which refuses anything outside it as before. The plans
  folder allows the file a request resolves to only when it is one of the
  session's plans (`Folder.allow`, asked of the resolved path), and then
  opens that file element by element with no link followed, so a link
  retargeted between the check and the open reaches nothing that was not
  allowed (Codex's first-round finding). No request names a root; a plan
  is addressed by its path, absolute or through `~`.
- **The plan on the question** travels inside the attention change that
  names it (`SetAttentionPlan`, the attention route's `plan`, the host's
  forwarded message), in the one step that change takes: learnt once the
  change is past the session's bucket, set on the question the change
  shows, or on the dialog of that approval already showing (told to every
  client); a refused or rate-limited report names nothing, and nothing is
  staged for a later question. The dialog read off the screen that takes
  the place of the hook's report keeps its plan; the dialog's own footer
  rides the dialog's change; the dialog that follows an answered approval
  takes the place of a dialog and carries none. A host tells its server
  the plan set on a question it already reported (its change detector
  compares it).
- **Aliases of the plans directory** (a linked `~/.claude/plans`, macOS's
  `/var` for `/private/var`) are matched by their resolved paths, for
  learning and forgetting alike, and a home with no `HOME` set falls back
  to the account database, as Claude Code's own `os.homedir()` does.
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
