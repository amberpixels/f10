# Step · debug - from a report to a confirmed cause

Role: a **senior engineer** investigating a problem before anyone plans its fix.
Input: one of two things.
From `/f10:debug`, it is a report with no task yet: an error, a stack trace, log lines, a screenshot of a chat or a support thread, or a complaint in the user's own words.
From the fetch step, it is a bug that already is a task (see **A bug that is a task**).
Output: a debug brief per bug, for the capture step or for the plan's `## Reproduction`.
A report that holds no bug gets an answer instead, and one whose cause stays unsettled gets a report of what was tried and what to collect next.
Context: the loader's `verify:` line, which names the commands that run the code, and the frontmatter's `debug_costly`.
Badge: `f10-state.sh set capture running debug` on entry from `/f10:debug`. From fetch, it is `f10-state.sh set plan running debug`.

**This step never fixes the bug.**
The fix goes through plan and ship, so that the task and the plan both carry the evidence.
The step leaves the working tree as it found it.
A reproduction script goes in the session's scratch directory.
A reproduction test written into the tree is removed once it has failed, before capture or the plan.
The brief names its file, its name and its assertion, so the implement step writes it first.

**The goal is the cause, and reproducing is one way to confirm it.**
The cause is what happened, where in the code, since when, and to whom.
Many reports settle without running anything: a log line shows the path the code took, a record shows the bad value, or a commit's date matches the day the complaints began.
Reproduce when it is cheap, or when nothing else can settle the cause.

1. **Read the report.**
   Read every part of it: text, screenshots, logs and traces.
   A report can hold several problems, as a chat thread often does.
   List each one and investigate it on its own, because unrelated problems never share a task.
   With more than three, ask which to chase in one `AskUserQuestion` (driven: one ask, `modes/driven.md`).
   For each problem, note the symptom, who saw it, where it happened (environment, version, account) and when, and what the reporter expected.
   The reporter's words name a symptom, not a cause: "the link is broken" may be an expired token, a wrong host or a missing permission.
   A report phrased as a question ("do reminders go out on weekends?") is still a report.
   What the code does is its answer, and the complaint behind the question is the symptom.
   If the report gives no expected behavior, infer one from the code, and say in the brief that it was inferred.
2. **Find the code behind the symptom.**
   Start from what the reporter sees, such as a message, a scheduled job, a page, a link or an error string, and trace it back to the code that produces it.
   Name the candidate causes, each with the evidence that would tell it apart from the others.
3. **Gather evidence, cheapest first.**
   - **The code path for the reported input:** the conditions, schedules, filters and early returns that would produce the symptom.
   - **The path's history:** `git log -S`, `git log -L` or `git blame` on it, to find when its behavior changed.
     Compare that date with the day the reports began.
   - **Config and environment:** flags, settings, and what differs between where it works and where it fails.
   - **Logs and data:** what the report gives, and what the project's tools can read.
     Production is read-only to this step.
   - **A reproduction**, where the project can run it: a test, the CLI or the verify commands, starting with the smallest input.
     A report that fails in one environment only ("works on staging, not prod") is reproduced by pinning the difference (config, version or data) and showing that changing it flips the result.
     If the report says it used to work and a check can run on each commit, find the commit that broke it with `git bisect run`.

   Stop gathering once one candidate explains every symptom and the evidence rules out the others.
4. **Settle the root cause** at a `file:line`.
   The line that throws or skips is only the symptom when the bad value or decision was made somewhere else.
   A cause is **confirmed** when it explains every symptom in the report and evidence beyond the code read shows it.
   That evidence is a run, a failing test, a log line, a record, or a change dated to the onset whose diff introduces the cause.
   A change that only shares the date, or only touched the same lines, is a lead, because another change may have landed the same day.
   A cause found by reading alone is **likely**.
   Note **since when** it happens (a commit, a deploy, a config change, a date, or always) and its **scope**: who or what is affected, and whether every case fails or only some.
5. **Sort the outcome** of each problem.
   - **A bug with a confirmed cause:** write its brief (point 7).
   - **Not a bug:** the code does what it was built to do, or the cause is data, configuration, a third party or how the product was used.
     Report the answer with its evidence, in words the user can pass on to the reporter.
     No task is created.
     When the expected behavior is worth building, offer in one line to capture it as a feature.
   - **Unsettled:** the cause is likely or still a guess, so point 6 runs.
6. **Ask for the missing evidence.**
   Ask in one `AskUserQuestion` (driven: one ask) for exactly what would settle the cause, with one question per unsettled problem.
   Name it precisely: a log search with its terms and time window, a record's id, the version that was live, or what the reporter clicked.
   With a likely cause, one option captures it as it stands, with the outcome `unconfirmed`.
   Retry once with the answer.
   If the cause is still unsettled and the user did not choose to capture it, that problem gets no task and no file.
   The report names what was found, what was tried and what to collect next.
   The other problems go on: a confirmed bug still gets its brief and its path.
   When no problem in the report gets a brief or an answer, run `f10-state.sh set capture blocked debug` with a `note` in the same call, naming what was tried and what to collect next, and stop at the report.
7. **Write the debug brief**, one per bug.
   It has four parts:
   - the report, in one line
   - **Reproduction**: the outcome line (see **The outcome**), then the steps or input with the expected result and the actual one.
     With no run, it holds the evidence that shows the bug instead: log lines, records, or a dated change and its hunk.
   - **Root cause**: the `file:line`, the evidence that confirmed it, since when it happens and whom it affects
   - the test that will fail before the fix: its name, and its assertion in words.
     Where no automated test can show the bug, name a manual check instead.
