# Convention · failure - a step cannot complete

A verify command stays red, an adapter errors, a tracker does not answer.
**Stop at the failing step, report, and let the user decide.**

1. **Do not advance the pipeline.**
   The run ends at the failing step.
   Do not "come back to it later".
2. **Never take an outward-facing action after a failure.**
   Run no `pr`, `push` or `deploy` after a failed step, on red verify, or with a blocking review unresolved, even when the user asked to "ship it".
3. **Retry once, only when it could plausibly work.**
   A network blip or a transient tracker 5xx may clear on a retry for the same cause.
   Never loop.
4. **Never silently substitute.**
   A declared adapter missing or erroring is a failure, not a reason to use another tool.
5. **Preserve the work.**
   Output what the step produced: the drafted task body, the investigation brief, the plan prose.
   After a failed `Write`, put the content in the report.
6. **Leave the workspace honest.**
   Make no partial commit, push no branch you cannot open a PR for, delete no evidence.
   Say exactly what was left half-done.
7. **Mark the badge before the report**: `f10-state.sh set <phase> failed`.
   Use `partial` when **this run produced something durable**: commits with a rejected push, an open PR with a red CI review, a deploy that errored after merge.
   Check for artifacts, do not judge severity.
   In the same call, `f10-state.sh note "<what failed>" "<next>"`.

```
f10 · <step> FAILED

what failed     <the action, and the adapter or command, quoted verbatim>
error           <the actual error text, not a paraphrase>
completed       <the steps that did succeed, and their artifacts>
state           <what exists now: branch, files written, task created, nothing>
preserved       <the content the step had produced, if any>
next            <the smallest thing the user can do to unblock it>
```

Then stop, without a question you could have answered yourself.

**A step that correctly declines has ended normally.**
Report it as an outcome:

- ship stopping at an open PR because the pipeline declares no review
- a gap left on its default
- a bug fetch could not reproduce
- a spike ending at its report
- review findings you then fixed
- a planless flow skipping the plan file
- a judge verdict that ends a ship run (`steps/judge.md`), badge `blocked`, never `failed`
