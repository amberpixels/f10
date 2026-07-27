# Step · fetch - task → understand + investigate

Role: a **senior engineer** doing due diligence before touching any code.
Input: a task identifier (in the project's id format).
Context: load per `conventions/context.md` - the tracker's **fetch adapter** comes from
`.f10/instructions/project.md` (+ `.f10/instructions/fetch.md` overlay if present).

1. **Fetch the task** via the project's fetch adapter - a skill to invoke, or a CLI command
   such as `gh issue view <n> --comments` / `glab issue view <n>`. Read the full task,
   including comments.
2. **Investigate the code - but only the genuine unknowns.** The task text and anything already
   established in this conversation are **ground truth**: do not spend investigation re-verifying
   a premise the task or the user already gave you (e.g. the task says module A lacks the
   behavior its sibling B already has - that premise *is* the task; don't go confirm it).
   Start from what's settled and investigate only what you still need to design well: the exact
   code you'll mirror or touch, existing patterns to reuse, callers, downstream consumers behind
   a real decision, and tests.
   - Prefer a few **targeted `Read`/`Grep`** calls over broad `Explore` fan-out. Do **not** launch
     multiple overlapping Explore agents on the same question - parallel broad explorers routinely
     return contradictory summaries and you then waste turns reconciling a contradiction you
     manufactured. One focused pass beats three sweeping ones.
   The goal is to plan in the grain of the codebase, reusing what exists - not to re-derive the
   codebase from zero every run.
3. **Surface obstacles explicitly.** Task out of date vs. the current code, internal
   conflicts, ambiguous or missing requirements, hidden coupling, data/migration concerns.
4. **Decide like a senior engineer.** Resolve ~90% of open questions yourself from the code
   and its house style. The _major_ forks left over - ones that change scope or are hard to reverse -
   become **gaps**: don't block the run asking them inline. Note each with a sensible default you'd
   proceed on, and carry them into the plan's Gaps section (see `conventions/gaps.md`). Don't nickel-and-dime.
5. **Output** a short investigation brief: what changes, where, the **areas** the task really
   touches (correcting the task's own _Areas_ note if investigation disagrees, and deriving the
   areas from the code when the task is small enough to carry no note at all - either way, name
   which conditional roles in `project.md → Roles` they trigger), the obstacles found, the
   decisions you took, and the list of gaps (with their defaults) for the plan to record.

**On failure:** the id does not resolve, or the tracker is unreachable - stop and report it.
Never plan against an assumed or invented task. See `conventions/failure.md`.
