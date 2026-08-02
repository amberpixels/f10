# Convention · report - a step succeeded

What a step or skill prints when it **works** - `conventions/failure.md` and `modes/dry-run.md`
fix the other two shapes.

A run report is read once, in a terminal, usually to lift one value out of it - a task url to
open, a sha to cite, the plan path to hand to the next run. So the values get a column of their
own, and every url gets a marker in the column beside it.

## The facts block

Every success report opens with one aligned block, fenced and tagged `yaml` so a terminal colors
labels and values distinctly:

```yaml
task:    GH-2
url:   ↗ https://github.com/amberpixels/f10/issues/2
plan:    .f10/plans/GH-2.md
```

- **Labels** left-aligned, padded to the widest label **including its colon** in *this* block
  (blocks are never printed side by side, so there is no global width to match).
- After the padded label: **two spaces**, one **marker cell** (`↗` for a url, blank otherwise),
  **one space**, then the **value**. The colon belongs to the label, not to that spacing. So
  values all start in the same column, and one glance down the marker column finds every link.
- **The tag is for the terminal, not for a parser.** It buys the colors; it does not make the
  block data - nothing reads these values back, which is what lets `url`'s value carry the
  arrow. If a value would break the highlighting, print the block untagged.

## Rules

1. **Urls bare.** Never `[text](url)`, never `<url>`, never backticks, never a trailing comma or
   period. Any markup layer is one more thing that can render away - and a terminal that drops
   the href drops the url with it. (The fence's info string never touches the url text, so the
   `yaml` tag is exempt.)
2. **Facts in the block, prose beneath it.** Rows carry addressable values only: task id, url,
   plan path, branch, commit sha, environment. What changed, how far the pipeline got, what the
   user does next - those are sentences under the block. Never widen a column to hold one.
3. **Only rows the run produced.** No placeholders, no `n/a`, no empty values.
4. **Most-identifying first** - the task, then its url, then the artifacts this step produced.
5. **The marker means "an openable url"** and nothing else: never on a path, a sha, or a branch.
   It is `↗` (U+2197), not an emoji - `🔗` is double-width and misaligns the very column it is
   supposed to make scannable.

## The status-line badge

While a run is still going, the same facts have a second surface: three glyphs in the
terminal's status line - capture, plan, ship. Steps keep it current with one call as they enter
and leave their phase:

```
f10-state.sh set <capture|plan|ship> <running|done|failed|partial|prior> [<leaf>]
f10-state.sh task <id> [<url>]
```

The plugin's `bin/` is on the Bash tool's `PATH`, so the bare name is the whole command. Each
step file states what it reports. Entering a phase retires the ones before it by itself;
`/f10:ship` also passes the pipeline step it is on as the `<leaf>` - the one thing three glyphs
cannot say. `prior` marks a phase whose work exists from an earlier run - a reused plan, a
task already in the tracker: done, just not by this run. Reporting the task id
(`task <id>`) marks capture `prior` by itself; a reused plan is the ship skill's to say.
`partial` marks a phase that stopped after this run produced something durable - commits, an
open PR, a deploy; `conventions/failure.md` rule 7 draws the line between it and `failed`.

**Cosmetic and best-effort, everywhere.** Nothing in the pipeline reads this state back - it
draws a badge. A missing command, a failed write, a phase nobody reported: each costs one glyph
and nothing else. Never treat one as a step failure, never retry it, never mention it to the
user, and never let it change what the run does. A run whose badge is wrong is still a correct
run.

## Who reports under it

The steps that produce something addressable: `capture` (task + url), `plan` (the saved plan
path), `pr` (branch + PR/MR url), `push` (branch + sha), `deploy` (target + url). Skills report
the same way for the run as a whole. `implement` and `review` produce nothing addressable and
stay prose.

`conventions/failure.md` and `modes/dry-run.md` keep their own blocks: the same left-aligned
label column and bare-url rules (1 and 5), but **plain-fenced and colonless**, with no marker
cell - their values are prose, and prose breaks yaml (a second colon in an `error` row, a value
opening with `|`). The success block is safe only because rule 2 confines it to addressable
values.
