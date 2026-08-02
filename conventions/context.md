# Convention · context - per-project instructions

f10 itself is **generic**. Everything project-specific - tracker, stack, roles, verify
commands, PR flow, review flow, domain guardrails - lives in the project, under
**`.f10/instructions/`**. Every f10 run starts by loading this context.

## Vocabulary

Three tiers, never interchangeable:

- **skill** - an entry point the user invokes: `/f10:capture`, `/f10:plan`, `/f10:ship`.
- **step** - a unit of work the plugin runs: `capture`, `fetch`, `plan`, `implement`, `pr`,
  `push`, `review`, `deploy` (`steps/*.md`). A skill runs one or more steps.
- **stage** - one ordered unit *inside* a plan file. Never a step, never a skill.

Two of them share a name, so bare `plan` is ambiguous - write `/f10:plan` for the skill,
"the plan step" or `steps/plan.md` for the step. "Run `steps/plan.md`" means execute that
step's instructions, never re-invoke the skill.

## Loading order (do this at the start of any f10 run)

Two calls, with two different lifetimes:

```
${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh                    # once per context
${CLAUDE_PLUGIN_ROOT}/bin/resolve.sh <step> [<step> ...]    # once per run
```

**`conventions.sh` - the static half.** Cats the four cross-cutting rule files (`context`,
`failure`, `gaps`, `report`). Identical output everywhere, so **load it unless this context
already holds it** (its banner makes a copy easy to spot). *Per context*, not per
conversation: a subagent or a fresh session starts empty and does need it.

**`resolve.sh` - the resolved half.** One call does the lookup, worktree layering, the
instructions listing, overlay concatenation, and the inference probes, printing a single
labelled bundle. Its output **can** change between two runs in one conversation - a different
worktree, an edited `project.md` - so it is never skipped.

Pass the step(s) this run executes and read the output as the resolved context. The script only **concatenates and probes**: it never interprets `project.md`
or binds an adapter, so you still read the prose and decide. Its one exception: it does honour
the **Layering** declaration below, since composition must be settled before concatenation.

**`/f10:ship` passes `--all` rather than step names:** ship's step list *is* the ship pipeline,
declared in the very `project.md` this call prints, so ship cannot name its steps beforehand.
`--all` cats every overlay each layer holds.

What `resolve.sh` returns (and the contract to implement by hand if it is ever unavailable):

1. **project.md** - `<storage root>/instructions/project.md`, the project facts file (contract
   below). The **storage root** is the **checkout root** plus `.f10/` (or out-of-tree,
   point 3) - anchored to `git rev-parse --show-toplevel`, never the process cwd, so a run
   from a subdirectory resolves exactly as one from the top. Reported as an absolute path:
   use it verbatim, never re-derive one.
2. **Worktree layering** - a linked worktree and the main checkout (first `git worktree list`
   path) can each hold instructions, and both are read: **main's first, the worktree's on top**.
   A worktree with none gets main's alone -
   instructions are often untracked and don't propagate into fresh worktrees. A worktree whose
   `project.md` declares `Layering - replaces main` gets its own alone - what a branch
   rewriting the stack needs, so main's verify commands cannot red a different-stack worktree.
   Anything else extends. Plans still always go to the **current** worktree's `.f10/plans/`. A
   subdirectory of the main checkout is not a layering case - it resolves against that
   checkout's root - and a `.f10/instructions/` sitting *below* the root is noted and ignored,
   not a per-directory config.
3. **Out-of-tree storage** - if neither checkout has `.f10/instructions/`, it tries
   **`~/.f10/<project>/instructions/`** (`<project>` = basename of the main checkout, or of the
   cwd outside git). Finding instructions there *is* the declaration: the whole storage root,
   instructions **and** plans, lives there - see **Storage** below.
4. **Per-step overlays** - each `<step>.md` you asked for (every one a layer holds, under
   `--all`), concatenated. An overlay extends the generic step. **Precedence is scope-major,
   specificity-minor**: `main/project.md` → `main/<step>.md` → `worktree/project.md` →
   `worktree/<step>.md`, later winning - an overlay beats `project.md` *within* its layer, but
   the worktree layer beats both of main's. The bundle prints in that order - read top to
   bottom, the last statement stands.
5. **What each layer holds** - an `instructions files:` line naming the `.md` files present per
   layer. The layer blocks and the `absent:` line cover only the names you requested, so
   without it a bundle could omit an overlay while asserting nothing is missing - and it is the
   only place a **project-defined step** (a pipeline name like `e2e` whose
   `.f10/instructions/e2e.md` *is* the step) is discoverable at all.
