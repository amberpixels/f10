# Convention · context - per-project instructions

f10 itself is **generic**. Everything project-specific - tracker, stack, roles, verify
commands, PR flow, review flow, domain guardrails - lives in the project, under
**`.f10/instructions/`**. Every f10 run starts by loading this context.

## Loading order (do this once, at the start of any f10 run)

**Run the resolver - one call, not five reads.** It does the lookup, worktree fallback, overlay
concatenation, and inference probes below in a single shot and prints one labelled bundle, so a
run spends one round trip here instead of a chain of Reads/greps:

```
${CLAUDE_PLUGIN_ROOT}/bin/resolve.sh <step> [<step> ...]
```

Pass the step(s) this run executes - `capture`; or `fetch plan`; or the ship pipeline's steps
(`implement pr review`). Read its output as the resolved context, then proceed. The script only
**concatenates and probes** - it never interprets `project.md` or binds an adapter; you still
read the prose and decide. What it returns (and the contract it implements by hand if the script
is ever unavailable):

1. **project.md** - `<storage root>/instructions/project.md`, the project facts file (contract
   below). The **storage root** is `.f10/` in the current checkout unless resolution lands
   out-of-tree (next two points).
2. **Worktree fallback** - if the current checkout has no `.f10/instructions/`, it uses the main
   checkout's (first `git worktree list` path); instructions are often untracked and don't
   propagate into fresh worktrees. Plans still always go to the **current** worktree's
   `.f10/plans/`.
3. **Out-of-tree storage** - if neither checkout has `.f10/instructions/`, it tries
   **`~/.f10/<project>/instructions/`**, where `<project>` is the basename of the main
   checkout's path (of the cwd when not in git). Finding instructions there *is* the
   declaration that this project stores out-of-tree: the whole storage root - instructions
   **and** plans - lives at `~/.f10/<project>/`, and nothing f10-related is ever written
   inside the project directory (no `.f10/`, no exclude entry needed). See **Storage** in the
   contract below.
4. **Per-step overlays** - each `<step>.md` you asked for, concatenated. An overlay extends the
   generic step; where the two conflict, the overlay wins.
5. **Inferred signals** - when project.md is absent/partial, the deterministic probes (remote
   host → `gh`/`glab`; `go.mod` / `Gemfile` / `package.json` → stack and role; `justfile` /
   `Makefile` → verify commands). State the assumptions you're proceeding on and suggest creating
   `.f10/instructions/project.md`. Do not refuse to run just because the config is missing.

## `project.md` contract

Free-form markdown under these headings - prose, not YAML. Only **Tracker** really matters for
the pipeline's contracts; everything else has workable inferred defaults.

- **Project** - one-liner: what this is, the stack, and the role to adopt per step
  (e.g. "plan as a senior software architect + Go developer; implement as a senior Go developer").
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
  pipeline marked **(planless)** skips capture/fetch/plan for free-text input: no ticket, no
  plan file - a brief inline plan in chat is enough.
- **Verify** - the exact lint/test commands, plus any "never run X" rules.
- **Review** - facts about the project's review(s): who/what reviews, when it fires, what
  resolves it. Whether and where a review actually *runs* is the Ship pipeline's call.
- **Guardrails** - domain rules: UI component galleries, PII handling, preferred dependencies,
  anything the plan and implementation must honour.
- **Visibility** - `stealth` or `public`. Missing → **stealth**.
- **Storage** - where this project's f10 files live. Missing → **in-repo**: the storage root
  is `<repo>/.f10/` (untracked per Visibility). **out-of-tree**: the storage root is
  `~/.f10/<project>/` (`<project>` = the main checkout's basename) and *nothing* f10-related
  exists inside the project directory - for when even an untracked dir in the worktree is too
  visible (screen-sharing, worktree-scanning tools). In practice out-of-tree declares itself
  by location (the loading order finds the instructions there); this section just makes it
  explicit. All worktrees of a repo share one out-of-tree root, so plans are per-project, not
  per-worktree. If two projects share a basename, out-of-tree keying collides - rename one
  dir or keep the busier project in-repo.

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
