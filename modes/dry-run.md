# Convention · dry-run - load and report, change nothing

With **`--dry-run`** anywhere in the argument (`/f10:ship 1042 --dry-run`), strip it, route as
normal, and follow this file instead of the steps: load everything, execute nothing.

**Never**: fetch a task (print the fetch adapter / `gh`/`glab` command instead); create a task,
investigate the codebase, draft a task body, or write a plan; implement, commit, push, open a
PR/MR, review, or deploy; create directories, touch `.git/info/exclude`, or write anything.
**May** read what loading needs: `.f10/instructions/*` (both layers), `git worktree list`, and
cheap inference signals (does `go.mod` / `justfile` / a remote exist), preferring facts already
known over shelling out. Never run an adapter.

1. **Load context** with the two calls (`--all` for ship), noting *how* each fact loaded: which
   instructions (main, worktree, layered, out-of-tree, or none and inferred), which overlays,
   each inferred default and its signal.
2. **Settle routing**: the entry step, how the argument was read, where the run would stop.
3. **Name each step that would run**: its adapter, *quoted, not executed*; whether an overlay
   applies; its concrete output (e.g. the exact `<storage root>/plans/<TASK-ID>.md`, and any
   archive to `archive/<TASK-ID>.<N>.md`). For ship, the selected pipeline, step by step.
4. **Report** in the format below and **stop**. Never offer to proceed for real; the user re-runs
   without `--dry-run`.

**Show only facts a step on *this* path consumes**, one per line, commands quoted; mark each
`inferred` that did not come from `project.md`. Per skill, beyond instructions source and role:

- **brainstorm**: guardrails, stack. No adapter.
- **judge**: guardrails; for the routed input only, the **fetch** adapter and plan path, or
  `gh pr diff` / `glab mr diff`, or `git show` / `git diff`; whether blind mode would spawn a
  fresh subagent.
- **review**: the categories (four core, project ones with source-of-truth files and the
  never-flag list, or "none declared"), the base and diff command, the findings path with its
  round (`<storage root>/reviews/<TASK-ID>/<round>.md`, or the decline at a third round), and
  that the reviewer always runs in a fresh subagent.
- **resolve**: the findings file, how many findings lack a verdict, verify, and where each
  `ask` would go (one questionnaire, or the declared human reviewer).
- **catchup**: the base and how it was found, the strategy and its source (`catchup:` under
  Hosting & PR, or the rebase default), the integration command, verify. Nothing pushed.
- **capture**: the **create** adapter, id format, visibility.
- **plan**: capture's facts plus the **fetch** adapter, guardrails, the plan path.
- **ship**: per pipeline step, what it touches (verify, the PR/push adapter, review, deploy,
  guardrails, visibility).

```
f10 · <skill> · DRY RUN - nothing will be fetched, written, created, or pushed

Context loaded     (only facts this run's path uses)
  instructions    <path(s), or "none - inferring">   (<main | worktree | layered, main + worktree>)
  overlays        <the .md files each layer holds, per the bundle's `instructions files:` line>
  role            <role adopted this run>
  tracker         <kind>, id format <FMT>   (<create | fetch> adapter shown under Step below)
  verify          <commands, or inferred source>          - ship only (implement/verify steps)
  visibility      <stealth | public>  → <one-line implication>
  storage         <the absolute root the bundle reported>   (in-repo | out-of-tree; plans land under it)
  inferred        <each inferred default ← the signal>   (only for facts on the path)

Routing
  arg "<stripped>"  → <interpretation, e.g. matches ABC-#### → ABC-1042 | free-text | plan file>
  path              <steps in order>   (<what's skipped and why>)
  stops at          <the task, plan, shipment or verdict this run would end on>

Step: <name>
  adapter         <skill to invoke | CLI command, quoted - NOT run>
  overlay         <APPLIED .f10/instructions/<name>.md | none>
  would produce   <output path / action, e.g. write <storage root>/plans/ABC-1042.md>
  archive first?  <yes → plans/archive/ABC-1042.1.md | n/a>
  (repeat per step on the path; for ship, one block per pipeline step in order)

Not executed: <the first real side effect this run would have performed>
```

Report structure (paths, adapters, routing, overlays, layering, storage, the pipeline), never
invented content such as a task body or plan prose.
