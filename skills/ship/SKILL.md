---
name: ship
description: "f10 ship skill (full run) - take a task through the project's ship pipeline: implement, then whatever the project declares (review, commit, PR/MR, deploy). Use when the user says \"/f10:ship <task-id | plan-file.md | description>\". Fetches and plans as needed, then runs the ship pipeline end to end."
allowed-tools: Bash, Read, Write, Edit, Grep, Glob, Agent, AskUserQuestion, Skill, ScheduleWakeup, ExitPlanMode
argument-hint: "<task-id | plan.md | description>"
---

# f10 · ship

Running this skill **is** the user's explicit authorization to push and open or refresh this task's PR (the **pr** step), satisfying the global "never push / open a PR unless asked" rule for that PR only, and to post resolve's `+reply`, push resolve's fixes to that PR, post review's `f10 review ack`, and, for a judge entry after `pr`, push its changes to that PR and post its status and comment.
Deploy has its own confirmation (the **deploy** step).

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- `--dry-run`: follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` instead of running anything, and stop.
  It reports which steps the rest of the argument would run.
- `--driven`: follow `${CLAUDE_PLUGIN_ROOT}/modes/driven.md`; everything below still runs.
- A pipeline name as a word (`/f10:ship direct: fix typo …`; `f10 start --local` sends `/f10:ship local: <id>`), or named in the user's own words: run that pipeline.
  Otherwise `default`; never self-select another, though for tiny work you may suggest one.
- A **plan file** (`*.md` path): run the pipeline with it.
- A **task id** (project id format / id / url), or a **bundle** joined by `+` (`ABC-1+ABC-2`, keyed on the lead): follow `${CLAUDE_PLUGIN_ROOT}/modes/forward.md` first, before loading anything; stop if it forwards the task.
  Otherwise run the **fetch** step, then look for **`<storage root>/plans/<TASK-ID>.md`** (a bundle's lead) (glob the plans dir if the exact name misses).
  Found: ask **reuse this plan or re-plan?** (AskUserQuestion); re-plan runs the **plan** step.
  Missing: run the **plan** step.
  Then the pipeline.
- A **free-text description**: the **capture**, **fetch** and **plan** steps, then the pipeline.
- Extra text beside the subject is steering.

**Run**
1. Run `f10-load-project.sh` first, on every call of this skill, naming the steps the input needs: `capture fetch plan` for a description, `fetch plan` for a task id, none for a plan file.
   The pipeline is declared in the `project.md` it prints.
2. Follow those steps in the order the Arguments item gives.
3. Load each pipeline step just before it runs, in one call with its badge: `f10-state.sh set ship running <step>; f10-load-project.sh <step>`, and follow the step from that load.
   A `review` entry loads `review resolve`; a `judge` entry after a `pr` entry loads `judge implement commit`, for the yellow route; with a recorded base (below), implement's load is `catchup implement`.

**Pipelines** are the frontmatter's `pipelines:`, a name to its ordered steps, a list or one `a → b` string (`implement → review (local) → pr → review (CI) → deploy (staging)`); none declared is `default: implement → pr`.
Each name is a generic step plus its overlay, or a project-defined one.
`resolve` is never named: a `review` entry runs it.
A `pr` entry opens the branch's PR, or refreshes it when one is open, so a pipeline may name `pr` again after review or judge to bring the description up to date.
A pipeline listed in `planless:` skips capture, fetch and plan for free-text input: no task id, no plan file, a brief inline plan in chat.
The last step is the **shipment**.

**A spike ends after implement**, whatever the pipeline: a `Kind: spike` plan answers a question, and its code never leaves the machine.
No step after implement runs (no cleanup, commit, push, pr, review, judge or deploy); the shipment is the spike report.
`f10 finish` cannot close it (it needs a PR and a clean worktree, and archives only the plan), so the final report ends with the manual close.
In a linked worktree with in-repo storage: copy the spike report and the plan into the main checkout's storage root (`spikes/`, `plans/archive/`), then, from the main checkout, `git worktree remove --force <path>`, `git branch -D <branch>`, and close its Herdr workspace.
In the main checkout, or with out-of-tree storage, the report already outlives the code: discard the experiment code by the paths the report names.
It also offers, in one line, to post the report's answer and recommendation as a comment on the task, outward-facing and free of f10 traces; a driven run lists the offer and posts nothing.
The kind is known from the plan before the pipeline starts, so the badge declares `f10-state.sh final implement`, and a dry run prints the path cut at implement.

**Catch up with the base** just before implement, only where `f10 start --after` or `f10 finish` recorded one on the branch (`f10-after-branch` or `f10-landed`; nothing recorded, nothing to do).
Run the **catchup** step's points 2 and 3, with three differences:

- **A base with no code yet** (its branch still at the default branch's commit): before integrating, ask once (AskUserQuestion) **wait**, or **proceed on the plan's assumption**.
  Wait ends the run blocked (`f10-state.sh set ship blocked implement`, a `note` naming the base task).
  Proceed builds against the base task's plan as the plan file records it, with no fallback if it is absent.
- **A conflict fails the run**: abort so the tree is unchanged, keep any mark, and name `/f10:catchup` in the `next` row.
- **`f10-landed`** is unset once the integration is clean; implement's verify covers it.

Run the pipeline's steps **in the declared order**, each from its own load.
A plan written this run needs no re-confirming.
A `judge` step routes the run by its verdict, differently before and after a `pr` entry (the **judge** step).
After `pr`, the changes folded into the plan are built by the judge step's own route, not by a rerun of `implement`.

**Badge** (`conventions/report.md`): `f10-state.sh final <step>` once the pipeline is selected; `f10-state.sh set ship running <step>` before each step, named as the pipeline names it, in the call that loads it; `f10-state.sh set ship done` after the last.
A plan from an earlier run (plan file, or reuse) adds `f10-state.sh set plan prior`.
Stopping mid-way with durable artifacts is `set ship partial <step>` with a `note` (`conventions/failure.md` rule 7).

**Exit**
- Stop after the pipeline's last step (a spike's: implement), with the final report: the facts block with the **shipment** (branch, PR/MR url, sha, deploy url, a spike report's path, as produced), then review status ("stopped at PR" is a normal end), deploy status, a bug's or refactor's proof or a spike's answer (implement's line) and its manual close, what changed, and a one-line reminder of gaps left on their defaults.
