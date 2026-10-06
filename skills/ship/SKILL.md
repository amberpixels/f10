---
name: ship
description: "f10 ship skill (full run) - take a task through the project's ship pipeline: implement, then whatever the project declares (review, commit, PR/MR, deploy). Use when the user says \"/f10:ship <task-id | plan-file.md | description>\". Fetches and plans as needed, then runs the ship pipeline end to end."
allowed-tools: Bash, Read, Write, Edit, Grep, Glob, Agent, AskUserQuestion, Skill, ScheduleWakeup, ExitPlanMode
argument-hint: "<task-id | path/to/plan.md | free-text description>"
---

# f10 · ship

Drive a task end-to-end: plan (if needed) → the project's **ship pipeline** (declared in
`project.md`; default `implement → pr` - see `conventions/context.md`).

**Dry run:** if the argument contains `--dry-run`, follow
`${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` - load and report, execute nothing.

**Driven:** if the argument contains `--driven`, follow `${CLAUDE_PLUGIN_ROOT}/modes/driven.md`
alongside this skill - no interactive question, every ask through run state. The questions this
skill asks - reuse or re-plan, wait or proceed on a base with no code, a judge's rethink - each
become one `ask` there.

**Forward first, when the argument is a task id.** A task started with `f10 start` has a
worktree, a workspace and an agent of its own, and its plan sits in that worktree's storage
root; shipping it here would ship it on the wrong branch, without the plan. Before loading
anything, one call:

```
f10 forward <task-id> "/f10:ship <the argument verbatim>"
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
then `${CLAUDE_PLUGIN_ROOT}/bin/bundle.sh --all`, every run. **`--all`, never a step list**:
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
the user named another (as a word in the argument - e.g. `/f10:ship direct: fix typo …`, which
is also how `f10 start --local` sends `/f10:ship local: <id>` - or in their own words). You may suggest a better-fitting pipeline for the task's size, but never
switch without the user's pick. A **(planless)** pipeline skips capture/fetch/plan for
free-text input (`conventions/context.md`).

**Dependency:** before the first pipeline step, read what `f10 start --after` recorded on the
branch:

```
git config --get branch."$(git branch --show-current)".f10-after          # the base task
git config --get branch."$(git branch --show-current)".f10-after-branch   # its branch
```

Nothing recorded: nothing changes. Recorded: `git fetch origin`, find the base branch
local-first (`refs/heads/<branch>`, else `origin/<branch>`), then one of four:
- the base exists nowhere: the base task was finished and its branch deleted, so its code is
  in the default branch. `git config --unset` both keys and carry on as if nothing was recorded
  (`f10 finish` does this itself; this is the fallback for a base merged by hand);
- the base is an ancestor of HEAD (`git merge-base --is-ancestor <base> HEAD`): nothing to do;
- the base moved: bring the branch up to date before implement, the way `steps/catchup.md`
  does - `git rebase <base>`, or `git merge --no-edit <base>` where `project.md → Hosting & PR`
  declares `catchup: merge`. A conflict ends the run as a failure (`conventions/failure.md`),
  the operation aborted so the tree stays as it was, and the report's `next` row names
  `/f10:catchup`: it settles the conflicts with both sides kept, after which this run can be
  re-issued. Ship never settles conflicts itself - an implement step over a half-merged tree
  is the outcome the failure convention exists to prevent;
- the base still equals the default branch's commit: the base task has no code yet. Ask once
  (AskUserQuestion): **wait**, or **proceed on the plan's assumption**. Wait ends the run blocked
  the way a judge stop does - `f10-state.sh set ship blocked implement`, a `note` naming the base
  task - and is not a failure. Proceed builds against the base task's plan as the plan file
  records it, with no fallback for its absence.

The pr step reads the same keys to stack the PR on the base branch (`steps/pr.md`).

Run the pipeline's steps **in the declared order**. The `--all` bundle already carries every
generic step, and project-defined ones arrived in it as overlays, so read nothing further before
running them. Any extra text the user adds is steering/notes for the run.
For a plan you just wrote this run, proceed without re-confirming; open gaps are surfaced by
the implement step (`conventions/gaps.md`).

**A `judge` step routes the run by its verdict** (`steps/judge.md`): proceed continues; stop
ends the run blocked - `f10-state.sh set ship blocked judge`, the verdict as the report, no
FAILED block; rethink or proceed with changes open a discussion whose gaps you put to the user
in one questionnaire. If they choose to continue, fold the answers into the plan file the way
you fold answered gaps, then run the next step; if they block, end as stop does. Never continue
past anything but proceed on your own.

**Badge** (`conventions/report.md`): once the pipeline is selected, declare its last step -
`f10-state.sh final <step>` - so a turn ending mid-pipeline keeps its spinning glyph. Then
`f10-state.sh set ship running <step>` before each pipeline step, named as the pipeline names
it, and `f10-state.sh set ship done` after the last. Where the plan came from an earlier run -
the plan-file route, or the reuse branch
above - add `f10-state.sh set plan prior`. Where the pipeline stops mid-way but durable
artifacts exist - commits, an open PR, a deploy - report `set ship partial <step>` rather
than `failed`, per `conventions/failure.md` rule 7, and `note` what stopped it and what
unblocks it in the same call.

**Final report:** lead with the facts block per `conventions/report.md`, carrying the run's
**shipment** - the branch, the PR/MR url where the pipeline opened one, the sha where it
committed or pushed, the deploy url where it deployed. Then, as prose: review status as far as
the pipeline goes ("stopped at PR" is a normal end), deploy status if it deploys, a short note of what changed,
and - if any gaps stayed on their defaults - a one-line reminder that they're recorded in the
plan.

Note: running this skill is the user's explicit go-ahead to push and open the PR
(`steps/pr.md`); deploy steps carry their own confirmation rules (`steps/deploy.md`).
