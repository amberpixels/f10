# Step · investigate - what a fetched task needs, before it is planned

Input: the task the fetch step read, with its base task and its stated kind.
Output: the **investigation brief** the plan step designs from.
On a planless flow, it is the inline plan `/f10:ship` prints.

It runs only when a plan will be written, so a run that reuses a saved plan skips it.
When the frontmatter declares `stale_after`, run the **stale** step first: `f10-load-project.sh stale`.

1. **Settle the kind**: `feature`, `bug`, `refactor` or `spike`.
   A kind the fetch step passed on wins.
   Otherwise the description decides: an error, a trace or "used to work" is a bug, and "no behavior change" is a refactor.
   The brief then names the description as the source, so the user can overrule it.
   The description never makes a spike, because a spike skips the PR.
   A task that only asks a question stays a feature, and the brief says it reads like a spike.
   Otherwise it is a feature.
   A bundle has one kind, a bug when any member is one, and the areas of all its members.
2. **Investigate only the real unknowns.**
   The task text and what this conversation settled are ground truth, except a bug's reported behavior.
   Read only what the design needs: the code you will touch or mirror, its callers and consumers, and its tests.
   - **bug**: run the **debug** step on the task, then its **Attribute**: `f10-state.sh set plan running debug; f10-load-project.sh debug`.
     With `debug: off`, trace the root cause from the code, and the brief says the pass was off.
     When this run already holds a debug brief (`/f10:debug`), check that its `file:line` still reads as it says.
     Then carry it over and run only **Attribute**.
     The steering `as a feature` makes it a feature with no pass, the opt-out for a "bug" label on a missing feature.
   - **refactor**: find the behavior that must not change, the tests that pin it, and where coverage is missing.
   - **spike**: state the question so an answer can be checked, the decision each answer feeds, and the cheapest experiment that answers it.
     A question that cannot be stated that way is a gap.
3. **Surface obstacles**: a task out of date against the code, conflicting or missing requirements, hidden coupling, data or migrations.
4. **Decide like a senior engineer.**
   Settle about 90% of the open questions from the code and its house style.
   A fork that changes scope or is hard to reverse becomes a **gap**, with the default you would proceed on (`conventions/gaps.md`).
5. **Write the brief**, short:
   - the kind, with a bug's reproduction and root cause, a refactor's invariant, or a spike's question
   - the base task, when there is one
   - what changes, and where
   - the **areas** the task touches, correcting or deriving its _Areas_ note, and the roles in `project.md → Roles` they trigger
   - the base role each step runs under where Roles or an overlay replaces it, and what set it (`conventions/context.md` → Roles)
   - the obstacles, the decisions taken, and the gaps with their defaults