6. **Adjudicating two layers** - prose you merge, not data the resolver merged for you. A field
   the worktree layer doesn't mention keeps main's value; a field it does mention wins outright -
   so a worktree layer should state only what it changes. Two layers describing incompatible
   stacks is a `replaces main` situation, not something to reconcile field by field. A genuine
   contradiction you cannot resolve: say so rather than picking.
7. **Inferred signals** - when project.md is absent/partial, the deterministic probes (remote
   host → `gh`/`glab`; `go.mod` / `Gemfile` / `package.json` → stack and role; `justfile` /
   `Makefile` → verify commands). State the assumptions you're proceeding on and suggest
   creating `.f10/instructions/project.md`. Do not refuse to run just because the config is
   missing.

## `project.md` contract

Free-form markdown under these headings - prose, not YAML. Only **Tracker** really matters for
the pipeline's contracts; everything else has workable inferred defaults.

Every **adapter** below is a binding, not a suggestion: missing, erroring, or ambiguous → that
step fails (`conventions/failure.md`), never substitute a different tool.

- **Project** - one-liner: what this is and the stack it's built on.
- **Layering** - `extends main` (default) or `replaces main`. Meaningful only in a **linked
  worktree's** own `project.md`; elsewhere there is nothing to layer onto. `replaces` is
  whole-dir - the worktree's overlays replace main's too, not just its `project.md`.
- **Roles** - the role to adopt per step, on top of the base role each step file declares.
  **Always** - applied to every run (e.g. "plan as a senior software architect + Go
  developer"). **Conditional** - attached only when the task touches a named area (e.g.
  "+ senior UI/UX engineer when the task touches user-facing UI; + security engineer on auth
  or PII"). Missing → each step's base role alone. Conditional roles resolve from the areas
  `fetch` settles and are recorded in the plan file, so `/f10:ship` inherits them.
- **Tracker** - kind (Notion / GitHub Issues / GitLab work items / Jira / …), the **task id
  format** (e.g. `ABC-####`, `GH-###`) - used verbatim as the plan filename
  `.f10/plans/<TASK-ID>.md` - and the **fetch** / **create** adapters: a skill to invoke or a
  CLI command to run (e.g. `gh issue view <n> --comments` / `gh issue create`).
- **Hosting & PR** - where the code lives and how to open a PR/MR: a skill, or plain
  `gh pr create` / `glab mr create` mechanics (branch naming, labels, assignee).
- **Ship pipeline(s)** - the ordered steps `/f10:ship` runs after planning, e.g.
  `implement → review (local) → pr → review (CI) → deploy (staging)`. Omitted →
  **`implement → pr`**. Each name resolves to a generic step in the plugin's `steps/`
  (extended by its same-named overlay); a name with no generic step (e.g. `e2e`) is a
  **project-defined step** - `.f10/instructions/<name>.md` *is* the step.
  A project may declare **several named pipelines** (e.g. `default`, `direct`): `default`
  runs unless the **user** selects another - never self-select one; for tiny work you may
  *suggest* and let the user pick. A pipeline marked **(planless)** skips capture/fetch/plan
  for free-text input: no task, no plan file - a brief inline plan in chat is enough.
- **Verify** - the exact lint/test commands, plus any "never run X" rules.
- **Review** - facts about the project's review(s): who/what reviews, when it fires, what
  resolves it. Whether and where a review actually *runs* is the Ship pipeline's call.
- **Guardrails** - domain rules: UI component galleries, PII handling, preferred dependencies,
  anything the plan and implementation must honour.
- **Visibility** - `stealth` or `public`. Missing → **stealth**.
- **Storage** - where this project's f10 files live. Missing → **in-repo**: storage root
  `<checkout root>/.f10/`, untracked per Visibility, **per-checkout** - each worktree has its
  own `.f10/`, so plans sit beside the branch they were written against. **out-of-tree**:
  storage root `~/.f10/<project>/` (`<project>` = the main checkout's basename), **per-project** -
  one root shared by every worktree, with *nothing* f10-related inside the project directory,
  for when even an untracked dir is too visible (screen-sharing, worktree scanners). It
  declares itself by location (point 3 above); this section only makes it explicit. Two
  projects with the same basename collide: rename one dir, or keep the busier one in-repo.
  Roots report as physical paths (symlinks resolved).

## Stealth mode

Unless `project.md` says `public`:

- `.f10/` stays untracked. Prefer **`.git/info/exclude`** over `.gitignore`, so even the
  ignore entry is never committed.
- No f10 traces in anything that leaves the machine: no mention of f10, plan files, or
  `.f10/` paths in commits, branch names, PR/MR text, tracker comments, or code comments.
- The work must **read as if f10 never existed**: never justify code or decisions by the
  pipeline (no "per the plan / per step 3"). State the *domain* reason instead - the same
  reason the plan itself recorded.
- Never commit or push files under `.f10/`.
