---
name: plan
description: "f10 plan skill - produce a senior-architect implementation plan for a task. Use when the user says \"/f10:plan <task-id | description>\". Fetches or creates the task, investigates the code, writes a staged plan, then stops before implementing."
allowed-tools: Bash, Read, Write, Edit, Grep, Glob, Agent, AskUserQuestion, Skill, ExitPlanMode
argument-hint: "<task-id | free-text description>"
---

# f10 · plan

Take a task from a reference (or a raw description) to a solid, saved plan.
**Do not implement.**

**Dry run:** if the argument contains `--dry-run`, follow
`${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` - load and report, execute nothing.

**Driven:** if the argument contains `--driven`, follow `${CLAUDE_PLUGIN_ROOT}/modes/driven.md`
alongside this skill - no interactive question, every ask through run state.

**Forward first, when the argument is a task id.** A task started with `f10 start` has a
worktree, a workspace and an agent of its own, and its plan lands in that worktree's storage
root; planning it here would plan it on the wrong branch. Before loading anything, one call:

```
f10 forward <task-id> "/f10:plan <the argument verbatim>"
```

- **exit 0** - another checkout holds the task's branch and the command went to its agent.
  Print the block the command printed (task, workspace, sent) in a fenced `yaml` block, add
  nothing, and stop: the outcome lands in that tab. Join `f10-state.sh clear` to the same call
  so this session's badge, seeded when the command was typed, retires - this session has no run.
- **exit 3** - the task is here (its branch is this checkout's, or it has no worktree): the
  message says so on stderr. Continue below.
- anything else is a failure per `${CLAUDE_PLUGIN_ROOT}/conventions/failure.md`: outside Herdr,
  no workspace shows the worktree, the agent is idle and not reporting. Report it and stop.

Without the `f10` binary on `PATH`, read `git worktree list --porcelain` once: a worktree other
than this checkout whose branch is `<id>`, `<id>/...` or `<id>-...` means the task is elsewhere
and this session cannot reach it - fail, pointing at
`go install github.com/amberpixels/f10/cli/cmd/f10@latest`; otherwise continue.

First load the context per `${CLAUDE_PLUGIN_ROOT}/conventions/context.md` - two calls:
`${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh` (skip if this context already holds the bundle),
then `${CLAUDE_PLUGIN_ROOT}/bin/bundle.sh fetch plan`, every run - prepend `capture` when the
argument is free text and the route starts there.

**Route by the argument:**
- **No argument, but a task is already in this conversation** (e.g. the user just fetched a
  task and discussed it): the task and the discussion are your context - the design agreed in
  chat is settled. **Skip `capture` and the `fetch` investigation**; go essentially straight to
  `steps/plan.md`, with at most a few targeted reads of the code you'll mirror or touch -
  never re-investigate from scratch or re-verify givens.
- Looks like a task id (in the project's id format - e.g. `ABC-1234`, `#123` - or an id/url):
  run `steps/fetch.md` → `steps/plan.md`.
- Free-text description: run `steps/capture.md` (create the task) → `steps/fetch.md` →
  `steps/plan.md`.

Follow each step file in order - the bundle already carries them, so read nothing further
from `${CLAUDE_PLUGIN_ROOT}/steps/`. Any extra text the user adds is steering/notes for the run.

**The run is only complete once the plan file exists on disk at
`<storage root>/plans/<TASK-ID>.md`**, the root context resolution reported. Do not end with
the plan only in chat, and do not stop at analysis - in Plan /
read-only mode, use `ExitPlanMode` to get the go-ahead to write it. Then present the plan,
report the saved path per `conventions/report.md`, and - if it has open **gaps** - offer to
fill them now via a questionnaire (see `conventions/gaps.md`; the user can decline and ship on
defaults). Driven, that offer is not made: the gaps stay on their defaults, listed in the report,
and the run continues. Stop before writing implementation code - hand off to `/f10:ship`.
