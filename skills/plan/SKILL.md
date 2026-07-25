---
name: plan
description: "f10 plan skill - produce a senior-architect implementation plan for a task. Use when the user says \"/f10:plan <task-id | description>\". Fetches or creates the task, investigates the code, writes a staged plan, then stops before implementing."
allowed-tools: Bash, Read, Write, Edit, Grep, Glob, Agent, AskUserQuestion, Skill, ExitPlanMode
argument-hint: "<task-id | free-text description>"
---

# f10 · plan

Take a task from a reference (or a raw description) to a solid, saved plan. Does **not**
implement.

**Dry run:** if the argument contains `--dry-run`, follow
`${CLAUDE_PLUGIN_ROOT}/steps/dry-run.md` instead of executing - strip the token, resolve the
route, fetch/plan adapters, overlays, and the plan-file path, report them, and change nothing
(no fetch, no plan written).

First load the project context per `${CLAUDE_PLUGIN_ROOT}/steps/context.md` - it defines the
tracker, its task-id format, and any per-step overlays.

**Route by the argument:**
- **No argument, but a task is already in this conversation** (e.g. the user just fetched a
  task and discussed it): the task and the discussion are your context - treat the design
  already agreed in the chat as settled. **Skip `capture` and skip the `fetch` investigation**; go
  essentially straight to `steps/plan.md`. Do at most a few targeted reads of the specific code
  you'll mirror or touch - do not re-investigate the task from scratch or re-verify givens.
- Looks like a task id (in the project's id format - e.g. `ABC-1234`, `#123` - or an id/url):
  run `steps/fetch.md` → `steps/plan.md`.
- Free-text description: run `steps/capture.md` (create the task) → `steps/fetch.md` →
  `steps/plan.md`.

Follow each step file in order - all live under `${CLAUDE_PLUGIN_ROOT}/steps/`. Any extra text
the user adds is steering/notes for the run.

**The run is only complete once the plan file exists on disk at `.f10/plans/<TASK-ID>.md`** (the
plan step's deliverable). Do not end with the plan only in chat, and do not stop at analysis. If
the session is in Plan / read-only mode, use `ExitPlanMode` to get the go-ahead to write it rather
than degrading to an analysis-only run. Then present the plan, report the saved path, and - if it
has open **gaps** - offer to fill them now via a questionnaire (see `gaps.md`; the user can decline
and ship on defaults). Stop before writing implementation code - hand off to `/f10:ship`.
