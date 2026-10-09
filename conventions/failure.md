# Convention · failure - a step cannot complete

A verify command that stays red, an adapter that errors, a tracker that will not answer.
**Stop at the failing step. Report. Let the user decide.**

1. **Do not advance the pipeline.** The run ends at the failing step; do not "come back to it
   later".
2. **Never take an outward-facing action on a failed predecessor.** Run no `pr`, `push`, or
   `deploy` after a failed step, on red verify, or with a blocking review unresolved, even when
   the user asked to "ship it".
3. **Retry once, for the same cause, only when a retry could plausibly work** (a network blip, a
   transient tracker 5xx). Never loop.
4. **Never silently substitute.** A declared adapter missing or erroring is a failure, not a
   reason to use a different tool.
5. **Preserve the work.** Output what the step produced (the drafted task body, the
   investigation brief, the plan prose). After a failed `Write`, the content goes in the report.
6. **Leave the workspace honest.** No partial commit, no pushing a branch you cannot open a PR
   for, no deleting evidence. Say exactly what was left half-done.
7. **Mark the badge** before the report: `f10-state.sh set <phase> failed`, or `partial` where
   **this run produced something durable** (commits made but the push rejected, a PR open but its
   CI review red, a deploy that errored after merge). Check for artifacts, do not judge
   severity. In the same call, `f10-state.sh note "<what failed>" "<next>"`.

```
f10 · <step> FAILED

what failed     <the action, and the adapter or command, quoted verbatim>
error           <the actual error text, not a paraphrase>
completed       <the steps that did succeed, and their artifacts>
state           <what exists now: branch, files written, task created, nothing>
preserved       <the content the step had produced, if any>
next            <the smallest thing the user can do to unblock it>
```

Then stop. Do not offer to continue, and do not ask a question you could have answered yourself.

**A step that correctly declines is a normal end**, reported as an outcome: ship stopping at an
open PR because the pipeline declares no review, a gap left on its default, review findings you
then fixed, a `(planless)` pipeline skipping the plan file, a judge verdict that ends a ship run
(`steps/judge.md`; badge `blocked`, never `failed`).
