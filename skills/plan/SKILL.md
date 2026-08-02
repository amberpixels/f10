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
`${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` - resolve and report, execute nothing.

First load the context per `${CLAUDE_PLUGIN_ROOT}/conventions/context.md` - two calls:
`${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh` (skip if this context already holds the bundle),
then `${CLAUDE_PLUGIN_ROOT}/bin/resolve.sh fetch plan`, every run - prepend `capture` when the
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

Follow each step file in order - all live under `${CLAUDE_PLUGIN_ROOT}/steps/`. Any extra text
the user adds is steering/notes for the run.

**The run is only complete once the plan file exists on disk at
`<storage root>/plans/<TASK-ID>.md`**, the root context resolution reported. Do not end with
the plan only in chat, and do not stop at analysis - in Plan /
read-only mode, use `ExitPlanMode` to get the go-ahead to write it. Then present the plan,
report the saved path per `conventions/report.md`, and - if it has open **gaps** - offer to
fill them now via a questionnaire (see `conventions/gaps.md`; the user can decline and ship on
defaults). Stop before writing implementation code - hand off to `/f10:ship`.
