# Step · plan - investigation → architect-grade plan

Role: a **senior software architect** in the project's stack - adopt the roles from
`project.md → Roles` (e.g. "+ Go developer"), including any conditional ones the areas in the
fetch brief trigger.
Input: the fetch step's investigation brief (run `steps/fetch.md` first if it hasn't been).
Context: load per `conventions/context.md` - guardrails and house style come from
`.f10/instructions/project.md` (+ `.f10/instructions/plan.md` overlay if present).

1. **Design a solid, conventional solution.** No hacky shortcuts, no reinventing the wheel.
   Reuse existing abstractions and honour the project's house style - its CLAUDE.md rules
   and the guardrails declared in project.md.
2. **Guardrails** - apply the project's declared guardrails to the design. Typical example: a
   UI component gallery / design system the project treats as canonical - design in terms of
   what already exists there, never invent a parallel component; the plan overlay spells out
   the specifics. No guardrails declared → skip.
3. **Produce a staged plan.** Open it with a one-line **Roles** note recording the roles this
   plan was written under, so a fresh `/f10:ship` agent adopts the same ones instead of
   re-deriving them. Then: ordered **stages**, the critical files each touches, any
   data/migration work, the tests to add or adjust, and the key tradeoffs / alternatives
   considered. Keep it focused and incremental - narrow per-piece stages, not a multi-tier
   strategy essay. End the plan with a **`## Gaps` section** capturing the real forks that need the
   user's call, each with the default the plan proceeds on (see `conventions/gaps.md`). No real forks →
   `## Gaps - none`.

   **Altitude - write for a senior engineer who knows this codebase, not a code generator:**
   - **Describe the change in prose; do NOT paste code.** Name the file, class/type, and
     function you'll touch and say *what* changes and *why* (e.g. "add
     `Account#effective_tags` that unions own + parent tags; call it from the projects
     controller's distinct-tags helper"). No code bodies in any language, no diff blocks, no
     snippets - a signature or a one-line pseudocode sketch is the ceiling, and only when the
     intent isn't clear from words.
   - **No effort or time estimates.** Never include human-hours, "~13-18h", story points, or a
     per-stage time budget - agents aren't paced like people and the numbers only spoil context.
   - **No process boilerplate.** Skip generic deployment checklists, rollout/monitoring sections,
     and speculative feature flags unless the task genuinely calls for one - then say so in one line.
   - **Right-size the stages.** A handful of ordered stages that fit the task. If you're at eight
     stages, you're writing an essay - collapse it. List real tradeoffs, not filler risk tables.
4. **Save the plan - this IS the deliverable, not optional polish.** Write it with the `Write`
   tool to **`<storage root>/plans/<TASK-ID>.md`**, where `<storage root>` is the path context
   resolution reported on its `storage root:` line (see `conventions/context.md`) and `<TASK-ID>`
   is the task's exact tracker id, uppercased, in the project's id format (e.g.
   `<storage root>/plans/ABC-2049.md`, `<storage root>/plans/GH-141.md`). Create the `plans/`
   dir if missing; for in-repo storage, ensure `.f10/` is untracked per the project's visibility
   (stealth → the file `git rev-parse --git-path info/exclude` names, which resolves correctly
   from a subdirectory and from a linked worktree, see `conventions/context.md`).
   Take the reported root verbatim - do NOT re-derive one from your current working directory.
   It is already anchored to the root of the checkout you are in, so a plan written from a
   worktree lands in that worktree (where the branch is) and one written from a subdirectory
   lands at the checkout root, not beside you. This exact path is the handoff contract
   `/f10:ship` reads, so do not vary it. If the task has no tracker id, use a short kebab slug
   (`<storage root>/plans/<slug>.md`).
   **Out-of-tree storage** (the reported root is `~/.f10/<project>/` - see
   `conventions/context.md → Storage`): everything above applies unchanged, plus write
   **nothing** inside the project directory (no `.f10/`, no exclude entry).
   A plan that exists only as a chat message is a **failed** run - finishing this step means the
   file is on disk.
   - **You, the main agent, write this file yourself.** Investigation subagents (Explore) are
     read-only and cannot save it - never leave the plan sitting only in a subagent's returned text.
   - **If a plan file already exists at that path, archive it - never edit it in place.** A clean
     rewrite beats diffing an old plan. `mkdir -p <storage root>/plans/archive/` and move the
     existing file to `<storage root>/plans/archive/<TASK-ID>.<N>.md`, where `<N>` is the next
     integer (1 if nothing archived yet, else max existing + 1). Then `Write` the fresh plan to
     the now-free `<storage root>/plans/<TASK-ID>.md`. Moving the old file away frees the path, so
     the new `Write` needs no prior `Read` - and a `Write` error is a real failure, never
     "already written."
   - **If you cannot write it (session is in Plan / read-only mode):** do NOT silently stop and
     offer a menu of options. Call `ExitPlanMode` to present the plan and get the go-ahead to save
     it (or, failing that, ask the user to exit plan mode with shift+tab), then write the file.
     The step is not done until the file exists.
5. **Confirm and hand off.** Verify the file exists, present the plan in chat, and report per
   `conventions/report.md` - a `task` row, a `url` row if the tracker gave one, and a `plan` row
   holding `<storage root>/plans/<TASK-ID>.md` so `/f10:ship`, this agent or a fresh one, can
   pick it up.
   Then, if the plan has **open gaps**, list them by title (one line each) and offer to fill them
   now per `conventions/gaps.md`.

Do not write implementation code in this step.

**On failure:** the plan file cannot be written for any reason other than read-only mode (which
step 4 already handles) - report the full plan prose in the message so the work survives, and say
which path it was meant to land on. See `conventions/failure.md`.
