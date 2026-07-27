# Convention · report - a step succeeded

`conventions/failure.md` fixes the shape of a step that cannot complete, and `modes/dry-run.md`
the shape of a resolution-only run. This file is the third: what a step or skill prints when it
**works**.

A run report is read once, in a terminal, usually to lift one value out of it - a task url to
open, a sha to cite, the plan path to hand to the next run. So the values get a column of their
own, and every url gets a marker in the column beside it.

## The facts block

Every success report opens with one aligned block:

```
task    GH-2
url   ↗ https://github.com/amberpixels/f10/issues/2
plan    .f10/plans/GH-2.md
```

- **Labels** left-aligned, padded to the widest label in *this* block. Two blocks are never
  printed side by side, so there is no global column width to match.
- After the padded label: **two spaces**, one **marker cell** (`↗` for a url, blank otherwise),
  **one space**, then the **value**.
- Values therefore all start in the same column, and every url in the report has its `↗` stacked
  in the column immediately before it. One glance down that column finds every link.

## Rules

1. **Urls bare.** Never `[text](url)`, never `<url>`, never backticks, never a trailing comma or
   period. The block is already fixed-width context, so any markup layer is one more thing that
   can render away - and a terminal that drops the href drops the url with it.
2. **Facts in the block, prose beneath it.** Rows carry addressable values only: task id, url,
   plan path, branch, commit sha, environment. What changed, how far the pipeline got, what the
   user does next - those are sentences under the block. Never widen a column to hold one.
3. **Only rows the run produced.** No placeholders, no `n/a`, no empty values. A step reports what
   it made and nothing else.
4. **Most-identifying first** - the task, then its url, then the artifacts this step produced.
5. **The marker means "an openable url"** and nothing else: never on a path, a sha, or a branch.
   It is `↗` (U+2197), not an emoji - `🔗` is double-width and misaligns the very column it is
   supposed to make scannable.

## Who reports under it

The steps that produce something addressable: `capture` (task + url), `plan` (the saved plan
path), `pr` (branch + PR/MR url), `push` (branch + sha), `deploy` (target + url). Skills report
the same way for the run as a whole - `/f10:ship`'s final report leads with the block, then
covers pipeline status in prose.

`implement` and `review` produce nothing addressable and stay prose.

`conventions/failure.md` and `modes/dry-run.md` keep their own blocks, on the same left-aligned
label column; they carry no marker cell because their values are prose, not artifacts. Where
either prints a url, rules 1 and 5 still apply.
