# Step · implement - from a plan to working, verified code

Role: a **senior engineer** in the project's stack, or the base role the plan's `Roles` line records, implementing an approved plan.
Input: a plan: from the plan step just run, a saved `<storage root>/plans/<task>.md`, or a file the user passed.
Output: verified code in the working tree; later pipeline steps (`pr`, `push`, `review`, `deploy`) decide where it goes.
A spike's output is its spike report, its code left uncommitted, and no later step runs.
Context: `project.md` guardrails and Verify.

0. **Check the plan's gaps first**: open items are handled per `conventions/gaps.md` before any code.
1. **Implement the plan** in the project's house style: its CLAUDE.md rules and the guardrails in project.md / the implement overlay.
   Comments stay 1-2 lines.
   Keep PII out of logs.
   The plan's **Kind** line sets what comes first, while none of the plan's code exists yet (a later change on the same branch, such as a judge's, skips it):
   - **bug** - write the Reproduction's test and run it on the unfixed tree: it must fail, for the reason the plan names.
     A test that passes there did not reproduce the bug: stop (`conventions/failure.md`), never fix against it.
     Then the fix, and the same test green.
     A plan naming a manual check instead: run it where the session can (the CLI, a script), else list it for the user.
   - **refactor** - run the Invariant's tests, and any characterization tests the plan adds, green before the first change and after the last.
     An existing assertion the plan did not name stays as it is.
   - **spike** - run the experiments with the least code that produces their evidence: no polish, and tests only where an experiment needs one.
     Write the **spike report** to `<storage root>/spikes/<TASK-ID>.md`: the question, the answer, the evidence (commands run, numbers, files by path), the recommendation, and follow-up tasks worth capturing, a title each.
     The answer and recommendation read on their own: ship posts them on the task as a comment.
     The experiment code stays uncommitted in the worktree; the report names it by path.
     Verify still runs, and its result goes in the report, but red verify on throwaway code does not fail the step.
2. **Verify locally.**
   Run the project's verify commands from project.md, respecting its "never run X" rules.
   No Verify section: detect.
   `justfile` means `just lint` / `just test`; `Makefile`, its standard targets; else the stack's idiomatic verify commands (`go test ./...` + the linter the repo configures, etc.).
   Fix what you broke; a spike records red verify in its report instead (point 1).
   **The one skip: a tree that did not move.**
   If *this session* already ran these same commands green and nothing has touched the working tree since, say so and move on.
3. **Record the decisions** (`docs/lore.md`), with a task id, once verify is green.
   A decision is a fork the plan's stages left open, where another competent engineer could have gone the other way and the choice shapes code someone will edit.
   The kinds: a library or dependency over another, a data shape or file format, where a check or a responsibility lives, an algorithm or structure with a real tradeoff, a behavior under an edge the plan did not name.
   Not one: naming, formatting, a choice the plan already made (its stage tradeoffs are that record), a change with one sane option.
   Each: `f10 lore add decision --change <id> --anchor <path:line[-end][#symbol]> --chosen <option> --rejected 'option: reason' -m <why>`, `--rejected` once per option, anchored to the code it shaped.
   The anchor's commit defaults to HEAD, the base this uncommitted work sits on; `why` links it by ancestry until the commit that follows carries the task trailer.
   Most small changes record none, and a spike records none: its report holds its reasoning.
   A failing `lore add` is one line in the hand-off, never a failed step.
4. **Hand off.**
   Briefly note what changed, then continue with the next ship-pipeline step.
   A bug or refactor names its proof in one line: the test, red before and green after, or the manual check and its result.
   A spike names its report's path and the answer in one line.
   The decisions take one line: `decisions: none`, or the count and their anchors.

**On failure:** verify stays red and you cannot fix it (`conventions/failure.md`), except in a spike (point 1).
