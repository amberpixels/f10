# Convention · context - per-project instructions

f10 itself is **generic**. Everything project-specific - tracker, stack, roles, verify
commands, PR flow, review flow, domain guardrails - lives in the project, under
**`.f10/instructions/`**. Every f10 run starts by loading this context.

## Vocabulary

Three tiers, three words. They never substitute for one another:

- **skill** - an entry point the user invokes: `/f10:capture`, `/f10:plan`, `/f10:ship`.
- **step** - a unit of work the plugin runs: `capture`, `fetch`, `plan`, `implement`, `pr`,
  `push`, `review`, `deploy` (`steps/*.md`). A skill runs one or more steps.
- **stage** - one ordered unit *inside* a plan file. Never a step, never a skill.

Write a skill as `/f10:plan` and a step as "the plan step" or `steps/plan.md`. Two of them share
a name, so bare `plan` is ambiguous: never use it where either could be meant. Reading
"run `steps/plan.md`" means execute that step's instructions - it is never an instruction to
invoke the `/f10:plan` skill again.

## Loading order (do this once, at the start of any f10 run)

**Run the resolver - one call, not five reads.** It does the lookup, worktree fallback, overlay
concatenation, inference probes, and this run's conventions in one shot, printing a single
labelled bundle:

```
${CLAUDE_PLUGIN_ROOT}/bin/resolve.sh <step> [<step> ...]
```

Pass the step(s) this run executes - `capture`, or `fetch plan`, or the ship pipeline's steps
(`implement pr review`). Read its output as the resolved context. The script only
**concatenates and probes**: it never interprets `project.md` or binds an adapter, so you still
read the prose and decide. What it returns (and the contract to implement by hand if it is ever
unavailable):

1. **project.md** - `<storage root>/instructions/project.md`, the project facts file (contract
   below). The **storage root** is the **checkout root** plus `.f10/` - anchored to
   `git rev-parse --show-toplevel`, never to the process cwd, so a run launched from a
   subdirectory resolves exactly as one launched from the top - unless resolution lands
   out-of-tree (next two points). It is reported as an absolute path: use that path verbatim
   rather than re-deriving one.
2. **Worktree fallback** - if the current checkout is a **different worktree** from main and has
   no `.f10/instructions/`, it uses the main checkout's (first `git worktree list` path);
   instructions are often untracked and don't propagate into fresh worktrees. Plans still always
   go to the **current** worktree's `.f10/plans/`. A subdirectory of the main checkout is not a
   fallback case - it resolves directly against that checkout's root. A `.f10/instructions/`
   sitting *below* the root is not a per-directory config: resolution notes it and ignores it.
3. **Out-of-tree storage** - if neither checkout has `.f10/instructions/`, it tries
   **`~/.f10/<project>/instructions/`** (`<project>` = basename of the main checkout, or of the
   cwd outside git). Finding instructions there *is* the declaration: the whole storage root,
   instructions **and** plans, lives there, and nothing f10-related is ever written inside the
   project directory. See **Storage** below.
4. **Per-step overlays** - each `<step>.md` you asked for, concatenated. An overlay extends the
   generic step; where the two conflict, the overlay wins.
5. **Inferred signals** - when project.md is absent/partial, the deterministic probes (remote
   host → `gh`/`glab`; `go.mod` / `Gemfile` / `package.json` → stack and role; `justfile` /
   `Makefile` → verify commands). State the assumptions you're proceeding on and suggest creating
   `.f10/instructions/project.md`. Do not refuse to run just because the config is missing.

## `project.md` contract

Free-form markdown under these headings - prose, not YAML. Only **Tracker** really matters for
the pipeline's contracts; everything else has workable inferred defaults.

Every **adapter** below is a binding, not a suggestion. An adapter that is missing, errors, or is
ambiguous is a failure of that step, never a licence to substitute a different tool. Stop and
report per `conventions/failure.md`.

- **Project** - one-liner: what this is and the stack it's built on.
- **Roles** - the role to adopt per step, on top of the base role each step file declares.
  **Always** - applied to every run (e.g. "plan as a senior software architect + Go developer;
  implement as a senior Go developer"). **Conditional** - attached only when the task touches a
  named area (e.g. "+ senior UI/UX engineer when the task touches user-facing UI; + data
  engineer on schema migrations; + security engineer on auth or PII"). Missing → each step's
  base role alone. Conditional roles resolve from the areas `capture` recorded and `fetch`
  confirmed; the plan file records which ones were adopted, so `/f10:ship` inherits them
  instead of re-deriving.
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
  runs unless the user selects another by name or in their own words. Never self-select a
  non-default pipeline - for tiny work you may *suggest* one and let the user pick. A
  pipeline marked **(planless)** skips capture/fetch/plan for free-text input: no task, no
  plan file - a brief inline plan in chat is enough.
- **Verify** - the exact lint/test commands, plus any "never run X" rules.
- **Review** - facts about the project's review(s): who/what reviews, when it fires, what
  resolves it. Whether and where a review actually *runs* is the Ship pipeline's call.
- **Guardrails** - domain rules: UI component galleries, PII handling, preferred dependencies,
  anything the plan and implementation must honour.
- **Visibility** - `stealth` or `public`. Missing → **stealth**.
- **Storage** - where this project's f10 files live. Missing → **in-repo**: storage root
  `<checkout root>/.f10/`, untracked per Visibility. **out-of-tree**: storage root
  `~/.f10/<project>/` (`<project>` = the main checkout's basename), with *nothing* f10-related
  inside the project directory - for when even an untracked dir is too visible (screen-sharing,
  worktree scanners). Out-of-tree declares itself by location, so this section only makes it
  explicit. The two modes scope plans differently: **in-repo is per-checkout** - each worktree
  has its own `.f10/`, so plans sit beside the branch they were written against, even when the
  instructions came from main via the worktree fallback - while **out-of-tree is per-project**:
  one root, keyed on the main checkout's basename, shared by every worktree of that repo. Two
  projects with the same basename collide: rename one dir, or keep the busier one in-repo.
  Reported roots are physical paths, so one reached through a symlink reads back resolved.

## Stealth mode

Unless `project.md` says `public`:

- `.f10/` stays untracked. Prefer **`.git/info/exclude`** over `.gitignore`, so even the
  ignore entry is never committed.
- No f10 traces in anything that leaves the machine: no mention of f10, plan files, or
  `.f10/` paths in commits, branch names, PR/MR text, tracker comments, or code comments.
- The work must **read as if f10 never existed**: never justify code or decisions by the
  pipeline (no "as f10:plan required X, we do Y", no "per the plan / per step 3", no
  references to gaps or plan stages in comments or PR text). State the *domain* reason
  instead - the same reason the plan itself recorded.
- Never commit or push files under `.f10/`.
