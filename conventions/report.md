# Convention · report - a step succeeded

`conventions/failure.md` fixes the shape of a step that cannot complete, and `modes/dry-run.md`
the shape of a resolution-only run. This file is the third: what a step or skill prints when it
**works**.

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

- **Labels** left-aligned, padded to the widest label **including its colon** in *this* block. Two
  blocks are never printed side by side, so there is no global column width to match.
- After the padded label: **two spaces**, one **marker cell** (`↗` for a url, blank otherwise),
  **one space**, then the **value**. The colon belongs to the label, not to that spacing.
- Values therefore all start in the same column, and every url in the report has its `↗` stacked
  in the column immediately before it. One glance down that column finds every link.
- **The tag is for the terminal, not for a parser.** It buys the colors; it does not make the block
  data. Nothing reads these values back (rule 2), which is what lets `url`'s value be
  `↗ https://…`, arrow included, without that costing anything real.

## Rules

1. **Urls bare.** Never `[text](url)`, never `<url>`, never backticks, never a trailing comma or
   period. The block is already fixed-width context, so any markup layer is one more thing that
   can render away - and a terminal that drops the href drops the url with it.
   The fence's `yaml` tag is the one exception, and only because it is not that kind of layer: an
   info string never touches the url text, so a highlighter that fails, or a terminal that cannot
   color at all, degrades to exactly the plain block. Where a value would break the highlighting
   anyway, print the block untagged - the plain form is always correct.
2. **Facts in the block, prose beneath it.** Rows carry addressable values only: task id, url,
   plan path, branch, commit sha, environment. What changed, how far the pipeline got, what the
   user does next - those are sentences under the block. Never widen a column to hold one.
3. **Only rows the run produced.** No placeholders, no `n/a`, no empty values. A step reports what
   it made and nothing else.
4. **Most-identifying first** - the task, then its url, then the artifacts this step produced.
5. **The marker means "an openable url"** and nothing else: never on a path, a sha, or a branch.
   It is `↗` (U+2197), not an emoji - `🔗` is double-width and misaligns the very column it is
   supposed to make scannable. Highlighting does not retire it: the tag colors **values**, not
   kinds of value, so a url renders exactly like a branch and only the marker separates them.

## The status-line badge

A report is read once, at the end. While a run is still going, the same facts have a second
surface: three glyphs in the terminal's status line - capture, plan, ship - with the task id beside
them. Steps keep it current with one call as they enter and leave their phase:

```
f10-state.sh set <capture|plan|ship> <running|done|failed> [<leaf>]
f10-state.sh task <id> [<url>]
```

The plugin's `bin/` is on the Bash tool's `PATH`, so the bare name is the whole command. Which
step reports which phase is stated in the step files. Entering a phase retires the ones before it
by itself, so a run that starts at `ship` never needs to say that it did not capture; `/f10:ship`
does pass the pipeline step it is on as the `<leaf>`, which is the one thing three glyphs cannot
say.

**Cosmetic and best-effort, everywhere.** Nothing in the pipeline reads this state back to decide
anything - it draws a badge. A missing command, a failed write, a phase nobody reported: each
costs one glyph and nothing else. Never treat one as a step failure, never retry it, never mention
it to the user, and never let it change what the run does or the order it does it in. A run whose
badge is wrong is still a correct run.

## Who reports under it

The steps that produce something addressable: `capture` (task + url), `plan` (the saved plan
path), `pr` (branch + PR/MR url), `push` (branch + sha), `deploy` (target + url). Skills report
the same way for the run as a whole - `/f10:ship`'s final report leads with the block, then
covers pipeline status in prose.

`implement` and `review` produce nothing addressable and stay prose.

`conventions/failure.md` and `modes/dry-run.md` keep their own blocks. They share this one's
left-aligned label column and its bare-url rules, but stay **plain-fenced and colonless**, and
carry no marker cell - all three for the same reason: their values are prose, not artifacts. Prose
breaks yaml, and not hypothetically. An `error` row reading `Could not connect: timeout` carries a
second colon, and a dry-run value opening with `|` is a block scalar. The success block is safe from
both because rule 2 confines it to addressable values, and `https://` survives because YAML splits
on a colon only where a space follows it. Where either block prints a url, rules 1 and 5 still
apply.
