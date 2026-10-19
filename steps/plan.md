# Step · plan - from investigation to an architect-grade plan

Role: a **senior software architect** in the project's stack, plus the conditional roles the fetch brief's areas trigger.
Input: the fetch step's investigation brief.
Context: `project.md` guardrails and house style.
Badge: `f10-state.sh set plan running` on entry if `fetch` did not open the phase; `f10-state.sh set plan done` once the plan file is on disk.

1. **Design a conventional solution**: reuse existing abstractions, follow the project's CLAUDE.md and declared guardrails (e.g. a canonical UI component gallery means designing with its components, never a parallel one).
2. **Write a staged plan.**
   First line: **Roles**, the roles it was written under, for `/f10:ship` to inherit.
   If the steering names a base task (`f10 start --after`) or the task body ends in an `After:` line, a **Depends on** line follows: the task id and its plan's path.
   Then ordered **stages**, the critical files each touches, data/migration work, tests to add or adjust, key tradeoffs and alternatives.
   Then a **`## Consequences` section**, a few lines of prose, in the ADR sense: what the chosen design makes easier, what it makes harder, the constraint it now depends on, and what to revisit under which condition.
   No trade-off worth naming: `## Consequences - none`.
   Stage tradeoffs weigh the alternatives; Consequences records what the chosen one costs, Gaps what is still undecided, and rollout or monitoring stays out (below).
   Last, a **`## Gaps` section** (`conventions/gaps.md`), or `## Gaps - none`.

   **Altitude: a senior engineer who knows this codebase, not a code generator.**
   - **Prose, no code.**
     Name the file, type and function and say *what* changes and *why*.
     A signature or one-line pseudocode sketch at most, only when words leave the intent unclear.
   - **No effort estimates.**
   - **No process boilerplate** (deployment checklists, rollout or monitoring sections, speculative feature flags) unless the task calls for it, said in one line.
   - **A handful of stages**; at eight, collapse.
     Real tradeoffs, not filler risk tables.
3. **Save the plan: it is the deliverable.**
   `Write` it to **`<storage root>/plans/<TASK-ID>.md`**, the root verbatim from `storage root:`, `<TASK-ID>` the exact tracker id, uppercased (e.g. `<storage root>/plans/ABC-2049.md`), or a short kebab slug without one.
   `/f10:ship` reads this exact path.
   Create `plans/` if missing; in-repo under stealth, keep `.f10/` in the file `git rev-parse --git-path info/exclude` names.
   **Out-of-tree storage** (`~/.f10/<project>/`): write **nothing** inside the project directory.
   - **The main agent writes it**, not a subagent.
   - **Archive an existing plan, never edit it**: `mkdir -p <storage root>/plans/archive/`, move it to `archive/<TASK-ID>.<N>.md` (`<N>` = next integer), then `Write` the new one (no `Read` first; a `Write` error is a real failure, never "already written").
   - **Plan / read-only mode:** never stop silently.
     Call `ExitPlanMode` to present the plan and get the go-ahead (or ask the user to exit plan mode with shift+tab), then write the file.
4. **Hand off.**
   Confirm the file exists, present the plan, and report per `conventions/report.md`: `task`, `title`, `url` if the tracker gave one, `plan`.
   Offer to fill open gaps (`conventions/gaps.md`).

**On failure:** the file cannot be written (other than read-only mode): put the full plan in the message with the path it was meant for.
