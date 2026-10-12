---
name: plan
description: "f10 plan skill - produce a senior-architect implementation plan for a task. Use when the user says \"/f10:plan <task-id | description>\". Fetches or creates the task, investigates the code, writes a staged plan, then stops before implementing."
allowed-tools: Bash, Read, Write, Edit, Grep, Glob, Agent, AskUserQuestion, Skill, ExitPlanMode
argument-hint: "<task-id | free-text description>"
---

# f10 · plan

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- `--dry-run`: follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` instead of running anything, and stop.
  It reports which steps the rest of the argument would run.
- `--driven`: follow `${CLAUDE_PLUGIN_ROOT}/modes/driven.md`; everything below still runs.
- A **task id** (in the project's id format, e.g. `ABC-1234`, `#123`, or an id/url): follow `${CLAUDE_PLUGIN_ROOT}/modes/forward.md` first, before loading anything; stop if it forwards the task, otherwise run `steps/fetch.md` and `steps/plan.md`.
- A **free-text description**: run `steps/capture.md` (create the task), `steps/fetch.md`, then `steps/plan.md`.
- **No argument** (an argument that names no task, like "let's plan something", counts as none):
  - **A task already in this conversation** (e.g. the user just fetched and discussed it): the task and the design agreed in chat are settled.
    **Skip `capture` and the `fetch` investigation**; go to `steps/plan.md` with at most a few targeted reads of the code you'll mirror or touch.
  - No task in the conversation: ask for one in one line and wait.
- Extra text beside a task id is steering for the run.

**Run**
- `${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh`, unless an earlier call's output is still in this context word for word; a summary of it does not count.
- `${CLAUDE_PLUGIN_ROOT}/bin/bundle.sh fetch plan`, every run, with `capture` first when the argument is free text.
- Follow each step it prints, in the order the Arguments item gives; read nothing from `steps/`.

**Exit**
- Stop at the saved plan.
- Next: `/f10:ship <id>`.
