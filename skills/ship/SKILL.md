---
name: ship
description: "f10 ship skill (full run) - take a task through the project's ship pipeline: implement, then whatever the project declares (review, PR/MR, deploy). Use when the user says \"/f10:ship <task-id | plan-file.md | description>\". Fetches and plans as needed, then runs the ship pipeline end to end."
allowed-tools: Bash, Read, Write, Edit, Grep, Glob, Agent, AskUserQuestion, Skill, ScheduleWakeup, ExitPlanMode
argument-hint: "<task-id | path/to/plan.md | free-text description>"
---

# f10 · ship

Drive a task end-to-end: plan (if needed) → the project's **ship pipeline** (declared in
`project.md`; default `implement → pr` - see `conventions/context.md`).

**Dry run:** if the argument contains `--dry-run`, follow
`${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` - resolve and report, execute nothing.

First load the context per `${CLAUDE_PLUGIN_ROOT}/conventions/context.md` - two calls:
`${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh` (skip if this context already holds the bundle),
then `${CLAUDE_PLUGIN_ROOT}/bin/resolve.sh --all`, every run. **`--all`, never a step list**:
ship's steps are the pipeline that same call prints, so they cannot be named beforehand
(`conventions/context.md`).

**Route by the argument:**
- A **plan file** (`*.md` path): skip planning - run the ship pipeline with that plan.
- A **task id** (project id format / id / url): run `steps/fetch.md`, then look for the saved
  plan at **`<storage root>/plans/<TASK-ID>.md`** - the exact path `/f10:plan` writes, under
  the root context resolution reported. Glob the plans dir if the exact name misses:
  - exists → ask the user **reuse this plan or re-plan?** (AskUserQuestion). Reuse → straight
    to the pipeline; re-plan → `steps/plan.md` first.
  - missing → run `steps/plan.md`.
  Then run the ship pipeline.
- A **free-text description**: full run - `steps/capture.md` → `steps/fetch.md` →
  `steps/plan.md` → the ship pipeline.

**Pipeline selection:** if the project declares several named pipelines, run `default` unless
the user named another (as a word in the argument - e.g. `/f10:ship direct: fix typo …` - or
in their own words). You may suggest a better-fitting pipeline for the task's size, but never
switch without the user's pick. A **(planless)** pipeline skips capture/fetch/plan for
free-text input (`conventions/context.md`).

Run the pipeline's steps **in the declared order** - generic ones live under
`${CLAUDE_PLUGIN_ROOT}/steps/`, project-defined ones are `<name>.md` in the instructions dir
context resolution reported. Any extra text the user adds is steering/notes for the run.
For a plan you just wrote this run, proceed without re-confirming; open gaps are surfaced by
the implement step (`conventions/gaps.md`).

**Badge** (`conventions/report.md`): once the pipeline is selected, declare its last step -
`f10-state.sh final <step>` - so a turn ending mid-pipeline keeps its spinning glyph. Then
`f10-state.sh set ship running <step>` before each pipeline step, named as the pipeline names
it, and `f10-state.sh set ship done` after the last. Where the plan came from an earlier run -
the plan-file route, or the reuse branch
above - add `f10-state.sh set plan prior`. Where the pipeline stops mid-way but durable
artifacts exist - commits, an open PR, a deploy - report `set ship partial <step>` rather
than `failed`, per `conventions/failure.md` rule 7.

**Final report:** lead with the facts block per `conventions/report.md` - the PR/MR url, its
branch, and the deploy url where the pipeline deployed. Then, as prose: review status as far as
the pipeline goes ("stopped at PR" is a normal end), deploy status if it deploys, a short note
of what changed, and - if any gaps stayed on their defaults - a one-line reminder that they're
recorded in the plan.

Note: running this skill is the user's explicit go-ahead to push and open the PR
(`steps/pr.md`); deploy steps carry their own confirmation rules (`steps/deploy.md`).
