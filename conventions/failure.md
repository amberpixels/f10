# Convention · failure - a step cannot complete

Every step assumes the happy path. This file covers the rest: a verify command that stays red,
an adapter that errors, a tracker that will not answer.

**Stop at the failing step. Report. Let the user decide.** A pipeline that keeps moving after a
failed step produces the worst outcome f10 can produce: an open PR whose verify never passed, or
a plan built on a task that was never really fetched.

## Rules

1. **Do not advance the pipeline.** The failing step is where the run ends. Do not start the next
   step, and do not "come back to it later".
2. **Never take an outward-facing action on a failed predecessor.** `pr`, `push`, and `deploy`
   leave the machine. Run none of them when an earlier step failed, verify is red, or a blocking
   review is unresolved - even when the user's original request was "ship it": their go-ahead
   covered a working change, not a broken one.
3. **Retry once, for the same cause, only when a retry could plausibly work** - a network blip, a
   transient tracker 5xx. A second identical failure is an answer. Never loop.
4. **Never silently substitute.** If `project.md` declares an adapter and it is missing or
   erroring, do not fall back to a different tool - substitution turns a visible failure into an
   invisible one.
5. **Preserve the work.** Output whatever the step had produced before it failed - the drafted
   task body, the investigation brief, the plan prose - so nothing has to be redone. A failed
   `Write` means the content belongs in the report.
6. **Leave the workspace honest.** Do not partially commit, do not push a branch you cannot open
   a PR for, and do not delete evidence. If the failure left something half-done, say exactly
   what.
7. **Mark the badge** before you write the report, so the status line shows where the run died:
   `f10-state.sh set <phase> failed` - or `partial` where the phase stopped **after this run
   produced something durable** (commits made but the push rejected, a PR open but its CI review
   red, a deploy that errored after merge). Check for artifacts, do not judge severity; a stop
   that left nothing usable stays `failed`. Cosmetic and best-effort like every badge call
   (`conventions/report.md`): if it errors, ignore it and report the real failure.

## Report format

```
f10 · <step> FAILED

what failed     <the action, and the adapter or command, quoted verbatim>
error           <the actual error text, not a paraphrase>
completed       <the steps that did succeed, and their artifacts>
state           <what exists now: branch, files written, task created, nothing>
preserved       <the content the step had produced, if any>
next            <the smallest thing the user can do to unblock it>
```

Same label column as the success block, but plain-fenced, colonless, and with no marker cell -
these values are prose, which the success block's `yaml` tag cannot take (see
`conventions/report.md`). A url anywhere in this report is still printed bare.

Then stop. Do not offer to continue as the next action, and do not ask a question whose answer
you could have found yourself - the user is reading this because you already could not.

## What is not a failure

A step that correctly declines is a **normal end**, not a failure. Ship stopping at an open PR
because the pipeline declares no review, a gap left on its default, a review that returns
findings you then fixed, a `(planless)` pipeline skipping the plan file: report these as
outcomes, in the run's normal report. Do not dress them up as errors.
