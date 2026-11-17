# Step · debug - from a report to a confirmed cause

Input: a raw report from `/f10:debug`, or a bug task from the investigate step.
A raw report is an error, logs, a screenshot or a complaint.
A task is the same report in a tracker, and runs the same investigation.
Output: a debug brief per bug, or an investigation report.
Badge: leaf `debug`, in the capture phase (`f10-state.sh set capture running debug`).
When investigate runs it, the leaf is in the plan phase.

**This step investigates and never fixes.**
It finds the real cause and proves it exists, and the fix goes through plan and ship.
A reproduction is not required.
A reproduction test that failed is kept for the fix: the brief carries its code, implement adds it first, and it turns green with the fix.
The tree is left as found, because the fix may run in another worktree.

**A task that carries an investigation** (a root cause at `file:line` with its evidence) asks once: **trust it** or **verify it**.
Trust carries it into the brief as written, with the outcome `trusted`.
Verify runs the points below and checks the result against the task's account.
A driven run trusts it when the cited code still reads as the task says, and verifies otherwise.

1. **Split a raw report into problems**, each investigated on its own, because unrelated problems never share a task.
   With more than three, ask which to chase (driven: one ask).
   A bug task stays one task and one PR, unless its parts should ship separately, which the brief then proposes as a gap.
   An expected behavior inferred from the code is marked as inferred.
2. **Gather evidence, cheapest first**: the code path, its git history against the onset, config, logs and data, then a reproduction.
   Production is read-only.
   A failing unit test is the cheapest reproduction, and it stays required when the cause looks obvious, because that is where a fix lands on the symptom.
   A reproduction the project cannot run cheaply, such as one needing production data, is asked about first.
   The options are **reproduce anyway** with an estimate, **diagnose from the code** (`skipped (costly)`), and **I'll give you a reproduction**.
   A driven run asks only under `debug_costly: ask`, and otherwise takes `skip` (the default) or `reproduce`.
   The steering `no repro` gives `skipped (no repro)`, and `with repro` forces a reproduction.
3. **Settle the cause** at the `file:line` where the bad value or decision is made, not where it surfaces.
   It is **confirmed** when it explains every symptom and evidence beyond the code read shows it.
   A change dated to the onset counts only when its diff introduces the cause.
   Otherwise it is **likely**.
4. **Sort each problem.**
   - A confirmed bug gets a brief.
   - Not a bug (working as built, data, config, a third party) gets an answer with evidence, and no task.
   - Unsettled: one `AskUserQuestion` for all of them (driven: one ask), naming the exact evidence that would settle each.
     Retry once with the answer.
     From `/f10:debug`, a likely cause gets an option to capture it as `unconfirmed`, and a problem still unsettled gets no task.
     From a task, it is planned anyway as `unconfirmed`, because the reviewer and the judge see the gap in the plan.

   One problem's outcome never holds back the others.
   From `/f10:debug`, with no brief and no answer, the badge goes `blocked` with a `note` on what to collect, and with answers only, `skipped`.
5. **The brief**, which becomes a task by capture, or a task's plan `## Reproduction`:
   - the report, in one line
   - `Reproduction: <outcome>`, and the evidence
   - the root cause at `file:line`, since when, and whom it affects
   - the test that fails before the fix, else a manual check
6. **The path**, from `/f10:debug`.
   The usual path is capture, fetch, investigate and plan.
   For a bug that hurts users now with a one-place fix, offer a quicker flow the project declares.
   Run it only when the user picks it, and never in a driven run.
   A planless flow skips the task and the plan, and a flow ending in `push` skips the PR.
   The option is `/f10:ship <name>: <the brief>`, saying what it skips (`f10 flow <name>`).

## The outcome

- `full` - a run reproduced it
- `obvious` - a failing test is the reproduction
- `traced` - other evidence confirmed it
- `trusted` - the task's own investigation, taken as written
- `skipped (costly)` or `skipped (no repro)` - point 2
- `unconfirmed` - the brief says what was tried

## Attribute

Once a task id exists, for every outcome: `f10 task regression <task-id> --commit <sha> --lines <path:start-end> --found-by <bisect|blame> --confidence <confirmed|likely|unclear> --repro <outcome's first word>`.
The commit is the `git bisect` result when one ran, else `git blame` on the root-cause lines at the base, which is only a lead until its diff is read.
A fault no diff review could see adds `--class out-of-reach`.
With no cause or no commit, drop `--commit` and `--lines`.
A failure here is one line in the brief, never a stop.
A printed task becomes the plan header's `Regression of: <the line>`.
