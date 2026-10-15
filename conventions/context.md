# Convention · context - per-project instructions

f10 is **generic**.
Everything project-specific (tracker, stack, roles, verify commands, PR flow, review flow, domain guardrails) lives in the project under **`.f10/instructions/`**.
Every f10 run starts by loading it.

## Vocabulary

- **skill** - an entry point the user invokes (`/f10:*`).
  `/f10:start`, `/f10:drive`, `/f10:finish` and `/f10:status` run the binary and no step.
- **step** - a unit of work in `steps/*.md`: `brainstorm`, `capture`, `fetch`, `plan`, `judge`, `explain`, `catchup`, `implement`, `commit`, `pr`, `push`, `review`, `resolve`, `demo`, `deploy`, `standup`.
  A skill runs one or more.
  A skill owns the invocation (argument, modes, authorization, where the session stops); a step owns the work.
  Where they disagree, the skill wins on the invocation and the step on the work.
- **stage** - one ordered unit *inside* a plan.
  Never a step or a skill.
- **task id** - the tracker id, in the project's format.
  It names the plan file, the branch and the report's rows.
  A planless pipeline has none, and no step may invent one.
- **plan** - `<storage root>/plans/<TASK-ID>.md`.
  Bare `plan` is ambiguous: write `/f10:plan` for the skill, the **plan** step for the step.
  "Run the **plan** step" means execute its instructions, never re-invoke the skill.
- **findings** - `<storage root>/reviews/<TASK-ID>/<round>.md` (`steps/review.md`).
- **shipment** - what the ship pipeline leaves, named by its **last step**: verified code (`implement`), local commits (`commit`), pushed commits (`push`), an open PR/MR (`pr`), a running environment (`deploy`), or a project-defined step's output.

**Step headers.**
A step's `Role:` is its base role; add the project's `project.md → Roles` on top, including conditional roles the plan recorded.
`Context:` names the `project.md` sections the step reads; its same-named overlay (`.f10/instructions/<step>.md`) extends it.

## Reading the project load

`f10-load-project.sh` prints the project's instructions, layered, the inference probes, the generic steps the skill named, and each layer's overlays for them.
Steps come only from it, never from the plugin's `steps/` read by hand.
It concatenates; you interpret `project.md` and bind adapters.
If it is ever unavailable, the points below say what to do by hand:

1. **`storage root:`** is absolute, anchored to the checkout root (`git rev-parse --show-toplevel`), never the cwd.
   Use it verbatim, never re-derive it.
   The project facts file is `<storage root>/instructions/project.md`.
2. **Layers.**
   The main checkout (first `git worktree list` path) and a linked worktree can each hold `.f10/instructions/`; **main's prints first, the worktree's on top**.
   A worktree with none gets main's; `layering: replaces` in a worktree's frontmatter drops main's.
   Plans never layer: they go to this run's storage root.
   A `.f10/instructions/` *below* the checkout root is ignored.
   Neither checkout holding one, the loader tries **`~/.f10/<project>/instructions/`** (`<project>` = main checkout's basename, or the cwd's outside git), which makes the whole storage root out-of-tree (**Storage** below).
3. **Precedence is scope-major, specificity-minor**: the generic step, then `main/project.md`, `main/<step>.md`, `worktree/project.md`, `worktree/<step>.md`.
   The last statement stands.
   A field the worktree does not mention keeps main's value.
   Incompatible stacks call for `replaces main`, never a field-by-field merge.
   A contradiction you cannot resolve: say so rather than pick.
4. **`instructions files:`** names every `.md` per layer; layer blocks and `absent:` cover only requested names.
   A pipeline name with no generic step (like `e2e`) is a **project-defined step**: its `.f10/instructions/e2e.md` *is* the step.
5. **Inferred defaults**, when `project.md` is absent or partial: the remote host gives `gh`/`glab`; the project name gives the task id prefix (`f10` is `F10`, `git-undo` is `GU`, `herdr` is `HER`); `go.mod` / `Gemfile` / `package.json` give stack and role; `justfile` / `Makefile` give verify commands.
   State them once, the prefix included, and suggest `f10 init`, which writes `.f10/instructions/project.md` from the same probes.
   Never refuse to run for missing config.

## `project.md` contract

A YAML frontmatter block holds the facts the `f10` binary acts on (an unknown key or value is an error), then prose under headings holds the rest.
Every heading is optional, and only **Tracker** matters for the pipeline's contracts.
A fact belongs to the step or skill that acts on it, which says what it means: `pipelines` and `planless` to `/f10:ship`, `review` to the **review** step, `catchup` to the **catchup** step.
The full reference for humans is `docs/project-setup.md`.

- **Project** - one line: what this is and its stack.
- **Tracker** - kind and **task id format** in the frontmatter's `tracker:` (`ABC-####`, `GH-###`); under the heading, the **fetch** and **create** adapters, a skill or a CLI command (`gh issue view <n> --comments`, `gh issue create`).
- **Hosting & PR** - how to open a PR/MR: a skill, or `gh pr create` / `glab mr create` mechanics (branch naming, labels, assignee).
  The branch's *shape* is the project's; that it carries the task id is not (`steps/pr.md`).
- **Verify** - the exact lint and test commands, plus any "never run X" rules.
- **Roles** - per step, on top of each step's base role: **Always** for every run, **Conditional** when the task touches a named area ("+ senior UI/UX engineer on user-facing UI").
  `fetch` settles the areas and the plan file records the roles for `/f10:ship`.
- **Review** - categories, a never-flag list and a human reviewer (`steps/review.md`).
- **Guardrails** - domain rules the plan and its code must honour: a UI component gallery, PII handling, preferred dependencies.

Every **adapter** is a binding: missing, erroring or ambiguous fails that step (`conventions/failure.md`); never substitute a different tool.

**Visibility** is frontmatter `visibility: stealth | public`, **stealth** when absent (below).
**Storage** is frontmatter `storage:`.
**in-repo** (default): storage root `<checkout root>/.f10/`, per checkout, untracked per Visibility.
**out-of-tree**: storage root `~/.f10/<project>/`, shared by every worktree, nothing f10-related inside the project directory; it declares itself by location (point 2 above).
Two projects with the same basename collide there: rename one dir, or keep one in-repo.
Roots report as physical paths (symlinks resolved).

## Stealth mode

Unless `project.md` says `public`:

- `.f10/` stays untracked; prefer **`.git/info/exclude`** over `.gitignore`, so even the ignore entry is never committed.
- No f10 traces in anything that leaves the machine: no mention of f10, plan files, or `.f10/` paths in commits, branch names, PR/MR text, tracker comments, or code comments.
- Never justify code or decisions by the pipeline (no "per the plan / per step 3"); state the *domain* reason the plan recorded.
- Never commit or push files under `.f10/`.
