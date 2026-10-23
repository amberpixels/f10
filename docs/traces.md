# Traces, version 1

What f10 leaves on the host for a reader of the history, in words that never name f10.
This file is the spec; `cli/internal/trace` is the one implementation, and `cli/internal/trace/testdata/trailers/` the conformance suite a second reader (a board, a browser extension) copies.
Everything here exists only with `traces: on` in `project.md`'s frontmatter (`docs/project-setup.md`); the default, `off`, writes none of it.

## Trailers on a commit

Every commit a step makes ends in git trailers, one `Key: value` per line, as the message's last paragraph.
`f10 trace trailers` prints them; a step appends its output as the last `-m` of its `git commit` and never writes the lines by hand.

| trailer | value | on |
|---|---|---|
| `Task: <id>` | the task id in the project's format (`FT-117`, `GH-22`) | every commit; a bundle writes one line per member, lead first |
| `Step: <step>` | the step whose work the commit carries | every commit |
| `Review: round=<n> fixed=<n> skipped=<n>` | the review round a resolve settled, and how many findings it fixed and skipped | a resolve's fix commit |

Rendered in that order: every `Task:`, then `Step:`, then `Review:`.

**`Step:` values.**
`implement` for the pipeline's code, whichever delivery step (`commit`, `push`, `pr`) made the commit, since those steps carry implement's work and have none of their own; `resolve` for a review's fixes; `judge` for a judge's changes after the PR; `catchup` for a merge commit or a fix-up that brings the base in; a project-defined step's own name for its commits.
A step name is a lowercase token (`[a-z][a-z0-9-]*`).

A commit with no task (a planless pipeline) carries `Step:` alone; no id is ever invented.

```
Guard the empty list

The walker read past a nil slice when a round found nothing.

Task: GH-12
Step: resolve
Review: round=1 fixed=1 skipped=1
```

## The commit on the default branch

A squash merge keeps one commit, with the host's message, so the branch commits' trailers would be lost.
`f10 finish` passes the commit's body on a squash or a merge commit: the PR's description, a blank line, then one `Task:` line per member of the task, and no `Step:`, since the merge is the host's commit, not a step's.
A rebase merge lands the branch commits as they are, trailers included, and passes nothing.

## Reading them back

A reader parses a whole commit message leniently, since a squash merge concatenates several messages and a trailer can sit mid-body under a `* subject` line:

- A trailer is a whole line matching `^(Task|Step|Review):[ \t]+(.+?)[ \t]*$`, the key compared case-insensitively, a trailing carriage return ignored.
  Nothing anchors it to the last paragraph.
- `Task:` values accumulate in order, one spelling per id, compared case-insensitively, the first spelling kept.
- Every `Step:` line is kept in order; the last is the commit's step.
- Every `Review:` line is kept in order; the last is the commit's review.
  Its value is `key=value` pairs separated by spaces; a pair with an unknown key, or a value that is not an integer, is ignored.
- A message with no trailer parses to nothing, never to an error.
  Nothing in a message can fail the parser.

## Conformance

`cli/internal/trace/testdata/trailers/<name>.msg` is a commit message as git prints it; `<name>.json` is what parsing it yields.
A reader passes when every `.msg` parses to its `.json`.
The set at version 1 covers a plain commit, a bundle, a resolve fix, a squash of two commits with trailers mid-body, a squash whose body is a PR description ending in `Task:`, a message with no trailers, lowercase keys, CRLF line ends, a `Review:` with an unknown pair and a non-integer, and one id spelled in two cases.

## Changing this spec

The vocabulary changes here and in the fixtures first, with a version bump at the top, then in the implementation; nothing a step writes by hand can change it.
Nothing in a trailer, a fixture or this file names f10, a plan or a findings file: that is what makes the trailers allowed under stealth (`conventions/context.md`).
The hidden state block in a PR body (FT-118) joins this document as part of version 1.
