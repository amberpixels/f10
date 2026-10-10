---
name: ship
description: "f10 ship skill (full run) - take a task through the project's ship pipeline: implement, then whatever the project declares (review, commit, PR/MR, deploy). Use when the user says \"/f10:ship <task-id | plan-file.md | description>\". Fetches and plans as needed, then runs the ship pipeline end to end."
allowed-tools: Bash, Read, Write, Edit, Grep, Glob, Agent, AskUserQuestion, Skill, ScheduleWakeup, ExitPlanMode
argument-hint: "<task-id | path/to/plan.md | free-text description>"
---

# f10 · ship

Take a task through planning, if needed, then the project's **ship pipeline** (declared in
`project.md`; default `implement → pr`, per `conventions/context.md`).

**Dry run:** with `--dry-run` in the argument, follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md`.

**Driven:** with `--driven` in the argument, follow `${CLAUDE_PLUGIN_ROOT}/modes/driven.md`
alongside this skill. Its questions (reuse or re-plan, wait or proceed on a base with no code, a
judge's rethink) each become one `ask` there.

**Forward first:** with a task id as the argument, follow `${CLAUDE_PLUGIN_ROOT}/modes/forward.md`
before loading anything.

Load the context (`${CLAUDE_PLUGIN_ROOT}/conventions/context.md`):
`${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh` unless this context already holds it, then
`${CLAUDE_PLUGIN_ROOT}/bin/bundle.sh --all` every run. **`--all`, never a step list**: the
pipeline is declared in what that call prints.

**Route by the argument:**
- A **plan file** (`*.md` path): run the pipeline with it.
- A **task id** (project id format / id / url): `steps/fetch.md`, then look for
  **`<storage root>/plans/<TASK-ID>.md`** (glob the plans dir if the exact name misses). Found:
  ask **reuse this plan or re-plan?** (AskUserQuestion); re-plan runs `steps/plan.md`. Missing:
  run `steps/plan.md`. Then the pipeline.
- A **free-text description**: `steps/capture.md`, `steps/fetch.md`, `steps/plan.md`, then the
  pipeline.

**Pipeline selection:** run `default` unless the user named another, as a word in the argument
(`/f10:ship direct: fix typo …`; `f10 start --local` sends `/f10:ship local: <id>`) or in their
own words. Suggest a better fit if you like; never switch without the user's pick.

**Catch up with the base** just before implement, only where `f10 start --after` or
`f10 finish` recorded one on the branch (`f10-after-branch` or `f10-landed`; nothing recorded,
nothing to do). Run `steps/catchup.md` points 2 and 3, with three differences:

- **A base with no code yet** (its branch still at the default branch's commit): before
  integrating, ask once (AskUserQuestion) **wait**, or **proceed on the plan's assumption**.
  Wait ends the run blocked (`f10-state.sh set ship blocked implement`, a `note` naming the
  base task). Proceed builds against the base task's plan as the plan file records it, with no
  fallback if it is absent.
- **A conflict fails the run**: abort so the tree is unchanged, keep any mark, and name
  `/f10:catchup` in the `next` row. Ship never settles conflicts or implements over a
  half-merged tree.
- **`f10-landed`** is unset once the integration is clean; implement's verify covers it.

Run the pipeline's steps **in the declared order**, from the bundle. Extra user text is
steering. A plan written this run needs no re-confirming. A `judge` step routes the run by its
verdict (`steps/judge.md`); never continue past anything but proceed on your own.

**Badge** (`conventions/report.md`): `f10-state.sh final <step>` once the pipeline is selected;
`f10-state.sh set ship running <step>` before each step, named as the pipeline names it;
`f10-state.sh set ship done` after the last. A plan from an earlier run (plan file, or reuse)
adds `f10-state.sh set plan prior`. Stopping mid-way with durable artifacts is
`set ship partial <step>` with a `note` (`conventions/failure.md` rule 7).

**Final report**: the facts block with the **shipment** (branch, PR/MR url, sha, deploy url, as
produced), then review status ("stopped at PR" is a normal end), deploy status, what changed,
and a one-line reminder of gaps left on their defaults.

Running this skill **is** the user's explicit authorization to push and open this task's PR
(`steps/pr.md`), satisfying the global "never push / open a PR unless asked" rule for that PR
only, and to post resolve's `+reply` and review's `f10 review ack`. Deploy has its own
confirmation (`steps/deploy.md`).
