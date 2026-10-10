# The sidebar, watched in use: five tasks

A script for the owner to run with two coworkers, one at a time, at the
owner's desktop app: each person does five everyday tasks while the owner
watches and does not help. The sidebar was designed in round 11 (screens
3a–3f) without anyone new to it trying it; this finds what the design
missed. About 20 minutes per person.

## Before each session

Set the app up so every task has something to find. Real agents, nothing
staged in the page:

1. **A crew with a run from yesterday.** On the Crews page, a crew of three
   (for example `users api`: core, tests, review). Launch it the day before
   and stop it once it has done something, so its record is there today.
2. **A run of the same crew, live, with one member asking.** Launch it again
   today with yolo off, and give one member work that needs a permission
   (Claude Code: "run the tests"), so it stops at "Allow Bash?" and sits in
   **Needs you**. The others keep running.
3. **Two loose sessions,** launched on their own: one running, one exited
   less than 10 minutes before the session (exited sessions leave the list
   after `exitedRetention`, 10 minutes).
4. Sidebar at its defaults: full width (not the rail), Exited folded,
   nothing filtered.

Between the two people, put things back: launch the run again and let the
same member reach its permission question, revoke the link the first
person made, clear the filter.

## What to say first

"This is Conductor: it runs coding agents and shows each one's terminal.
I'll ask you to do five small things. There are no wrong answers; we are
testing the screen, not you. Please think aloud: say what you're looking
for and what you expect to happen. I won't help unless you're stuck for a
couple of minutes."

## The tasks

Read each one out as written, in this order: a stopped session offers no
Share, so sharing comes before stopping. Don't name a section, a button or
a word on the screen.

| # | Read out | Done when | The path the design intends |
|---|---|---|---|
| 1 | "One of the agents is waiting for you to answer something. Find it and answer it." | The prompt is answered and the member moves on. | **Needs you** at the top, the run's block, the member asking first; the numbered choice in the row (1 Yes), or open it. |
| 2 | "A coworker wants to watch just the tests agent, not the whole team. Get them a link." | A link for that one member is copied. | Hover the member's row → **Share** (or right-click → Share…); not the run header's Share run. |
| 3 | "Stop everything the `users api` team is doing right now." | The live run is stopped, every member's terminal closed. | Hover the run's header in the sidebar → **Stop run**, then confirm in the row; or open the run → Stop. |
| 4 | "Yesterday the same team ran. Open what it did yesterday." | Yesterday's run is open (its members, its output or diffs). | Not in the sidebar (exited sessions leave it after 10 minutes): Crews → `users api` → **Runs** → yesterday's row. Known: **Open** on a run kept only as a record answers 404 today (docs/tasks-todo.md, Bugs); note where they looked and stop the task there. |
| 5 | "What's the difference between `users api` on the Crews page and `users api` in the list on the left?" | They say, in their words, that one is the saved team (the plan) and the other is one time it ran. | The crew lives on the Crews page; the sidebar lists runs, the crew's name with the start time ("started 08:31 · 3 agents"). |

## What to note for each task

- Where they looked first, and second.
- How long it took, and whether they finished on their own, with a hint, or
  not at all.
- Every wrong click (a row instead of its button, Share run for one member,
  the rail, the Yard), and what they expected it to do.
- Words they used for things ("team", "job", "session", "task") against the
  screen's (crew, run, session, agent).
- Anything they said aloud about colour, order or folding (the amber Needs
  you, Exited folded, the line joining a run's members).

## After the five

Ask:

- "What was the hardest of the five? Why?"
- "What do you think the amber means? The grey?"
- "If you could change one thing in the list on the left, what would it be?"

## Afterwards

Each finding goes in `docs/tasks-todo.md` (the step that went wrong, what
the person expected, how many of the two hit it); what held up goes in
`docs/features.md` under the round that runs it.
