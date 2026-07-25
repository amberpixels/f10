# Convention · dry-run - resolve and report, change nothing

A **dry run** makes f10's resolution observable. f10 has no prompt generator: a run is this
agent reading step files and the loaded `project.md`/overlays and acting on them. A dry run does
all of that reading and resolution, prints what it resolved and what *would* fire, and then
stops - executing nothing. It's how you validate a repo's f10 binding (or its inferred defaults)
before trusting a real run, in any repo.

## Activation

Any f10 skill (`/f10:capture`, `/f10:plan`, `/f10:ship`) triggers a dry run when its argument
contains the token **`--dry-run`** (anywhere - `/f10:plan --dry-run 1042`,
`/f10:ship 1042 --dry-run`). Strip that token from the argument first, then route the remaining
argument exactly as normal - but follow *this* file instead of executing the steps.

## Principle: resolve everything, execute nothing

Resolution-only. Do the reads f10 always does to figure out *what* it would do, but perform **no
side effect and no work-producing step**. Specifically, in a dry run you must **not**:

- fetch a task (don't invoke the tracker's fetch adapter / `gh`/`glab` - resolve and print the
  command instead),
- create a task, investigate the codebase, draft task bodies, or write a plan file,
- implement, commit, push, open a PR/MR, review, or deploy,
- create directories, touch `.git/info/exclude`, or write anything to disk.

You **may** read what's needed to *resolve*: `.f10/instructions/*` (with the worktree fallback),
`git worktree list`, and cheap repo signals for inference (does `go.mod` / `justfile` / a remote
exist). Prefer already-known facts over shelling out. Never run the adapters themselves.

## Procedure

1. **Load context** per `conventions/context.md` - run its resolver
   (`${CLAUDE_PLUGIN_ROOT}/bin/resolve.sh <step> …`) once; it already does the lookup, worktree
   fallback, overlay concat, and probes. This is the point of the exercise, so read its output
   for *how* each fact resolved: which `project.md` (direct, worktree-fallback, or none →
   inference), which overlays exist, and every inferred default with its signal.
2. **Resolve routing** - apply the invoking skill's routing rules to the stripped argument:
   which entry step, what the argument was interpreted as, and where the run would stop.
3. **Resolve each step that would run** - for every step on the path, name its adapter (skill or
   CLI command, *quoted, not executed*), whether a same-named overlay applies, and its concrete
   output (e.g. the exact `.f10/plans/<TASK-ID>.md` path, and whether an existing file would be
   archived first and to what `archive/<TASK-ID>.<N>.md`). For ship, resolve the selected
   pipeline and list its steps in order with each one's adapter.
4. **Report** in the format below, then **stop**. Do not offer to proceed for real as the next
   action - the user re-runs without `--dry-run` when they want the real thing.

## Report format

Adapt to the invoking skill; omit sections that don't apply. Keep it scannable, one fact per
line, commands quoted verbatim.

**Scope the context to the run's path** - surface only the facts a step on *this* path actually
consumes, not every field `project.md` defines. A fact nothing on the path uses is noise; leave
it out. Roughly:

- **capture** uses: project.md source, role, tracker's **create** adapter + id format,
  visibility. It does **not** use verify, the fetch adapter, guardrails, or the ship pipeline.
- **plan** (fetch → plan) uses: project.md source, role, tracker's **fetch** adapter + id
  format, guardrails, the plan output path, visibility. It does **not** use verify or the PR/deploy adapters.
- **ship** uses: whatever its selected pipeline touches - so verify (implement), the PR/push
  adapter, review, deploy, guardrails, visibility - listed per step.

Always report `inferred` when a fact came from inference rather than `project.md`, but only for
facts on the path (don't infer-and-print verify for a capture run).

```
f10 · <skill> · DRY RUN - nothing will be fetched, written, created, or pushed

Context resolved   (only facts this run's path uses)
  project.md      <path, or "none - inferring">   (<direct | worktree-fallback from <main>>)
  role            <role adopted this run>
  tracker         <kind>, id format <FMT>   (<create | fetch> adapter shown under Step below)
  verify          <commands, or inferred source>          - ship only (implement/verify steps)
  visibility      <stealth | public>  → <one-line implication>
  storage         <in-repo .f10/ | out-of-tree ~/.f10/<project>/>   (plans land under it)
  inferred        <each inferred default ← the signal>   (only for facts on the path)

Routing
  arg "<stripped>"  → <interpretation, e.g. matches ABC-#### → ABC-1042 | free-text | plan file>
  path              <steps in order>   (<what's skipped and why>)
  stops at          <the deliverable this skill would end on>

Step: <name>
  adapter         <skill to invoke | CLI command, quoted - NOT run>
  overlay         <APPLIED .f10/instructions/<name>.md | none>
  would produce   <output path / action, e.g. write .f10/plans/ABC-1042.md>
  archive first?  <yes → plans/archive/ABC-1042.1.md | n/a>
  (repeat per step on the path; for ship, one block per pipeline step in order)

Not executed: <the first real side effect this run would have performed>
```

## Faithfulness note

A dry run is accurate for **structural** resolution - paths, adapters, routing, which overlay
wins, worktree fallback, out-of-tree storage, the ship pipeline and its order. It does **not** predict free-text
content (a drafted task body, the plan's prose), because producing that *is* the work a dry
run skips. Report structure, not invented content.
