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

**Forward first, when the argument is a task id.** A task started with `f10 start` has its own
worktree, workspace and agent, and its plan sits in that worktree's storage root; shipping it
here would use the wrong branch and no plan. Before loading anything, one call:

```
f10 forward <task-id> "/f10:ship <the argument verbatim>"
```

- **exit 0** - another checkout holds the task's branch and the command went to its agent.
  Print the command's block (task, workspace, sent) in a fenced `yaml` block, add nothing, and
  stop. Join `f10-state.sh clear` to the same call to retire the badge seeded when the command
  was typed: this session has no run.
- **exit 3** - the task is here (its branch is this checkout's, or it has no worktree), as
  stderr says. Continue below.
- anything else is a failure per `${CLAUDE_PLUGIN_ROOT}/conventions/failure.md` (outside Herdr,
  no workspace shows the worktree, the agent is idle and not reporting). Report it and stop.

Without the `f10` binary on `PATH`, read `git worktree list --porcelain` once. A worktree other
than this checkout whose branch is `<id>`, `<id>/...` or `<id>-...` holds the task out of reach:
fail, pointing at `go install github.com/amberpixels/f10/cli/cmd/f10@latest`. Otherwise continue.

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

**Dependency:** before the first pipeline step, read what `f10 start --after` and `f10 finish`
recorded on the branch:

```
git config --get branch."$(git branch --show-current)".f10-after          # the base task
git config --get branch."$(git branch --show-current)".f10-after-branch   # its branch
git config --get branch."$(git branch --show-current)".f10-landed         # a base finished since
```

- **`f10-landed`**: the base task merged after this branch began. Catch up with the default
  branch, then `git config --unset` the mark.
- **`f10-after`**: `git fetch origin`, find the base local-first (`refs/heads/<branch>`, else
  `origin/<branch>`), then:
  - exists nowhere: the base was finished; `git config --unset` both keys and catch up with the
    default branch;
  - an ancestor of HEAD (`git merge-base --is-ancestor <base> HEAD`): nothing to do;
  - moved: catch up with `<base>`;
  - still at the default branch's commit (no code yet): ask once (AskUserQuestion) **wait**, or
    **proceed on the plan's assumption**. Wait ends the run blocked
    (`f10-state.sh set ship blocked implement`, a `note` naming the base task). Proceed builds
    against the base task's plan as the plan file records it, with no fallback if it is absent.
- Nothing recorded: nothing to do.

**Catching up** happens just before implement (target: the base, or `origin/<default>` after
the fetch): `git rebase <target>`,
or `git merge --no-edit <target>` where `project.md → Hosting & PR` declares `catchup: merge`. A
conflict fails the run: abort so the tree is unchanged, keep any mark, and name `/f10:catchup`
in the `next` row. Ship never settles conflicts or implements over a half-merged tree.

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

Running this skill authorizes the push and the PR (`steps/pr.md`); deploy has its own
confirmation (`steps/deploy.md`).
