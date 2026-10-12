# Step · fetch - understand and investigate a task

Role: a **senior engineer** doing due diligence before touching any code.
Input: a task identifier (in the project's id format).
Context: `project.md → Tracker` (the **fetch adapter**).
Badge: `f10-state.sh set plan running` on entry, `f10-state.sh task <id> <url>` once fetched.

1. **Fetch the task**: `f10 task read <id>`; without the binary, the project's fetch adapter (a
   skill to invoke, or a CLI command such as `gh issue view <n> --comments`).
   A base task, named by the steering (`f10 start --after`) or a body line `After: <id>`, goes
   into the brief: the plan step records it as the plan's **Depends on** line and designs
   against the base task's plan (its task, where the plan is not written yet), never this
   checkout, with no fallback for its absence.
2. **Investigate only the real unknowns.** The task text and anything established in this
   conversation are **ground truth**: never re-verify a premise the task or the user gave you.
   Investigate only what you need to design well: the exact code you'll mirror or touch,
   existing patterns to reuse, callers, downstream consumers behind a real decision, and tests,
   in a few targeted reads (`conventions/latency.md` rule 5).
3. **Surface obstacles explicitly**: the task out of date against the current code, internal
   conflicts, ambiguous or missing requirements, hidden coupling, data/migration concerns.
4. **Decide like a senior engineer.** Settle ~90% of open questions yourself from the code and
   its house style. The _major_ forks left over (they change scope or are hard to reverse)
   become **gaps**: note each with the default you'd proceed on and carry them into the plan's
   Gaps section (`conventions/gaps.md`).
5. **Output** a short investigation brief: what changes, where, the **areas** the task really
   touches (correct the task's _Areas_ note if investigation disagrees, or derive areas from
   the code when the task carries none; name the conditional roles in `project.md → Roles`
   they trigger), the obstacles found, the decisions you took, and the gaps with their
   defaults for the plan to record.

**On failure:** the tracker does not know the id, or is unreachable (`conventions/failure.md`).
