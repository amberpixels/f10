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
- A **task id** (in the project's id format, e.g. `ABC-1234`, `#123`, or an id/url), or a **bundle** of them joined by `+` (`ABC-1+ABC-2`, one plan in the lead's file): follow `${CLAUDE_PLUGIN_ROOT}/modes/forward.md` first, before loading anything; stop if it forwards the task, otherwise run the **fetch** and **plan** steps.
  Under `stale_after`, fetch judges an old task first and may end the run blocked before any plan; `anyway` in the steering skips that judge.
- A **free-text description**: run the **capture** step (create the task), then **fetch** and **plan**.
- **No argument** (an argument that names no task, like "let's plan something", counts as none):
  - **A task already in this conversation** (e.g. the user just fetched and discussed it): the task and the design agreed in chat are settled.
    **Skip `capture` and the `fetch` investigation**; go to the **plan** step with at most a few targeted reads of the code you'll mirror or touch.
    Fetch's kind rules still run: settle the kind, and reproduce a bug or ask.
  - No task in the conversation: ask for one in one line and wait.
- Extra text beside a task id is steering for the run, except a `&& /f10:<skill> ...` tail: that is a chain, run once the plan is saved (`conventions/report.md`).

**Run**
1. Run `f10-load-project.sh fetch plan` first, on every call of this skill; with a free-text argument, `f10-load-project.sh capture fetch plan`.
2. Follow each step it prints, in the order the Arguments item gives.

**Exit**
- Stop at the saved plan, unless the argument chains a skill: then run it.
- Next: `/f10:ship <id>`.
