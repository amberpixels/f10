# Step · fetch - task → understand + investigate

Role: a **senior engineer** doing due diligence before touching any code.
Input: a task identifier (in the project's id format).
Context: per `conventions/context.md` - the tracker's **fetch adapter** comes from
`project.md` (+ same-named overlay).
Badge (`conventions/report.md`): `f10-state.sh set plan running` on entry - fetch opens the plan
phase - and `f10-state.sh task <id> <url>` once the task resolves.

1. **Fetch the task** via the project's fetch adapter - a skill to invoke, or a CLI command
   such as `gh issue view <n> --comments` / `glab issue view <n>`. Read the full task,
   including comments.
2. **Investigate the code - but only the genuine unknowns.** The task text and anything already
   established in this conversation are **ground truth**: never spend investigation
   re-verifying a premise the task or the user already gave you. Start from what's settled and
   investigate only what you still need to design well: the exact code you'll mirror or touch,
   existing patterns to reuse, callers, downstream consumers behind a real decision, and tests.
   Prefer a few **targeted `Read`/`Grep`** calls over broad `Explore` fan-out, and never launch
   multiple overlapping Explore agents on the same question - parallel broad explorers return
   contradictory summaries you then waste turns reconciling.
3. **Surface obstacles explicitly.** Task out of date vs. the current code, internal
   conflicts, ambiguous or missing requirements, hidden coupling, data/migration concerns.
4. **Decide like a senior engineer.** Resolve ~90% of open questions yourself from the code and
   its house style. The _major_ forks left over - ones that change scope or are hard to
   reverse - become **gaps**: note each with a sensible default you'd proceed on and carry them
   into the plan's Gaps section (see `conventions/gaps.md`) rather than blocking the run to ask.
   Don't nickel-and-dime.
5. **Output** a short investigation brief: what changes, where, the **areas** the task really
   touches (correct the task's _Areas_ note if investigation disagrees, or derive areas from
   the code when the task carries none; name the conditional roles in `project.md → Roles`
   they trigger), the obstacles found, the decisions you took, and the list of gaps (with
   their defaults) for the plan to record.

**On failure:** the id does not resolve, or the tracker is unreachable - stop and report it.
Never plan against an assumed or invented task. See `conventions/failure.md`.
