---
name: ship
description: "f10 pipeline step 3 (full run) — take a task through the project's ship pipeline: implement, then whatever the project declares (review, PR/MR, deploy). Use when the user says \"/f10:ship <task-id | plan-file.md | description>\". Fetches and plans as needed, then runs the ship pipeline end to end."
allowed-tools: Bash, Read, Write, Edit, Grep, Glob, Agent, AskUserQuestion, Skill, ScheduleWakeup, ExitPlanMode
argument-hint: "<task-id | path/to/plan.md | free-text description>"
---

# f10 · ship

Drive a task end-to-end: plan (if needed) → the project's **ship pipeline** (declared in
`project.md`; default `implement → pr` — see `steps/context.md`).

**Dry run:** if the argument contains `--dry-run`, follow
`${CLAUDE_PLUGIN_ROOT}/steps/dry-run.md` instead of executing — strip the token, resolve the
route, the selected ship pipeline and each step's adapter/overlay, report them, and change
nothing (no implement, push, PR, or deploy).

First load the project context per `${CLAUDE_PLUGIN_ROOT}/steps/context.md` — it defines the
tracker, its task-id format, the ship pipeline, and any per-step overlays.

**Route by the argument:**
- A **plan file** (`*.md` path): skip planning — run the ship pipeline with that plan.
- A **task id** (project id format / id / url): run `steps/fetch.md`, then look for the saved
  plan at **`<storage root>/plans/<TASK-ID>.md`** — the exact path `/f10:plan` writes; the
  storage root is `.f10/` unless context resolved out-of-tree storage (`~/.f10/<project>/`,
  see `context.md → Storage`). Glob the plans dir if the exact name misses:
  - exists → ask the user **reuse this plan or re-plan?** (AskUserQuestion). Reuse → straight
    to the pipeline; re-plan → `steps/plan.md` first.
  - missing → run `steps/plan.md`.
  Then run the ship pipeline.
- A **free-text description**: full run — `steps/capture.md` → `steps/fetch.md` →
  `steps/plan.md` → the ship pipeline.

**Pipeline selection:** if the project declares several named pipelines, run `default` unless
the user named another (as a word in the argument — e.g. `/f10:ship direct: fix typo …` — or
in their own words). You may suggest a better-fitting pipeline for the task's size, but never
switch without the user's pick. A **(planless)** pipeline skips capture/fetch/plan for
free-text input (see `context.md`).

Run the pipeline's steps **in the declared order** — generic ones live under
`${CLAUDE_PLUGIN_ROOT}/steps/` (`implement`, `pr`, `push`, `review`, `deploy`), project-defined
ones are `.f10/instructions/<name>.md`. Any extra text the user adds is steering/notes for the
run.
For a task whose plan you just wrote this run, proceed without re-confirming; open gaps are
surfaced by the implement step (see `gaps.md`).

**Final report:** the PR/MR url, review status as far as the pipeline goes ("stopped at PR"
is a normal end), deploy status if the pipeline deploys, a short note of what changed, and —
if any gaps were left on their defaults — a one-line reminder that they're recorded in the
plan and can be revisited.

Note: running this skill is the user's explicit go-ahead to push and open the PR
(`steps/pr.md`); deploy steps carry their own confirmation rules (`steps/deploy.md`).
