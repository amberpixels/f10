# Step · plan - investigation → architect-grade plan

Role: a **senior software architect** in the project's stack - adopt the roles from
`project.md → Roles`, including any conditional ones the areas in the fetch brief trigger.
Input: the fetch step's investigation brief (run `steps/fetch.md` first if it hasn't been).
Context: per `conventions/context.md` - guardrails and house style come from `project.md`
(+ same-named overlay).
Badge (`conventions/report.md`): `f10-state.sh set plan running` on entry if `fetch` did not open
the phase; `f10-state.sh set plan done` once the plan file is on disk.

1. **Design a solid, conventional solution.** No hacky shortcuts, no reinventing the wheel.
   Reuse existing abstractions and honour the project's house style - its CLAUDE.md rules
   and the guardrails declared in project.md.
2. **Guardrails** - apply the project's declared guardrails to the design. Typical example: a
   UI component gallery the project treats as canonical - design in terms of what exists
   there, never invent a parallel component. No guardrails declared → skip.
3. **Produce a staged plan.** Open it with a one-line **Roles** note recording the roles this
   plan was written under (so `/f10:ship` inherits them instead of re-deriving). Then:
   ordered **stages**, the critical files each touches, any data/migration work, the tests to
   add or adjust, and the key tradeoffs / alternatives considered. End with a
   **`## Gaps` section** capturing the real forks that need the
   user's call, each with the default the plan proceeds on (see `conventions/gaps.md`); no
   real forks → `## Gaps - none`.

   **Altitude - write for a senior engineer who knows this codebase, not a code generator:**
   - **Describe the change in prose; do NOT paste code.** Name the file, class/type, and
     function you'll touch and say *what* changes and *why*. No code bodies, no diff blocks -
     a signature or a one-line pseudocode sketch is the ceiling, and only when the intent
     isn't clear from words.
   - **No effort or time estimates.** No human-hours, story points, or per-stage time
     budgets - agents aren't paced like people and the numbers only spoil context.
   - **No process boilerplate.** Skip generic deployment checklists, rollout/monitoring
     sections, and speculative feature flags unless the task genuinely calls for one - then
     say so in one line.
   - **Right-size the stages.** A handful of ordered stages that fit the task; at eight
     you're writing an essay - collapse it. List real tradeoffs, not filler risk tables.
4. **Save the plan - this IS the deliverable, not optional polish.** Write it with the `Write`
   tool to **`<storage root>/plans/<TASK-ID>.md`** - `<storage root>` taken verbatim from the
   `storage root:` line context resolution reported (never re-derive it from your cwd - see
   `conventions/context.md`), `<TASK-ID>` the task's exact tracker id, uppercased, in the
   project's id format (e.g. `<storage root>/plans/ABC-2049.md`); no tracker id → a short
   kebab slug. This exact path is the handoff contract `/f10:ship` reads, so do not vary it.
   Create the `plans/` dir if missing; for in-repo storage, ensure `.f10/` is untracked per
   the project's visibility (stealth → the file `git rev-parse --git-path info/exclude`
   names). **Out-of-tree storage** (the reported root is `~/.f10/<project>/`): everything
   above applies, plus write **nothing** inside the project directory - no `.f10/`, no
   exclude entry.
   A plan that exists only as a chat message is a **failed** run - finishing this step means
   the file is on disk.
   - **You, the main agent, write this file yourself.** Investigation subagents (Explore) are
     read-only - never leave the plan sitting only in a subagent's returned text.
   - **A plan file already at that path is archived, never edited in place** - a clean rewrite
     beats diffing an old plan. `mkdir -p <storage root>/plans/archive/`, move the existing
     file to `archive/<TASK-ID>.<N>.md` (`<N>` = next integer), then `Write` the fresh plan to
     the now-free path (no prior `Read` needed - a `Write` error is a real failure, never
     "already written").
   - **If you cannot write it (session is in Plan / read-only mode):** do NOT silently stop.
     Call `ExitPlanMode` to present the plan and get the go-ahead (or ask the user to exit
     plan mode with shift+tab), then write the file. The step is not done until the file
     exists.
5. **Confirm and hand off.** Verify the file exists, present the plan in chat, and report per
   `conventions/report.md` - a `task` row, a `url` row if the tracker gave one, and a `plan`
   row holding the saved path so `/f10:ship`, this agent or a fresh one, can pick it up.
   Then, if the plan has **open gaps**, offer to fill them per `conventions/gaps.md`.

Do not write implementation code in this step.

**On failure:** the plan file cannot be written for any reason other than read-only mode (step 4
handles that) - report the full plan prose in the message so the work survives, and say which
path it was meant to land on. See `conventions/failure.md`.
