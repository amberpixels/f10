# Step · plan - from investigation to an architect-grade plan

Role: a **senior software architect** in the project's stack, unless the fetch brief settled another base role, plus the conditional roles the fetch brief's areas trigger.
Input: the fetch step's investigation brief.
Context: `project.md` guardrails and house style.
Badge: `f10-state.sh set plan running` on entry, unless `fetch` already opened the phase. `f10-state.sh set plan done` once the plan file is on disk.

1. **Design a conventional solution.**
   Reuse the abstractions the code already has.
   Follow the project's CLAUDE.md and the guardrails `project.md` declares.
   For example, when the project names a canonical UI component gallery, design with its components and never build a parallel set.
2. **Write a staged plan.**
   The plan opens with a few **header lines**, one fact each, in this order:
   - **Roles** comes first.
     It records the roles the plan was written under, which are the `Role:` line above with the conditional roles added, so that `/f10:ship` runs the later steps under the same roles.
     Usually the line is one flat list.
     When `project.md → Roles` replaces a base role or scopes a role to some steps, the line names the roles for each group of steps, each with its source, such as `Roles: all steps: senior marketing analyst (replaces base, report work) + senior Python developer (framework work)`.
   - **Kind** follows, but only for a `bug`, a `refactor` or a `spike` (`Kind: bug`).
     A plan with no Kind line is a feature.
   - **Areas** follows when the frontmatter declares `flow_by_area`.
     It lists the areas the fetch brief settled, in the words the map uses.
     `/f10:ship` picks the flow from it when it runs on the plan alone.
   - **Depends on** follows when the task has a base task, which is when the steering names one (`f10 start --after`) or the task body ends in an `After:` line.
     It holds the base task's id and the path of its plan.
   - **Bundle** follows when the plan covers several tasks (`Bundle: ABC-1+ABC-2`).
     The first task is the lead, and the others are its riders.
     A bundled plan also has a `## Riders` section, which lists each rider's acceptance criteria under the rider's id.
     The stages cover every task in the bundle.
   - **Size** comes last.
     It holds the plan's size and the reason in a clause, such as `Size: small - one cause at <file:line>, one area`.
     How to choose the size comes after the full plan shape, below.

   A bug, a refactor and a spike each open with a section of their own, after the header lines and before the stages:
   - **bug** - `## Reproduction` holds the steps or input, the expected behavior, the actual behavior, the root cause at `file:line`, and the test that will fail before the fix.
     The first stage writes that test.
     Sometimes no automated test can reproduce the bug, as with a race, a third-party outage or UI timing.
     In that case the section says so and names a manual check instead.
   - **refactor** - `## Invariant` holds the behavior that stays the same and the tests that prove it.
     When coverage is missing, the first stage writes characterization tests, which pass before any change is made.
     An existing assertion that must change is a behavior change, so the plan names it and says why.
     Without that, the work is not a refactor.
   - **spike** - `## Question` holds the question, the decision its answer feeds, and the evidence that counts as an answer.
     A spike's stages are experiments, not features to build, and each one names the evidence it produces.
     A spike's Consequences section states what the likely recommendation would commit the project to.

   Then come the ordered **stages**.
   Each stage names the critical files it touches, any data or migration work, and the tests to add or adjust.
   The stages also weigh the main tradeoffs and the alternatives.

   Then comes **`## Consequences`**, a few lines of prose in the sense of an architecture decision record (ADR).
   It says what the chosen design makes easier, what it makes harder, the constraint the design now depends on, and what to revisit under which condition.
   For a bug, it also names the related failures this fix leaves uncovered, if there are any.
   When no trade-off is worth naming, write `## Consequences - none`.

   The last section is **`## Gaps`**, in the shape `conventions/gaps.md` gives, or `## Gaps - none`.
   The hand-off (point 4) offers to fill the open gaps.

   Each of these sections holds something different.
   The stage tradeoffs weigh the alternatives.
   Consequences records what the chosen alternative costs.
   Gaps holds what is still undecided.
   Rollout and monitoring stay out of the plan (**Altitude**, below).

   **Size the plan** by how much design the implementer still has to do, the same way capture sizes a task body.
   Most plans are normal, which means the full shape above.

   A plan is small when the work is already decided, which means all of these are true:
   - You know where the change goes.
     For a feature, the task says so.
     For a bug or a refactor, the task's _Root cause_, this run's debug brief, or a single `file:line` from the fetch brief names it.
   - It touches one area.
   - No other competent engineer would make a different call on it.
   - There are no gaps.

   If you are unsure about any of them, the plan is normal.
   A spike is never small, because its `## Question` has to spell out the decision and the evidence.
   Neither is a refactor without test coverage, because writing the characterization tests is a stage of its own.

   A small plan keeps the header lines and cuts the rest down to the brief in a few lines, one stage that names the change, its files and its test, then `## Consequences - none` and `## Gaps - none`.
   For a bug, the `## Reproduction` section shrinks to the root cause at `file:line`, the expected behavior against the actual one, and the failing test.
   A refactor's `## Invariant` stays as written.

   A plan is large when it bundles tasks, cuts across the codebase, or has a boundary worth fencing off.
   It has the normal shape, with the stages arranged around that boundary.

   Once the plan is saved, its size changes only when a judge's change is folded into it.
   That change rewrites the `Size:` line and adds the sections a normal plan has.
   A decision the implementer meets while building goes in the implement step's hand-off, as it does for any plan, and does not resize the plan.

   **Altitude: write as a senior engineer who knows this codebase, not as a code generator.**
   - **Prose, no code.**
     Name the file, type and function, and say *what* changes and *why*.
     Use a signature or a one-line pseudocode sketch at most, and only when words leave the intent unclear.
   - **No effort estimates.**
   - **No process boilerplate**, such as deployment checklists, rollout or monitoring sections, or speculative feature flags.
     Include it only when the task calls for it, and then in one line.
   - **A handful of stages.**
     A small plan has one.
     When a plan reaches eight, collapse them.
     The tradeoffs are real ones, not filler risk tables.
