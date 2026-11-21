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
plan:    /work/app/.f10/plans/GH-2.md
```

- The **label**, left-aligned and padded to the widest label in *this* block, **colon included**.
- **Two spaces**, then one **marker cell**: `↗` for a url, blank otherwise.
- **One space**, then the **value**.

Nothing parses the block.
If a value would break the highlighting, print it untagged.

1. **Urls are bare.**
   Never `[text](url)`, `<url>`, backticks, or a trailing comma or period.
   **Paths are absolute**, as the storage root gives them, because upstream herdr and most terminals open only an absolute path on click, and a relative one resolves against the pane's directory, which may not be where f10 wrote the file.
2. **Facts in the block, prose beneath it.**
   Rows carry addressable values only: task id, url, plan path, branch, commit sha, environment.
   The one exception is a `title` row right under `task`: the task's title as the tracker holds it, so the reader recognises the id without opening it.
   What changed, how far the pipeline got and what the user does next are sentences under the block.
3. **Only rows the run produced.**
   No placeholders, no `n/a`, no empty values.
   A ship run prints the shipment rows its last step declares (`skills/ship/SKILL.md`).
   A pipeline ending in a project-defined `publish`, for example, prints its `report` url and no branch, PR or sha rows.
4. **Most identifying rows first**: the task, its title, its url, then this step's artifacts.
5. **The marker means "a url a browser opens"**, so it never marks a path, a sha or a branch.
   It is `↗` (U+2197), not the double-width `🔗`.

### Several tasks

Several tasks from one run take one row each, in place of the `task` and `url` rows.
Print them with `f10 task report <ids>`, verbatim, in the fenced `yaml` block.
Ids go in `After:` order, a task outside the chain last.
Titles go in the prose beneath.

A parent and its subtasks take one block, from `f10 task report --children <parent>`.
It draws the parent, then its open subtasks nested by `After:`, each url under its title:

```
FT-71        Rate limits
│            ↗ https://x/71
└─ FT-72     Count per key
   │         ↗ https://x/72
   └─ FT-73  Reject excess
             ↗ https://x/73
```

Titles break the highlighting, so it prints untagged.
Its rows carry the titles, so nothing goes beneath it.

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

`bin/` is on the Bash tool's `PATH`, so the bare name is the command.
Each step's `Badge:` line says what it reports, and a step without one opens no phase.
Inside a pipeline, `/f10:ship` declares every step with `pipeline` and passes the current one as `<leaf>`.

- `prior`: the work exists from an earlier run, such as a reused plan.
- `partial`: the run stopped after producing something durable (`conventions/failure.md` rule 7).
- `blocked`: a decision ended the run, not breakage: a judge verdict or a driven ask.

`note` records why a run stopped and what unblocks it, for `f10 status`.

**Never skip a badge call**, because `f10 stats` times each step from it.
A call that fails is ignored: never retry it, mention it or let it change the run.
Send it with the step's first real call (`conventions/latency.md`).