8. **Choose the path**, for a bug from `/f10:debug`.
   The usual path is capture, fetch and plan, so the fix ships as a task with a plan.
   A quicker path exists when the project's `flows` declare a flow that skips part of that:
   - a planless flow, one with no leading `plan`, skips the task and the plan, because ship runs free text on it without capture or fetch
   - a flow that ends in `push` instead of `pr` skips the PR and its review (`steps/push.md`), but still captures and plans when it leads with `plan`

   Suggest one only when the bug is urgent and its fix is small.
   A bug is urgent when it hurts users now: production broken, data lost or wrong, money or security at stake, or a deadline such as tonight's scheduled job.
   A fix is small when it changes one place and needs no decision.
   Suggest it in one `AskUserQuestion`: the usual path, and `/f10:ship <name>: <the brief>`.
   Each option says what it skips, read from the flow's entries (`f10 flow <name>`).
   Run the quicker path only when the user picks it, and never on your own.
   A driven run never suggests it and takes the usual path.

When every problem in the report is settled as not a bug, run `f10-state.sh set capture skipped debug` with a `note` naming the answer, and stop at the report.

## A bug that is a task

The fetch step runs this section, from its point 3, when it settles a task's kind as `bug`.
The task's text is the report, and points 1 to 4, 6 and 7 run on it with the changes below.
No capture follows, because the task exists: the brief becomes the plan's `## Reproduction`, which has the same fields.

**Triage first.**
Before gathering evidence, read the task and the code it points at, and settle how much reproduction the bug deserves.
Write the depth and its reason as one line of the brief.

- **obvious** - the cause is visible at a `file:line` the task names or one quick read finds, and a unit-level test can hit it.
  Write that one failing test and watch it fail: that is the reproduction.
  Skip environment pinning, bisect and input minimizing.
  The test is still required, because an obvious-looking bug is where a fix lands on the symptom, such as the line that throws when the bad value is made upstream.
  If the test does not fail as expected, the depth becomes **full**.
- **full** - the cause is not visible, the task says it used to work, or the failure depends on input the test must find.
  Points 1 to 4 run as written, bisect included.
- **costly** - reproducing needs what the project cannot run cheaply.
  Examples are an external service, production data, a race, UI timing, hardware and a long setup.
  Ask before spending it.
  Interactively, ask one `AskUserQuestion` with three options:
  - **reproduce anyway**, with an estimate of what it needs, such as "a local Postgres with the prod schema, about 20 min".
  - **fix from the code**: diagnose from reading the code and the evidence the task carries, with the outcome `skipped (costly)`.
    Write a regression test after the fix where one is possible.
  - **I'll give you a reproduction**: the user pastes steps, logs or data, and the step goes on from them.

  A driven run never asks this, because the question would stall the drive.
  The frontmatter's `debug_costly` decides instead.
  Its default, `skip`, means **fix from the code**.
  `reproduce` means **reproduce anyway**.
  `ask` puts the same three options in one ask (`modes/driven.md`).

The steering word `no repro` sets the outcome to `skipped (no repro)` for this run, and `with repro` sets the depth to **full**, whatever triage would say.

**Not confirmed.**
Point 6 asks its one question (driven: one ask) and retries once.
If the cause is still unsettled, the plan is written anyway, with the outcome `unconfirmed` and what was tried.
The run does not block, because a reviewer and the judge see the gap in the plan.

## The outcome

The brief opens its reproduction with `Reproduction: <outcome>`, one of:

- `full` - a run reproduced the bug
- `obvious` - a failing test written for it is the reproduction
- `traced` - evidence other than a run confirmed the cause, such as logs, records or a change dated to the onset whose diff introduces the cause
- `skipped (costly)` - triage chose to diagnose from the code
- `skipped (no repro)` - the steering chose it
- `unconfirmed` - the cause is likely but unproven, and the brief says what was tried

## Attribute

Once a task id exists and the outcome is settled, attribute the bug to the task that introduced it.
This runs for every outcome, `unconfirmed` included, because the recorded outcome is how a drive names a task planned without a reproduction.
With no root cause named, skip points 1 and 2 and record the outcome alone (point 3).
Fetch runs this after the section above, and after a `/f10:debug` run's capture.
A failure anywhere here is one line in the brief, never a stop, because attribution must not block a fix.

1. **Find the introducing commit.**
   Use the `git bisect` result when one ran, and say `--found-by bisect`.
   Otherwise run `git blame` on the root-cause lines at the base, and say `--found-by blame`.
   Bisect is evidence, but blame is only a lead, because the last commit to touch a line did not necessarily cause the fault.
2. **Read that commit's diff** and settle whether it introduced the fault: `confirmed`, `likely` or `unclear`.
   If the fault is an interaction with code the commit did not touch, or depends on data or an environment no diff review could see, pass `--class out-of-reach`.
3. **Record it**: `f10 task regression <task-id> --commit <sha> --lines <path:start-end> --found-by <bisect|blame> --confidence <c> --repro <outcome>`, one `--lines` per faulty range, numbered as the base has them.
   `--repro` takes the outcome's first word, so `skipped (costly)` and `skipped (no repro)` are both `skipped`.
   With no commit found, leave out `--commit` and `--lines`, because the outcome is still recorded.
   The command finds the commit's task and classifies how its review let the fault through (`docs/cli.md` → Regression).
4. **Carry its printed line** into the brief.
   When it names a task, the plan's header gains `Regression of: <the line>`.

**On failure:** debug binds no adapter.
A verify command that cannot run at all, such as a missing toolchain, fails the step (`conventions/failure.md`).
A report that holds no bug, or whose cause stays unsettled, is a declined outcome, not a failure.
