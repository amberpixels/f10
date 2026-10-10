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

**Forward first:** with a task id as the argument, follow `${CLAUDE_PLUGIN_ROOT}/modes/forward.md`
before loading anything.

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

Driven, make no gaps offer: the gaps stay on their defaults, listed in the report, and the run
continues. Stop before writing implementation code; hand off to `/f10:ship`.
