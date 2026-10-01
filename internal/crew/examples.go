package crew

import "time"

// Examples returns the example crews: data like any saved crew, which
// conductor serve --examples and POST /api/crews/examples seed once
// (Store.Seed) and which are edited or deleted like any other. Their members
// use the claude and codex built-ins; every crew works in cwd, the server's
// default working directory, with a worktree per member, and opens its view
// after launch. now stamps them.
//
// A member's worktree starts at the checkout's HEAD, so what an earlier
// member committed is on its branch only, crew/<run>/<member>: each member
// that starts after another begins by merging that member's branch, naming
// the run as $CONDUCTOR_RUN, which the launch puts in the member's
// environment and the agent's shell expands (ExpandPrompt expands only
// $GOAL). The notify command goes through $CONDUCTOR_BIN, as the Conductor
// skill's does, for an agent whose PATH has no conductor.
func Examples(cwd string, now time.Time) []Crew {
	crew := func(id, name, goal string, members ...Member) Crew {
		return Crew{ID: id, Name: name, Goal: goal, Cwd: cwd, Where: WhereServer, Isolation: IsolationWorktree, OpenAfterLaunch: true, Members: members, CreatedAt: now, UpdatedAt: now}
	}
	member := func(name, agent, prompt string, start Start) Member {
		return Member{Name: name, AgentID: agent, Prompt: prompt, Start: start}
	}
	first := Start{When: StartImmediately}
	after := func(m string) Start { return Start{When: StartAfter, Member: m} }
	return []Crew{
		crew("example-todo-app", "Example: todo app",
			"Build a small command-line todo app in this repository (meant for a fresh repository with one commit: point the crew's working directory at one before launching, not at a real project): add, list and done commands, items kept in a JSON file in the user's home directory, with tests.",
			member("lead", "claude", "You lead a crew of four on this goal: $GOAL. Plan, do not build. Write PLAN.md at the top of the repository with the data model, the command-line interface and the file layout, then split the work into two parts, core (storage and the data model) and cli (the commands), naming the files each part owns and the interface between them. Commit PLAN.md on your branch and stop: the builders merge your branch and start when you report done.", first),
			member("core", "claude", "Start by merging the lead's branch, which holds PLAN.md: git merge --no-edit crew/$CONDUCTOR_RUN/lead (your run is in $CONDUCTOR_RUN). Then build the core part of PLAN.md for this goal: $GOAL. Work only in the files PLAN.md gives to core, with unit tests, and commit as you go. Report done when the storage and the data model are complete and their tests pass.", after("lead")),
			member("cli", "codex", "Start by merging the lead's branch, which holds PLAN.md: git merge --no-edit crew/$CONDUCTOR_RUN/lead (your run is in $CONDUCTOR_RUN). Then build the cli part of PLAN.md for this goal: $GOAL. Work only in the files PLAN.md gives to cli, against the interface PLAN.md says core exposes, and commit as you go. Report done when every command works end to end.", after("lead")),
			// A start condition names one member: the tester starts after cli
			// and waits for core's branch itself.
			member("tester", "codex", "Start by merging the lead's and cli's branches: git merge --no-edit crew/$CONDUCTOR_RUN/lead, then git merge --no-edit crew/$CONDUCTOR_RUN/cli (your run is in $CONDUCTOR_RUN). You test the todo app built for this goal: $GOAL. You start once cli reports done; core may still be at work: wait until crew/$CONDUCTOR_RUN/core holds the core part PLAN.md describes, then git merge --no-edit crew/$CONDUCTOR_RUN/core, run the whole test suite, add the tests PLAN.md asks for that are missing, and change test code only. Hand a bug in the app to its owner with: \"${CONDUCTOR_BIN:-conductor}\" notify --event handoff --to core --message \"what and where\" (or --to cli). Report done when the suite is green.", after("cli")),
		),
		crew("example-test-fixer", "Example: test fixer",
			"Make this repository's test suite pass without weakening a test.",
			member("triage", "claude", "Run this repository's test suite for this goal: $GOAL. Write TRIAGE.md at the top of the repository: every failing test, its cause as far as you can tell, grouped by the production file to change. Commit it on your branch and report done. Fix nothing yourself.", first),
			member("fixer", "codex", "Start by merging the triage branch, which holds TRIAGE.md: git merge --no-edit crew/$CONDUCTOR_RUN/triage (your run is in $CONDUCTOR_RUN). Then fix the failures TRIAGE.md lists, for this goal: $GOAL. Change production code before tests; never delete, skip or loosen a test. Commit once per group in TRIAGE.md and report done when the whole suite passes.", after("triage")),
		),
		crew("example-docs-writer", "Example: docs writer",
			"Write a README for this repository that a new contributor can build, run and configure from.",
			member("reader", "claude", "Read this repository for this goal: $GOAL: the build files, the entry points, the configuration and the tests. Write NOTES.md at the top of the repository with what the project does, how it is built and run, and how it is configured, every fact with the file it comes from. Commit it on your branch and report done.", first),
			member("writer", "codex", "Start by merging the reader's branch, which holds NOTES.md: git merge --no-edit crew/$CONDUCTOR_RUN/reader (your run is in $CONDUCTOR_RUN). Then write README.md from NOTES.md, for this goal: $GOAL: what it is, a quick start, configuration, development. Keep every command exactly as it is run; mark anything NOTES.md does not establish as an open question rather than guessing. Commit it and report done.", after("reader")),
		),
		crew("example-dependency-upgrade", "Example: dependency upgrade",
			"Upgrade this repository's direct dependencies to their latest compatible versions, one at a time, with the test suite passing after each.",
			member("scout", "codex", "Survey this repository's direct dependencies for this goal: $GOAL. Write UPGRADES.md at the top of the repository: each dependency with its current and latest versions, the ones furthest behind first, and a note on any whose release notes announce a breaking change. Commit it on your branch and report done. Upgrade nothing yourself.", first),
			member("upgrader", "claude", "Start by merging the scout's branch, which holds UPGRADES.md: git merge --no-edit crew/$CONDUCTOR_RUN/scout (your run is in $CONDUCTOR_RUN). Then upgrade the dependencies UPGRADES.md lists, for this goal: $GOAL, one dependency per commit, running the test suite after each and fixing what the upgrade breaks. Skip, and note in UPGRADES.md, any that cannot be made to pass. Report done with the list of what moved.", after("scout")),
		),
	}
}
