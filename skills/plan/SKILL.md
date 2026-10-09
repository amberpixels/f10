---
name: plan
description: "f10 plan skill - produce a senior-architect implementation plan for a task. Use when the user says \"/f10:plan <task-id | description>\". Fetches or creates the task, investigates the code, writes a staged plan, then stops before implementing."
allowed-tools: Bash, Read, Write, Edit, Grep, Glob, Agent, AskUserQuestion, Skill, ExitPlanMode
argument-hint: "<task-id | free-text description>"
---

# f10 · plan

Take a task from a reference (or a raw description) to a saved plan. **Do not implement.**

**Dry run:** with `--dry-run` in the argument, follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md`.

**Driven:** with `--driven` in the argument, follow `${CLAUDE_PLUGIN_ROOT}/modes/driven.md`
alongside this skill.

**Forward first, when the argument is a task id.** A task started with `f10 start` has its own
worktree, workspace and agent, and its plan lands in that worktree's storage root; planning it
here would use the wrong branch. Before loading anything, one call:

```
f10 forward <task-id> "/f10:plan <the argument verbatim>"
```

- **exit 0** - another checkout holds the task's branch and the command went to its agent.
  Print the command's block (task, workspace, sent) in a fenced `yaml` block, add nothing, and
  stop. Join `f10-state.sh clear` to the same call to retire the badge seeded when the command
  was typed: this session has no run.
- **exit 3** - the task is here (its branch is this checkout's, or it has no worktree), as
  stderr says. Continue below.
- anything else is a failure per `${CLAUDE_PLUGIN_ROOT}/conventions/failure.md` (outside Herdr,
  no workspace shows the worktree, the agent is idle and not reporting). Report it and stop.

Without the `f10` binary on `PATH`, read `git worktree list --porcelain` once. A worktree other
than this checkout whose branch is `<id>`, `<id>/...` or `<id>-...` holds the task out of reach:
fail, pointing at `go install github.com/amberpixels/f10/cli/cmd/f10@latest`. Otherwise continue.

Load the context (`${CLAUDE_PLUGIN_ROOT}/conventions/context.md`):
`${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh` unless this context already holds it, then
`${CLAUDE_PLUGIN_ROOT}/bin/bundle.sh fetch plan` every run, with `capture` prepended when the
argument is free text.

**Route by the argument:**
- **No argument, but a task is already in this conversation** (e.g. the user just fetched and
  discussed it): the task and the design agreed in chat are settled. **Skip `capture` and the
  `fetch` investigation**; go to `steps/plan.md` with at most a few targeted reads of the code
  you'll mirror or touch. Never re-investigate from scratch or re-verify givens.
- A task id (in the project's id format, e.g. `ABC-1234`, `#123`, or an id/url): run
  `steps/fetch.md`, then `steps/plan.md`.
- Free-text description: run `steps/capture.md` (create the task), `steps/fetch.md`, then
  `steps/plan.md`.

Follow each step file in order from the bundle; read nothing from `${CLAUDE_PLUGIN_ROOT}/steps/`.
Extra text from the user is steering for the run.

The run completes only with the plan file on disk (`steps/plan.md` point 3). Then present the
plan, report the saved path per `conventions/report.md`, and offer to fill any open **gaps**
(`conventions/gaps.md`); the user can decline and ship on defaults. Driven, make no offer: the
gaps stay on their defaults, listed in the report, and the run continues. Stop before writing
implementation code; hand off to `/f10:ship`.
