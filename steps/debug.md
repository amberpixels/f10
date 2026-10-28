# Step · debug - from a bug report to a confirmed cause

Role: a **senior engineer** debugging a report before anyone writes a task for it.
Input: an error, a stack trace, or a description of wrong behavior; no task exists yet.
Output: a debug brief for the capture step, or a report of what was tried and what to collect next.
Context: `project.md → Verify` (the commands that run the code).
Badge: `f10-state.sh set capture running debug` on entry.

**It never fixes the bug.**
The fix goes through plan and ship, so the task and the plan carry the reproduction.
The working tree ends as it started: a reproduction script lives in the session's scratch directory, and a reproduction test written into the tree is removed before capture.

1. **Read the report**: the symptom, the input, where it happened (environment, version, commit), and what the reporter expected.
   A report with no expected behavior gets one inferred from the code, named as inferred in the brief.
2. **Reproduce it** where the project can run it (a test, the CLI, the verify commands), smallest input first.
   Record the steps or input, expected against actual.
   An environment-only report ("works on staging, not prod") reproduces by pinning the difference (config, version, data) and showing that it flips the result; log evidence alone is not a reproduction.
3. **Isolate**: narrow to the smallest input that still fails and, for "it used to work" with a runnable check, to the commit (`git bisect run`).
4. **Diagnose**: the root cause at a `file:line`, confirmed by evidence that changing it changes the outcome, not by reading alone.
   Separate cause from symptom: the line that throws is the symptom when the bad value was made elsewhere.
5. **Not reproduced, or the cause still a guess after a few reads**: ask what the user saw, under which input and version, in one `AskUserQuestion` (driven: one ask, per `modes/driven.md`), and retry the reproduction once with the answer.
   Still not reproduced: `f10-state.sh set capture blocked debug` and, in the same call, a `note` naming what was tried and what to collect next.
   Report both and stop: no task is created and no file written.
6. **Output the debug brief**: the report in one line; **Reproduction** (steps or input, expected, actual); **Root cause** (the `file:line` and the evidence that confirmed it); and the test that will fail before the fix, its name and assertion in words.

**On failure:** debug binds no adapter; a verify command that cannot run at all (a missing toolchain) fails the step (`conventions/failure.md`).
A bug that does not reproduce is a declined outcome, not a failure.
