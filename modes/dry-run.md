# Convention · dry-run - resolve and report, change nothing

A **dry run** makes f10's resolution observable: it does all the reading and resolution a real
run does, prints what it resolved and what *would* fire, then stops - executing nothing. It's
how a repo's f10 binding (or its inferred defaults) is validated before trusting a real run.

## Activation

Any f10 skill triggers a dry run when its argument contains the token **`--dry-run`**
(anywhere in it - `/f10:ship 1042 --dry-run`). Strip the token, then route the remaining
argument exactly as normal - but follow *this* file instead of executing the steps.

## Principle: resolve everything, execute nothing

Do the reads f10 always does to figure out *what* it would do, but perform **no side effect
and no work-producing step**. In a dry run you must **not**:

- fetch a task (don't invoke the tracker's fetch adapter / `gh`/`glab` - resolve and print the
  command instead),
- create a task, investigate the codebase, draft task bodies, or write a plan file,
- implement, commit, push, open a PR/MR, review, or deploy,
- create directories, touch `.git/info/exclude`, or write anything to disk.

You **may** read what's needed to *resolve*: `.f10/instructions/*` (both layers where both
exist), `git worktree list`, and cheap repo signals for inference (does `go.mod` / `justfile` /
a remote exist). Prefer already-known facts over shelling out. Never run the adapters
themselves.

## Procedure

1. **Load context** per `conventions/context.md` - its two calls (`--all` for a ship dry run).
   This is the point of the exercise, so read the resolver's output for *how* each fact
   resolved: which instructions (main, worktree, layered, out-of-tree, or none → inference),
   which overlays exist, and every inferred default with its signal.
2. **Resolve routing** - apply the invoking skill's routing rules to the stripped argument:
   which entry step, what the argument was interpreted as, and where the run would stop.
3. **Resolve each step that would run** - its adapter (skill or CLI command, *quoted, not
   executed*), whether a same-named overlay applies, and its concrete output (e.g. the exact
   `<storage root>/plans/<TASK-ID>.md` path, and whether an existing file would be archived
   first and to what `archive/<TASK-ID>.<N>.md`). For ship, resolve the selected pipeline and
   list its steps in order with each one's adapter.
4. **Report** in the format below, then **stop**. Do not offer to proceed for real - the user
   re-runs without `--dry-run` when they want the real thing.

## Report format

Adapt to the invoking skill; omit sections that don't apply. One fact per line, commands
quoted verbatim.

**Scope the context to the run's path** - surface only the facts a step on *this* path
actually consumes; a fact nothing on the path uses is noise:

- **brainstorm**: instructions source, role, guardrails, stack - and no adapter at all, since
  it binds none and creates nothing.
- **capture**: instructions source, role, the **create** adapter + id format, visibility - not
  verify, the fetch adapter, guardrails, or the ship pipeline.
- **plan** (fetch → plan): the above plus the **fetch** adapter, guardrails, and the plan
  output path - still not verify or the PR/deploy adapters.
- **ship**: whatever its selected pipeline touches - verify (implement), the PR/push adapter,
  review, deploy, guardrails, visibility - listed per step.

Mark `inferred` on every fact that came from inference rather than `project.md`, but only for
facts on the path.

```
f10 · <skill> · DRY RUN - nothing will be fetched, written, created, or pushed

Context resolved   (only facts this run's path uses)
  instructions    <path(s), or "none - inferring">   (<main | worktree | layered, main + worktree>)
  overlays        <the .md files each layer holds, per the resolver's `instructions files:` line>
  role            <role adopted this run>
  tracker         <kind>, id format <FMT>   (<create | fetch> adapter shown under Step below)
  verify          <commands, or inferred source>          - ship only (implement/verify steps)
  visibility      <stealth | public>  → <one-line implication>
  storage         <the resolved absolute root>   (in-repo | out-of-tree; plans land under it)
  inferred        <each inferred default ← the signal>   (only for facts on the path)

Routing
  arg "<stripped>"  → <interpretation, e.g. matches ABC-#### → ABC-1042 | free-text | plan file>
  path              <steps in order>   (<what's skipped and why>)
  stops at          <the deliverable this skill would end on>

Step: <name>
  adapter         <skill to invoke | CLI command, quoted - NOT run>
  overlay         <APPLIED .f10/instructions/<name>.md | none>
  would produce   <output path / action, e.g. write <storage root>/plans/ABC-1042.md>
  archive first?  <yes → plans/archive/ABC-1042.1.md | n/a>
  (repeat per step on the path; for ship, one block per pipeline step in order)

Not executed: <the first real side effect this run would have performed>
```

## Faithfulness note

A dry run is accurate for **structural** resolution - paths, adapters, routing, which overlays
exist and which one wins, worktree layering, out-of-tree storage, the ship pipeline and its
order. It does not predict free-text content (a drafted task body, the plan's prose) -
producing that *is* the work a dry run skips. Report structure, not invented content.
