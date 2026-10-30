# Mode · dry-run - load and report, change nothing

A dry run shows what a run would do, and does none of it.
Load as normal, and read the argument as the skill's Arguments items say.
Then report what the run would do.
Execute nothing.

## What a dry run reads

Reads stop once loading is done.
A dry run reads the instructions and `git worktree list`.
It may also make cheap checks, such as whether a `go.mod`, a `justfile` or a remote exists.

Nothing is fetched, so neither `f10 task read` nor the fetch adapter runs.
Nothing is written or created.
Every command a step would run is printed, quoted, and not run.

## What the report holds

The report has two parts, the context and the path.

**The context** is the output of `f10 config`, printed verbatim.
It shows where each fact came from, `declared` or `default`.

**The path** follows the format below.
It says how the argument was read and lists the steps in order.
For each step it gives the adapter, the overlay if there is one, and the step's exact output, such as the plan path, an archive move or the findings round.
The load's `instructions files:` line names the overlays before any step loads.

The path holds only facts that a step on it consumes.
Never invent content for it, such as a task body or plan prose.

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

## After the report

Stop once the report is printed.
The user runs the command again without `--dry-run` to do the work.
