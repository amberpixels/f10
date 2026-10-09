# Step · plan - from investigation to an architect-grade plan

Role: a **senior software architect** in the project's stack, plus the conditional roles the
fetch brief's areas trigger.
Input: the fetch step's investigation brief (run `steps/fetch.md` first if it hasn't been).
Context: `project.md` guardrails and house style.
Badge: `f10-state.sh set plan running` on entry if `fetch` did not open the phase;
`f10-state.sh set plan done` once the plan file is on disk.

1. **Design a conventional solution**: no hacky shortcuts, no reinvented wheels; reuse existing
   abstractions, follow the project's CLAUDE.md and declared guardrails (e.g. a canonical UI
   component gallery means designing with its components, never a parallel one).
2. **Write a staged plan.** First line: **Roles**, the roles it was written under, for
   `/f10:ship` to inherit. If the steering names a base task (`f10 start --after`) or the task
   body ends in an `After:` line, a **Depends on** line follows: the task id and its plan's path.
   Then ordered **stages**, the critical files each touches, data/migration work, tests to add or
   adjust, key tradeoffs and alternatives. Last, a **`## Gaps` section** (`conventions/gaps.md`),
   or `## Gaps - none`.

   **Altitude: a senior engineer who knows this codebase, not a code generator.**
   - **Prose, no code.** Name the file, type and function and say *what* changes and *why*. A
     signature or one-line pseudocode sketch at most, only when words leave the intent unclear.
   - **No effort or time estimates**: no hours, story points, or per-stage budgets.
   - **No process boilerplate**: no generic deployment checklists, rollout/monitoring sections
     or speculative feature flags unless the task calls for one; then say so in one line.
   - **A handful of stages**; at eight, collapse. Real tradeoffs, not filler risk tables.
3. **Save the plan: it is the deliverable.** `Write` it to **`<storage root>/plans/<TASK-ID>.md`**,
   the root verbatim from `storage root:`, `<TASK-ID>` the exact tracker id, uppercased (e.g.
   `<storage root>/plans/ABC-2049.md`), or a short kebab slug without one. `/f10:ship` reads
   this exact path. Create `plans/` if missing; in-repo under stealth, keep `.f10/` in the file
   `git rev-parse --git-path info/exclude` names. **Out-of-tree storage** (`~/.f10/<project>/`):
   write **nothing** inside the project directory. A plan only in chat is a **failed** run.
   - **The main agent writes it**, never leaving it in an Explore subagent's returned text.
   - **Archive an existing plan, never edit it**: `mkdir -p <storage root>/plans/archive/`,
     move it to `archive/<TASK-ID>.<N>.md` (`<N>` = next integer), then `Write` the new one (no
     `Read` first; a `Write` error is a real failure, never "already written").
   - **Plan / read-only mode:** never stop silently. Call `ExitPlanMode` to present the plan and
     get the go-ahead (or ask the user to exit plan mode with shift+tab), then write the file.
4. **Hand off.** Confirm the file exists, present the plan, and report per
   `conventions/report.md`: `task`, `url` if the tracker gave one, `plan`. Offer to fill open
   gaps (`conventions/gaps.md`).

No implementation code in this step.

**On failure:** the file cannot be written (other than read-only mode): put the full plan in the
message with the path it was meant for.
