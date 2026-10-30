# Convention · report - a step succeeded

What a step or skill prints when it **works**.
`conventions/failure.md` and `modes/dry-run.md` set the other two shapes.
A report is read once, to copy one value out of it: a url, a sha or a path.

## The facts block

Every success report opens with one aligned block, fenced and tagged `yaml` so the terminal colors it:

```yaml
task:    GH-2
title:   Add rate limiting to the public API
url:   ↗ https://github.com/amberpixels/f10/issues/2
plan:    .f10/plans/GH-2.md
```

- The **label**, left-aligned and padded to the widest label in *this* block, **colon included**.
- **Two spaces**, then one **marker cell**: `↗` for a url, blank otherwise.
- **One space**, then the **value**.

Nothing parses the block.
If a value would break the highlighting, print it untagged.

1. **Urls are bare.**
   Never `[text](url)`, `<url>`, backticks, or a trailing comma or period.
2. **Facts in the block, prose beneath it.**
   Rows carry addressable values only: task id, url, plan path, branch, commit sha, environment.
   The one exception is a `title` row right under `task`: the task's title as the tracker holds it, so the reader recognises the id without opening it.
   What changed, how far the pipeline got and what the user does next are sentences under the block.
3. **Only rows the run produced.**
   No placeholders, no `n/a`, no empty values.
   A ship run prints the shipment rows its last step declares (`conventions/context.md` → shipment).
   A pipeline ending in a project-defined `publish`, for example, prints its `report` url and no branch, PR or sha rows.
4. **Most identifying rows first**: the task, its title, its url, then this step's artifacts.
5. **The marker means "a url you can open"**, so it never marks a path, a sha or a branch.
   It is `↗` (U+2197), not the double-width `🔗`.

### Several tasks

Several tasks from one run take one row each, in place of the `task` and `url` rows.
Print them with `f10 task report <ids>`, verbatim, in the fenced `yaml` block.
Ids go in `After:` order, a task outside the chain last.
Titles go in the prose beneath.

A parent and its subtasks take two blocks, first `f10 task report <parent> <subtask ids>` for the urls:

```yaml
FT-71:     ↗ https://github.com/amberpixels/f10/issues/71
├─ FT-72:  ↗ https://github.com/amberpixels/f10/issues/72
└─ FT-73:  ↗ https://github.com/amberpixels/f10/issues/73
```

Then `f10 status <parent> --plain` in a plain fenced block: its tree carries each title and no url, so nothing goes beneath it.

Never draw the rows or the glyphs by hand, or turn them into a table.

### Other reports

A step that produces nothing addressable (`implement`) reports in prose.
A skill reports the whole run the same way.
Failure and dry-run reports use the same label column and bare urls, but their block is **plain-fenced and colonless**, with no marker cell, because their values are prose.

## The next skill

A skill's **Exit** block may name a `Next:` skill.
Offer it in one line under the report, with its condition if the Exit item gives one.
Leave the call to the user: never run it.

**A chained skill is not offered, it runs.**
When the argument goes on with `&& /f10:<skill> ...`, the user already made that call.
Invoke it with the Skill tool right after this skill's report, its arguments verbatim (`--driven` kept), without ending the turn.
A stop the steps call for (a `blocked` ask, a failure) still stops the chain.

## The status-line badge

Three glyphs in the status line, one per phase (capture, plan, ship), track a running run:

```
f10-state.sh set <capture|plan|ship> <running|done|failed|partial|prior|blocked> [<leaf>]
f10-state.sh task <id> [<url>]
f10-state.sh note <reason> [<next>]
f10-state.sh pipeline <step>...
```

The bare name is the whole command, because `bin/` is on the Bash tool's `PATH`.
Each step's `Badge:` line says what it reports.
A step without one opens no phase.
Entering a phase retires the phases before it.
Inside a pipeline, `/f10:ship` declares every step with `pipeline`, the last one final, so `f10 status` lists the steps not reached yet.
It also passes the current step as `<leaf>`.

- `prior`: the work exists from an earlier run.
  `task <id>` marks capture `prior` by itself.
  The ship skill marks a reused plan.
- `partial`: the run stopped after producing something durable (commits, an open PR, a deploy; `conventions/failure.md` rule 7).
- `blocked`: a decision ended the run, not breakage: a judge verdict (`steps/judge.md`) or a driven ask (`modes/driven.md`).

A phase that stops keeps its leaf.
`note` records why and what unblocks it, one line each, for `f10 status` (`/f10:status` in a session).

**Badge calls are best-effort, but never skipped.**
The run never reads the badge state back, but the ledger records every call, and `f10 stats` times each step from its `set ship running <entry>` call.
A skipped call counts that step's time to the step before it.
If a badge call fails, ignore it: never treat it as a step failure, retry it, mention it, or let it change the run.
Send it in the same message as the step's first real call, joined with `;` or beside a `Read` (`conventions/latency.md`).
