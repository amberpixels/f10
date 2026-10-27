---
name: ship
description: "f10 ship skill (full run) - take a task through the project's flow: implement, then whatever the project declares (review, commit, PR/MR, deploy). Use when the user says \"/f10:ship <task-id | plan-file.md | description>\". Fetches and plans as needed, then runs the flow end to end."
allowed-tools: Bash, Read, Write, Edit, Grep, Glob, Agent, AskUserQuestion, Skill, ScheduleWakeup, ExitPlanMode
argument-hint: "<task-id | plan.md | description>"
---

# f10 · ship

Running this skill **is** the user's explicit authorization to push and open or refresh this task's PR (the **pr** step), satisfying the global "never push / open a PR unless asked" rule for that PR only, and to post resolve's `+reply`, push resolve's fixes to that PR, post review's `f10 review ack`, and for a CI half in the flow its `f10 review trigger` (when the Review section declares a manual how it can read) and resolve's `f10 review settle`, for a judge entry after `pr`, push the catchup guard's integration and its own changes to that PR and post its status and comment, and post the run's report-back comment and status on the task (**Report back**).
Deploy has its own confirmation (the **deploy** step).

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- `--dry-run`: follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` instead of running anything, and stop.
  It reports which steps the rest of the argument would run.
- `--driven`: follow `${CLAUDE_PLUGIN_ROOT}/modes/driven.md`; everything below still runs.
- A flow name as a word (`/f10:ship direct: fix typo …`; `f10 start --local` sends `/f10:ship local: <id>`, `--flow <name>` sends `<name>:`), or named in the user's own words: run that flow.
  Otherwise the flow the frontmatter's `flow_by_area` maps the task's area to (the fetch brief's areas, else the plan's `Areas` line), else `default`.
  Two areas mapping to different flows: the deliverable's area wins, as for Roles, and the brief names the other.
  The area map is the project's choice, not yours: never self-select another flow, though for tiny work you may suggest one.
  Name the flow an area chose, and the area, in one line before the first step.
- A **plan file** (`*.md` path): run the flow with it.
- A **task id** (project id format / id / url), or a **bundle** joined by `+` (`ABC-1+ABC-2`, keyed on the lead): follow `${CLAUDE_PLUGIN_ROOT}/modes/forward.md` first, before loading anything; stop if it forwards the task.
  Otherwise run the **fetch** step, then look for **`<storage root>/plans/<TASK-ID>.md`** (a bundle's lead) (glob the plans dir if the exact name misses).
  Found: reuse it, never asking; a task with a plan file never plans again, and replanning is `/f10:plan <id>` by hand.
  Missing: run the **plan** step when the flow leads with `plan` (with `flow_by_area`, start and drive leave planning to ship); a **planless** flow (below) never runs it, and the fetch brief becomes the inline plan.
  Then the flow.
- A **free-text description**: the **capture**, **fetch** and **plan** steps, then the flow.
- Extra text beside the subject is steering.

**Run**
1. Run `f10-load-project.sh` first, on every call of this skill, naming the steps the input needs: `capture fetch plan` for a description, `fetch plan` for a task id, none for a plan file.
   The flow is declared in the `project.md` it prints; a planless one loads `plan` but never runs it.
2. Follow those steps in the order the Arguments item gives.
3. Load each flow entry just before it runs, in one call with its badge: `f10-state.sh set ship running <entry>; f10-load-project.sh <step>`, and follow the step from that load.
   A `review` entry loads `review resolve`; a change judge after a `pr` entry loads `judge implement commit`, for the yellow route; the catchup guard (below) adds `catchup` to the load of each entry it guards.

**Flows** are the frontmatter's `flows:`, a name to its ordered entries, a list or one string split on spaces or arrows (`plan judge implement cleanup:comments,prose pr review:ci deploy:staging`); none declared is `default: plan implement pr`.
`f10 flow` lists them and what selects each.
An entry is a step, or `step:arg`: a generic step plus its overlay, or a project-defined one, the argument passed to it (`review:local` or `review:ci` names the half, `review:local+ci` runs both into one round and one resolve, `cleanup:<targets>` the cleanup targets, `deploy:<env>` the environment).
The run starts at the entry after a leading `plan`, which runs only for a task with no plan file.
`resolve` is never named: a `review` entry runs it.
A `judge` entry judges by position: before `implement` the **plan judge** (subject `plan <id>`), whose yellow verdict revises the plan file in place and writes no code; after it the **change judge**, routed as the **judge** step says before and after `pr`.
A `pr` entry opens the branch's PR, or refreshes it when one is open, so a flow may name `pr` again after review or judge to bring the description up to date.
A flow with no leading `plan` is **planless**: it writes no plan file, and a brief inline plan in chat stands in for it unless a saved one exists.
The old keys (`pipelines`, `planless`, `drive_chain`) still read, converted to flows with a warning that names `f10 init --migrate`, and so does the old entry form `review (local)`.
Free-text input also skips capture and fetch, so the run has no task id.
A task id runs fetch and keeps the run tied to the id (badge, report, branch, review directory), and marks `f10-state.sh set plan skipped` once fetched, unless it reuses a saved plan (`prior`, below).
Its inline plan states the kind fetch settled, with a bug's reproduction and root cause, a refactor's invariant or a spike's question in a line each, and lists any gaps on their defaults.
The **shipment** is the last delivering entry's (`conventions/context.md` → shipment): its rows are the ones that step declares, a project-defined step's on its overlay's `Shipment:` line; a `judge` or `demo` after it adds rows, never replaces them.
A `demo` entry with nothing to show skips in one line (the **demo** step), a normal end that adds no rows.

**A spike ends after implement**, whatever the flow: a `Kind: spike` plan answers a question, and its code never leaves the machine.
No step after implement runs (no cleanup, commit, push, pr, review, judge or deploy); the shipment is the spike report.
`f10 finish` cannot close it (it would merge the experiment code, and needs a clean worktree), so the final report ends with the manual close.
In a linked worktree with in-repo storage: copy the spike report and the plan, when there is one, into the main checkout's storage root (`spikes/`, `plans/archive/`), then, from the main checkout, `git worktree remove --force <path>`, `git branch -D <branch>`, and close its Herdr workspace.
In the main checkout, or with out-of-tree storage, the report already outlives the code: discard the experiment code by the paths the report names.
Its report back (below) carries the question, the answer and the recommendation; it names the spike report's path only under `visibility: public`, since stealth keeps `.f10/` paths off the tracker.
The kind is known from the plan, or a planless run's inline plan, before the flow starts, so the badge declares `f10-state.sh pipeline implement`, and a dry run prints the path cut at implement.

**The catchup guard** brings the base in before three kinds of entry, as the **catchup** step's **As a guard** says; under `catchup: off` it never runs, and its load leaves `catchup` out.
Catchup is never a flow entry.

- **Before implement**, only where `f10 start --after` or `f10 finish` recorded a base on the branch (`f10-after-branch` or `f10-landed`).
  A base with no code yet (its branch still at the default branch's commit): before integrating, ask once (AskUserQuestion) **wait**, or **proceed on the plan's assumption**.
  Wait ends the run blocked (`f10-state.sh set ship blocked implement`, a `note` naming the base task).
  Proceed builds against the base task's plan as the plan file records it, with no fallback if it is absent.
- **Before every `pr` entry**, the first and each refresh: fetch and check ancestry first; only when the base moved, commit what the tree holds as the pr step would (`steps/commit.md` point 2, `--step implement`) and integrate, so the push carries it.
  An unmoved base leaves the commit to pr and its adapter.
- **Before a change judge** (a `judge` after `implement`; the plan judge has no code): with uncommitted work in the tree (implement leaves its work uncommitted, so `implement judge pr` always has some), skip it in one line: that judge sees the branch on its old base, and a later `pr` entry's guard, when the flow has one, brings the base in before the code leaves.
  After a `pr` entry, push the integration, never forcing, so the PR head is the code judged and the judge's status lands on it.

The guard runs under the entry it guards, in that entry's load (`catchup pr`) and badge.
A failure is `set ship failed <entry>`, or `partial` once the run left something durable, the guard's own commit or an open PR (`conventions/failure.md` rule 7), its `state` row naming that commit and its `note` naming catchup and `/f10:catchup`.

Run the flow's entries **in the declared order**, each from its own load.
A plan written this run needs no re-confirming.
A `judge` entry routes the run by its verdict: the plan judge as the judge step's before-`pr` route says, its changes folded into the plan file; the change judge differently before and after a `pr` entry (the **judge** step).
After `pr`, the changes folded into the plan are built by the judge step's own route, not by a rerun of `implement`.

**Badge** (`conventions/report.md`): `f10-state.sh pipeline <entry>...` once the flow is selected, every entry after `plan` as the flow names it, one argument each (`pipeline judge implement cleanup pr review:local judge pr`), which also declares the last as final; `f10-state.sh set ship running <entry>` before each entry, named as the flow names it, in the call that loads it; `f10-state.sh set ship done` after the last.
A plan from an earlier run (plan file, or reuse) adds `f10-state.sh set plan prior`.
Stopping mid-way with durable artifacts is `set ship partial <step>` with a `note` (`conventions/failure.md` rule 7).

**Report back**, after the last step (a spike's: implement), when the run has a task id, the frontmatter's `report_back` is not `off`, and the shipment left the machine: a flow whose last delivering entry is `implement` or `commit` promises it does not, and reports back nothing.
A spike's answer is the exception: it carries no code, so it is posted.

1. Compose one comment from the shipment rows (each url, the PR/MR, commits by sha) and two or three lines on what changed.
   It leaves the machine: no f10, plan or findings mention under stealth (`conventions/context.md`), and other tasks named by title.
   A bundle comments on the lead and names the riders.
2. `f10 task comment <id>` with it on stdin, in a call of its own.
3. With `tracker.status.shipped` declared, `f10 task update <id> --status <that word>`, for every bundle member.

When the tracker is the host's issues and the shipment is a PR/MR carrying the task's closing keyword, skip points 2 and 3: the host links the two, and closes the issue on merge, not when the PR opens.

Exit 3 from either is a normal end: the tracker takes no such write, and the final report alone carries the shipment.
Any other failure leaves the shipment standing: `f10-state.sh set ship partial <last step>` with a `note`, and the comment is never retried, since it may have posted.
A driven run reports back too; nothing is asked.

**Exit**
- Stop after the flow's last entry (a spike's: implement) and the report back, with the final report: the facts block with the **shipment** rows the last delivering entry declares, plus a judge's or demo's when they ran (branch, PR/MR url, sha, deploy url, a spike report's path, a project-defined step's rows, a demo report) and a `comment` row with the url report back got, then review status ("stopped at PR" is a normal end), deploy status, a bug's or refactor's proof or a spike's answer (implement's line) and its manual close, what changed, and a one-line reminder of gaps left on their defaults.
