# Step · debug - from a bug report to a confirmed cause

Role: a **senior engineer** debugging a bug before anyone plans its fix.
Input: an error, a stack trace, or a description of wrong behavior, with no task yet (`/f10:debug`), or a bug that already is a task, run from the fetch step (**A bug that is a task**, below).
Output: a debug brief for the capture step (point 6) or for the plan's `## Reproduction`, or a report of what was tried and what to collect next.
Context: the loader's `verify:` line, which names the commands that run the code, and the frontmatter's `debug_costly`.
Badge: `f10-state.sh set capture running debug` on entry from `/f10:debug`; from fetch, `f10-state.sh set plan running debug`.

**This step never fixes the bug.**
The fix goes through plan and ship, so that the task and the plan both carry the reproduction.
The step leaves the working tree as it found it.
A reproduction script goes in the session's scratch directory.
A reproduction test written into the tree is removed once it has failed, before capture or the plan, and the brief names its file, its name and its assertion so the implement step writes it first.

1. **Read the report.**
   Note the symptom, the input, where it happened (environment, version, commit) and what the reporter expected.
   If the report gives no expected behavior, infer one from the code, and say in the brief that it was inferred.
2. **Reproduce the bug** where the project can run it: a test, the CLI or the verify commands.
   Start with the smallest input.
   Record the steps or the input, and what was expected against what happened.
   Log evidence alone is not a reproduction.
   A report that fails in one environment only ("works on staging, not prod") is reproduced by pinning the difference (config, version or data) and showing that changing it flips the result.
3. **Isolate the failure** to the smallest input that still fails.
   If the report says it used to work and a check can run on each commit, find the commit that broke it with `git bisect run`.
4. **Diagnose the root cause** at a `file:line`.
   Confirm it with evidence that changing that line changes the outcome, because reading the code alone does not confirm a cause.
   The line that throws is only the symptom when the bad value was made somewhere else.
5. **If the bug does not reproduce**, or the cause is still a guess after a few reads, ask the user what they saw, with which input and on which version.
   Ask it in one `AskUserQuestion`.
   A driven run puts the same question in one ask instead (`modes/driven.md`).
   Then retry the reproduction once with the answer.
   If it still does not reproduce, or the cause is still a guess, run `f10-state.sh set capture blocked debug` with a `note` in the same call, naming what was tried and what to collect next.
   Report what was tried and what to collect next, and stop.
   No task is created and no file is written.
6. **Write the debug brief.**
   It has four parts:
   - the report, in one line
   - **Reproduction**: the steps or the input, the expected result and the actual one
   - **Root cause**: the `file:line`, and the evidence that confirmed it
   - the test that will fail before the fix: its name, and its assertion in words

## A bug that is a task

The fetch step runs this section, from its point 3, when it settles a task's kind as `bug`.
The task's text is the report, and points 1 to 4 and 6 run on it with the changes below.
No capture follows, because the task exists: the brief becomes the plan's `## Reproduction`, which has the same fields.

**Triage first.**
Before reproducing, read the task and the code it points at, and settle how much reproduction the bug deserves.
Write the depth and its reason as one line of the brief.

- **obvious** - the cause is visible at a `file:line` the task names or one quick read finds, and a unit-level test can hit it.
  Write that one failing test and watch it fail: that is the reproduction.
  Skip environment pinning, bisect and input minimizing.
  The test is still required, because an obvious-looking bug is where a fix lands on the symptom, such as the line that throws when the bad value is made upstream.
  If the test does not fail as expected, the depth becomes **full**.
- **full** - the cause is not visible, the task says it used to work, or the failure depends on input the test must find.
  Points 1 to 4 run as written, bisect included.
- **costly** - reproducing needs what the project cannot run cheaply: an external service, production data, a race, UI timing, hardware or a long setup.
  Ask before spending it.
  Interactively, ask one `AskUserQuestion` with three options:
  - **reproduce anyway**, with an estimate of what it needs ("a local Postgres with the prod schema, about 20 min");
  - **fix from the code**: diagnose from reading the code, the outcome `skipped`, with a regression test written after the fix where one is possible;
  - **I'll give you a reproduction**: the user pastes steps, logs or data, and the step goes on from them.

  A driven run never asks this, because the question would stall the drive.
  The frontmatter's `debug_costly` decides instead: `skip` (the default) is **fix from the code**, `reproduce` is **reproduce anyway**, and `ask` puts the same three options in one ask (`modes/driven.md`).

The steering word `no repro` sets the outcome to `skipped` for this run, and `with repro` sets the depth to **full**, whatever triage would say.

**Not reproduced.**
Point 5 asks its one question (driven: one ask) and retries once.
If the bug still does not reproduce, or the cause is still a guess, the plan is written anyway, with the outcome `unconfirmed` and what was tried.
The run does not block, because a reviewer and the judge see the gap in the plan.

**The outcome** is one of `obvious`, `full`, `skipped` or `unconfirmed`.
The brief opens its reproduction with `Reproduction: <outcome>`.
A skip names its reason, `skipped (costly)` when triage chose it and `skipped (no repro)` when the steering did.

## Attribute

Once a task id exists and the reproduction's outcome is settled, attribute the bug to the task that introduced it.
This runs for every outcome, `unconfirmed` included, because the recorded outcome is how a drive names a task planned without a reproduction.
With no root cause named, skip points 1 and 2 and record the outcome alone (point 3).
Fetch runs this after the section above, and after a `/f10:debug` run's capture.
A failure anywhere here is one line in the brief, never a stop, because attribution must not block a fix.

1. **Find the introducing commit.**
   Use the `git bisect` result when point 3 ran one, and say `--found-by bisect`.
   Otherwise run `git blame` on the root-cause lines at the base, and say `--found-by blame`.
   Bisect is evidence, but blame is only a lead, because the last commit to touch a line did not necessarily cause the fault.
2. **Read that commit's diff** and settle whether it introduced the fault: `confirmed`, `likely` or `unclear`.
   If the fault is an interaction with code the commit did not touch, or depends on data or an environment no diff review could see, pass `--class out-of-reach`.
3. **Record it**: `f10 task regression <task-id> --commit <sha> --lines <path:start-end> --found-by <bisect|blame> --confidence <c> --repro <outcome>`, one `--lines` per faulty range, numbered as the base has them.
   With no commit found, leave out `--commit` and `--lines`, because the outcome is still recorded.
   The command finds the commit's task and classifies how its review let the fault through (`docs/cli.md` → Regression).
4. **Carry its printed line** into the brief.
   When it names a task, the plan's header gains `Regression of: <the line>`.

**On failure:** debug binds no adapter.
A verify command that cannot run at all, such as a missing toolchain, fails the step (`conventions/failure.md`).
A bug that does not reproduce is a declined outcome, not a failure.
