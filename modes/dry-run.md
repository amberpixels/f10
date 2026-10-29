# Mode · dry-run - load and report, change nothing

Load as normal, read the argument as the skill's Arguments items do, and report what the run would do.
Execute nothing.

**Reads stop at loading**: the instructions, `git worktree list`, and cheap checks (does `go.mod`, a `justfile`, a remote exist).
Nothing is fetched (no `f10 task read`, no fetch adapter), written or created.
Every command a step would run is printed, quoted, not run.

**Context** is `f10 config`, printed verbatim: where each fact came from, `declared` or `default`.

**The path**, in the format below: how the argument was read, the steps in order, and per step its adapter, any overlay (`instructions files:` lists them before the step loads), and its exact output (the plan path, an archive move, the findings round).
Only facts a step on this path consumes; never invented content such as a task body or plan prose.

```
f10 · <skill> · DRY RUN - nothing will be fetched, written, created, or pushed

Routing
  arg "<stripped>"  → <interpretation, e.g. matches ABC-#### → ABC-1042 | free-text | plan file>
  path              <steps in order>   (<what's skipped and why, e.g. a saved plan's `Kind: spike` ends it at implement, or plan skipped: the flow is planless, or the task has a plan file; the area that chose the flow, when `flow_by_area` did>)
  stops at          <the task, plan, shipment or verdict this run would end on>

Step: <name>
  adapter         <skill to invoke | CLI command, quoted - NOT run>
  overlay         <APPLIED .f10/instructions/<name>.md | none>
  guard           <catchup <method> onto <base> | catchup skipped (<why>) | nothing to bring in>, from `f10 pr catchup --guard <entry> --no-fetch`   (ship only: before implement, pr and a change judge)
  stale           <judge first if created before <today - stale_after>>   (fetch only, when stale_after is declared and a plan would be written)
  would produce   <output path / action, e.g. write <storage root>/plans/ABC-1042.md>
  (one block per step on the path; for ship, per flow entry in order)

Not executed: <the first real side effect this run would have performed>
```

Then stop; the user re-runs without `--dry-run`.