3. **Save the plan.**
   The plan file is this step's deliverable.
   `Write` it to **`<storage root>/plans/<TASK-ID>.md`**, because `/f10:ship` reads this exact path.
   Take the root verbatim from the load's `storage root:` line.
   `<TASK-ID>` is the exact tracker id, uppercased, as in `<storage root>/plans/ABC-2049.md`.
   A bundle uses its lead's id.
   A plan with no task id uses a short kebab slug instead.
   Create `plans/` if it is missing.
   With in-repo storage under stealth, keep `.f10/` listed in the exclude file that `git rev-parse --git-path info/exclude` names.
   With **out-of-tree storage** (`~/.f10/<project>/`), write **nothing** inside the project directory.
   - **The main agent writes the file**, not a subagent.
   - **An existing plan is archived, never edited.**
     Run `mkdir -p <storage root>/plans/archive/` and move the old plan to `archive/<TASK-ID>.<N>.md`, where `<N>` is the next integer.
     Then `Write` the new plan, with no `Read` first.
     A `Write` error is a real failure, never a sign that the file is "already written".
   - **In plan mode or read-only mode, never stop silently.**
     Call `ExitPlanMode` to present the plan and get the go-ahead, or ask the user to leave plan mode with shift+tab.
     Then write the file.
   - **Lore** (`docs/lore.md`): once the file is on disk, a plan with a task id records its intent with `f10 lore add intent --change <id> -m "<text>"`, where a bundle's id is its lead's.
     A plan with no task id writes no intent.
     The body is two to four sentences on what the change does and why, for someone reading it beside the code later.
     It gives the domain reason, with no plan, stage or f10 vocabulary.
     A spike's intent states the question and what its answer decides.
     A replan is a run that archived a plan above.
     A replan supersedes the standing intent with `--supersedes <id>`, where `<id>` is the id that `f10 lore show <task-id> --json` lists for the `intent` entry.
     A judge revising the plan file in place is not a replan.
     A second intent without `--supersedes` is refused, and the refusal names the standing one.
     A failing `lore add` is one line in the hand-off, never a failed step.
4. **Hand off.**
   Confirm the file exists and present the plan.
   Report per `conventions/report.md`, with the rows `task`, `title`, `url` when the tracker gave one, and `plan`.
   Under the facts block, name the size and its reason in one line.
   Then offer to fill the open gaps (`conventions/gaps.md`).

**On failure:** when the file cannot be written for any reason other than read-only mode, put the full plan in the message, with the path it was meant for.
