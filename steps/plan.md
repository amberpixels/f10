# Step · plan - from investigation to an architect-grade plan

Role: a **senior software architect** in the project's stack, or the base role the fetch brief settled, plus the conditional roles the fetch brief's areas trigger.
Input: the fetch step's investigation brief.
Context: `project.md` guardrails and house style.
Badge: `f10-state.sh set plan running` on entry if `fetch` did not open the phase; `f10-state.sh set plan done` once the plan file is on disk.

1. **Design a conventional solution**: reuse existing abstractions, follow the project's CLAUDE.md and declared guardrails (e.g. a canonical UI component gallery means designing with its components, never a parallel one).
2. **Write a staged plan.**
   First line: **Roles**, the roles it was written under, for `/f10:ship` to inherit.
   Where Roles replaces a base role or scopes a role to steps, the line names the roles per step group, each with its source (`Roles: all steps: senior marketing analyst (replaces base, report work) + senior Python developer (framework work)`); otherwise one flat list.
   A **Kind** line follows for a `bug`, `refactor` or `spike` (`Kind: bug`); a plan without one is a feature.
   Where the frontmatter declares `flow_by_area`, an **Areas** line follows: the areas the fetch brief settled, in the map's words, for `/f10:ship` to pick the flow from when it runs on the plan alone.
   If the steering names a base task (`f10 start --after`) or the task body ends in an `After:` line, a **Depends on** line follows: the task id and its plan's path.
   A bundle adds a **Bundle** line (`Bundle: ABC-1+ABC-2`) and a `## Riders` section listing each rider's acceptance criteria under its id; the stages cover every member.
   A **Size** line follows: the size below and its reason in a clause (`Size: small - one cause at <file:line>, one area`).

   **Size the plan by what the implementer needs to design**, as capture sizes a task body:
   - **small** - every condition holds: the cause or the one place to change is already named (a feature's in its task; a bug's or a refactor's in the task's _Root cause_, this run's debug brief, or one `file:line` the fetch brief traced); one area; no decision another competent engineer would take the other way; no gap.
     The plan is the fixed lines above, the brief in a few lines, one stage naming the change, its files and its test, then `## Consequences - none` and `## Gaps - none`.
     A bug's `## Reproduction` shrinks to the root cause at `file:line`, expected against actual, and the failing test; a refactor's `## Invariant` stays as written.
   - **normal** - any small condition missed, doubt counting as a miss: the full shape below.
   - **large** - a bundle, a cross-cutting change, or a boundary worth fencing: normal's shape, the stages fencing the boundary.
   A spike is never small: its `## Question` needs the decision and the evidence; nor is a refactor whose coverage is missing, since its characterization tests are a stage of their own.
   A saved plan is resized only by a judge's change folded into it, which rewrites the `Size:` line and writes normal's sections; a decision implement meets goes in its hand-off, as for any plan.

   The kind opens the plan, before the stages:
   - **bug** - a `## Reproduction` section: the steps or input, expected, actual, the root cause at `file:line`, and the test that will fail before the fix.
     The first stage writes that test.
     Where no automated test can reproduce it (a race, a third-party outage, UI timing), the section says so and names the manual check instead.
   - **refactor** - a `## Invariant` section: the behavior that stays the same and the tests that prove it.
     Missing coverage makes the first stage characterization tests that pass before any change.
     An existing assertion that must change is a behavior change: name it and why, or the work is not a refactor.
   - **spike** - a `## Question` section: the question, the decision it feeds, and the evidence that counts as an answer.
     Stages are experiments, each naming the evidence it produces, not features to build.
     Consequences states what the likely recommendation would commit the project to.

   Then ordered **stages**, the critical files each touches, data/migration work, tests to add or adjust, the main tradeoffs and alternatives.
   Then a **`## Consequences` section**, a few lines of prose, in the ADR sense: what the chosen design makes easier, what it makes harder, the constraint it now depends on, and what to revisit under which condition.
   For a bug, also the related failures this fix leaves uncovered, when there are any.
   No trade-off worth naming: `## Consequences - none`.
   Stage tradeoffs weigh the alternatives; Consequences records what the chosen one costs; Gaps holds what is still undecided; rollout and monitoring stay out (below).
   Last, a **`## Gaps` section** (`conventions/gaps.md`), or `## Gaps - none`.

   **Altitude: a senior engineer who knows this codebase, not a code generator.**
   - **Prose, no code.**
     Name the file, type and function and say *what* changes and *why*.
     A signature or one-line pseudocode sketch at most, only when words leave the intent unclear.
   - **No effort estimates.**
   - **No process boilerplate** (deployment checklists, rollout or monitoring sections, speculative feature flags) unless the task calls for it, said in one line.
   - **A handful of stages**, one on a small plan; at eight, collapse.
     Real tradeoffs, not filler risk tables.
3. **Save the plan: it is the deliverable.**
   `Write` it to **`<storage root>/plans/<TASK-ID>.md`**, the root verbatim from `storage root:`, `<TASK-ID>` the exact tracker id (a bundle's lead), uppercased (e.g. `<storage root>/plans/ABC-2049.md`), or a short kebab slug without one.
   `/f10:ship` reads this exact path.
   Create `plans/` if missing; in-repo under stealth, keep `.f10/` in the file `git rev-parse --git-path info/exclude` names.
   **Out-of-tree storage** (`~/.f10/<project>/`): write **nothing** inside the project directory.
   - **The main agent writes it**, not a subagent.
   - **Archive an existing plan, never edit it**: `mkdir -p <storage root>/plans/archive/`, move it to `archive/<TASK-ID>.<N>.md` (`<N>` = next integer), then `Write` the new one (no `Read` first; a `Write` error is a real failure, never "already written").
   - **Plan / read-only mode:** never stop silently.
     Call `ExitPlanMode` to present the plan and get the go-ahead (or ask the user to exit plan mode with shift+tab), then write the file.
   - **Lore** (`docs/lore.md`), with a task id (a bundle's lead) and the file on disk: `f10 lore add intent --change <id> -m "<text>"`.
     The body is two to four sentences on what the change does and why, for someone reading it beside the code later: the domain reason, in no plan, stage or f10 vocabulary.
     A spike's intent states the question and what its answer decides.
     A replan (a plan was archived above) supersedes the standing intent with `--supersedes <id>`, the id of the `intent` entry `f10 lore show <id> --json` lists; a judge's revision of the plan file in place is not a replan.
     A second intent without `--supersedes` is refused, naming the standing one.
     A failing `lore add` is one line in the hand-off, never a failed step; a plan with no task id writes none.
4. **Hand off.**
   Confirm the file exists, present the plan, and report per `conventions/report.md`: `task`, `title`, `url` if the tracker gave one, `plan`.
   The prose under the facts block names the size and its reason in one line.
   Offer to fill open gaps (`conventions/gaps.md`).

**On failure:** the file cannot be written (other than read-only mode): put the full plan in the message with the path it was meant for.
