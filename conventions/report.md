# Convention · report - a step succeeded

What a step or skill prints when it **works**. `conventions/failure.md` and `modes/dry-run.md`
fix the other two shapes. A report is read once, to lift one value out of it: a url, a sha, a
path.

## The facts block

Every success report opens with one aligned block, fenced and tagged `yaml` for terminal colors:

```yaml
task:    GH-2
url:   ↗ https://github.com/amberpixels/f10/issues/2
plan:    .f10/plans/GH-2.md
```

- **Labels** left-aligned, padded to the widest label in *this* block, **colon included**.
- Then **two spaces**, one **marker cell** (`↗` for a url, blank otherwise), **one space**, the
  **value**.
- Nothing parses the block. If a value would break the highlighting, print it untagged.

1. **Urls bare.** Never `[text](url)`, `<url>`, backticks, or a trailing comma or period.
2. **Facts in the block, prose beneath it.** Rows carry addressable values only: task id, url,
   plan path, branch, commit sha, environment. What changed, how far the pipeline got and what
   the user does next are sentences under the block.
3. **Only rows the run produced.** No placeholders, no `n/a`, no empty values.
4. **Most-identifying first**: the task, its url, then this step's artifacts.
5. **The marker means "an openable url"**, never a path, a sha or a branch. It is `↗` (U+2197),
   not `🔗`, which is double-width.

A step that produces nothing addressable (`implement`) reports in prose. Skills report the whole
run the same way. `conventions/failure.md` and `modes/dry-run.md` use the same label column and
bare urls, but **plain-fenced and colonless**, with no marker cell: their values are prose.

## The status-line badge

Three glyphs in the status line (capture, plan, ship) track a running run:

```
f10-state.sh set <capture|plan|ship> <running|done|failed|partial|prior|blocked> [<leaf>]
f10-state.sh task <id> [<url>]
f10-state.sh note <reason> [<next>]
f10-state.sh final <step>
```

The bare name is the whole command (`bin/` is on the Bash tool's `PATH`). Each step's `Badge:`
line says what it reports; a step without one opens no phase. Entering a phase retires the ones
before it. Inside a pipeline, `/f10:ship` passes the current step as `<leaf>` and declares the
last with `final`.

- `prior` - the work exists from an earlier run. `task <id>` marks capture `prior` by itself; the
  ship skill marks a reused plan.
- `partial` - stopped after producing something durable (commits, an open PR, a deploy); see
  `conventions/failure.md` rule 7.
- `blocked` - ended by a decision, not breakage: a judge verdict (`steps/judge.md`) or a driven
  ask (`modes/driven.md`).

A stop state keeps its leaf; `note` records why and what unblocks it, one line each, for
`f10 status` (`/f10:status` in a session, `f10-state.sh show` without the binary).

**Cosmetic and best-effort.** Nothing reads this state back. If a badge call fails, ignore it:
never treat it as a step failure, retry it, mention it, or let it change the run. Send it in the
same message as the step's first real call, joined with `;` or beside a `Read`
(`conventions/latency.md`).
