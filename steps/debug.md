# Step · debug - from a bug report to a confirmed cause

Role: a **senior engineer** debugging a report before anyone writes a task for it.
Input: an error, a stack trace, or a description of wrong behavior.
No task exists yet.
Output: a debug brief for the capture step (point 6), or a report of what was tried and what to collect next.
Context: the loader's `verify:` line, which names the commands that run the code.
Badge: `f10-state.sh set capture running debug` on entry.

**This step never fixes the bug.**
The fix goes through plan and ship, so that the task and the plan both carry the reproduction.
The step leaves the working tree as it found it.
A reproduction script goes in the session's scratch directory.
A reproduction test written into the tree is removed before capture runs.

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

**On failure:** debug binds no adapter.
A verify command that cannot run at all, such as a missing toolchain, fails the step (`conventions/failure.md`).
A bug that does not reproduce is a declined outcome, not a failure.
