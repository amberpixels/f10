# Step · fetch - understand and investigate a task

Role: a **senior engineer** doing due diligence before touching any code.
Input: a task identifier (in the project's id format).
Context: `project.md → Tracker` (the **fetch adapter**).
Badge: `f10-state.sh set plan running` on entry, `f10-state.sh task <id> <url>` once fetched.

1. **Fetch the task**: `f10 task read <id>`; when it cannot read this tracker it says so, and the project's fetch adapter reads the task instead (a skill to invoke, or a CLI command such as `gh issue view <n> --comments`).
   A base task, named by the steering (`f10 start --after`) or a body line `After: <id>`, goes into the brief: the plan step records it as the plan's **Depends on** line and designs against the base task's plan (its task, where the plan is not written yet), never this checkout, with no fallback for its absence.
2. **Settle the kind**: `feature`, `bug` or `refactor`.
   The steering's word wins (`/f10:plan FT-12 as a bug`), then a tracker label or type that names one (`bug`, `defect`, `regression` are a bug; `refactor`, `tech-debt` a refactor; other labels say nothing), then the description (an error, a stack trace, "used to work" read as a bug; "no behavior change", "restructure" as a refactor), else `feature`.
   A kind read from the description alone is named as such in the brief, so a reader can overrule it.
3. **Investigate only the real unknowns.**
   The task text and anything established in this conversation are **ground truth**: never re-verify a premise the task or the user gave you.
   Investigate only what you need to design well: the exact code you'll mirror or touch, existing patterns to reuse, callers, downstream consumers behind a real decision, and tests, in a few targeted reads (`conventions/latency.md` rule 5).
   The kind adds what it must prove:
   - **bug** - reproduce it where the project can run it (a test, the CLI, the verify commands): the steps or input, expected against actual.
     Then trace the root cause to a `file:line`.
     A bug that does not reproduce, or a cause still a guess after a few reads, never becomes a plan: ask what the user saw, under which input and version, in one `AskUserQuestion` (driven: one ask, `set plan blocked`, per `modes/driven.md`), and stop.
   - **refactor** - the behavior that must not change, the tests that pin it, and where coverage is missing.
4. **Surface obstacles explicitly**: the task out of date against the current code, internal conflicts, ambiguous or missing requirements, hidden coupling, data/migration concerns.
5. **Decide like a senior engineer.**
   Settle ~90% of open questions yourself from the code and its house style.
   The _major_ forks left over (they change scope or are hard to reverse) become **gaps**: note each with the default you'd proceed on and carry them into the plan's Gaps section (`conventions/gaps.md`).
6. **Output** a short investigation brief: the kind (with a bug's reproduction and root cause, or a refactor's invariant), what changes, where, the **areas** the task really touches (correct the task's _Areas_ note if investigation disagrees, or derive areas from the code when the task carries none; name the conditional roles in `project.md → Roles` they trigger), the obstacles found, the decisions you took, and the gaps with their defaults for the plan to record.

**On failure:** the tracker does not know the id, or is unreachable (`conventions/failure.md`).
A bug that does not reproduce is a declined outcome, not a failure.
