# Step · plan - investigation → architect-grade plan

Role: a **senior software architect** in the project's stack - adopt the role from
`.f10/instructions/project.md` (e.g. "+ Ruby on Rails developer", "+ Go developer").
Precondition: the fetch step's brief (run `fetch.md` first if it hasn't been).
Context: load per `conventions/context.md` - guardrails and house style come from
`.f10/instructions/project.md` (+ `.f10/instructions/plan.md` overlay if present).

1. **Design a solid, conventional solution.** No hacky shortcuts, no reinventing the wheel.
   Reuse existing abstractions and honour the project's house style - its CLAUDE.md rules
   and the guardrails declared in project.md.
2. **Guardrails** - apply the project's declared guardrails to the design. Typical example: a
   UI component gallery / design system the project treats as canonical - design in terms of
   what already exists there, never invent a parallel component; the plan overlay spells out
   the specifics. No guardrails declared → skip.
3. **Produce a staged plan:** ordered steps, the critical files each touches, any
   data/migration work, the tests to add or adjust, and the key tradeoffs / alternatives
   considered. Keep it focused and incremental - narrow per-piece steps, not a multi-tier
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
     per-phase time budget - agents aren't paced like people and the numbers only spoil context.
   - **No process boilerplate.** Skip generic deployment checklists, rollout/monitoring sections,
     and speculative feature flags unless the task genuinely calls for one - then say so in one line.
   - **Right-size the phases.** A handful of ordered steps that fit the task. If you're at eight
     phases, you're writing an essay - collapse it. List real tradeoffs, not filler risk tables.
4. **Save the plan - this IS the deliverable, not optional polish.** Write it with the `Write`
   tool to the **relative** path **`.f10/plans/<TASK-ID>.md`** - using the task's exact tracker
   id, uppercased, in the project's id format (e.g. `.f10/plans/ABC-2049.md`,
   `.f10/plans/GH-141.md`). Create the `.f10/plans/` dir if missing; ensure `.f10/` is
   untracked per the project's visibility (stealth → `.git/info/exclude`, see `conventions/context.md`).
   Use the relative path from your **current working directory** - do NOT build an absolute
   path to a specific checkout. If you're in a git worktree, the plan must land in that
   worktree (where the branch is), not the main checkout. This exact relative path is the
   handoff contract `/f10:ship` reads, so do not vary it. If the task has no tracker id, use a
   short kebab slug (`.f10/plans/<slug>.md`).
   **Out-of-tree storage** (context resolution found the instructions at `~/.f10/<project>/` -
   see `conventions/context.md → Storage`): everything above applies with the storage root swapped in -
   write to `~/.f10/<project>/plans/<TASK-ID>.md`, archive to its `plans/archive/`, and write
   **nothing** inside the project directory (no `.f10/`, no exclude entry).
   A plan that exists only as a chat message is a **failed** run - finishing this step means the
   file is on disk.
   - **You, the main agent, write this file yourself.** Investigation subagents (Explore) are
     read-only and cannot save it - never leave the plan sitting only in a subagent's returned text.
   - **If a plan file already exists at that path, archive it - never edit it in place.** A clean
     rewrite beats diffing an old plan. `mkdir -p .f10/plans/archive/` and move the existing file
     to `.f10/plans/archive/<TASK-ID>.<N>.md`, where `<N>` is the next integer (1 if nothing
     archived yet, else max existing + 1). Then `Write` the fresh plan to the now-free
     `.f10/plans/<TASK-ID>.md`. Moving the old file away frees the path, so the new `Write`
     needs no prior `Read` - and a `Write` error is a real failure, never "already written."
   - **If you cannot write it (session is in Plan / read-only mode):** do NOT silently stop and
     offer a menu of options. Call `ExitPlanMode` to present the plan and get the go-ahead to save
     it (or, failing that, ask the user to exit plan mode with shift+tab), then write the file.
     The step is not done until the file exists.
5. **Confirm and hand off.** Verify the file exists, present the plan in chat, and report its
   path so `/f10:ship` - this agent or a fresh one - can pick it up: `.f10/plans/<TASK-ID>.md`.
   Then, if the plan has **open gaps**, list them by title (one line each) and **offer to fill them
   now** via a batched `AskUserQuestion` (see `conventions/gaps.md`) - an offer the user can decline; the plan
   ships on its defaults if they do. If they fill any, update the Gaps section + fold the decisions
   into the plan, and re-save the file.

Do not write implementation code in this step.

**On failure:** the plan file cannot be written for any reason other than read-only mode (which
step 4 already handles) - report the full plan prose in the message so the work survives, and say
which path it was meant to land on. See `conventions/failure.md`.
