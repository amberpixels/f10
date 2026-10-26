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

A commit with no task (free-text planless work) carries `Step:` alone; no id is ever invented.

```
Guard the empty list

The walker read past a nil slice when a round found nothing.

Task: GH-12
Step: resolve
Review: round=1 fixed=1 skipped=1
```

## The commit on the default branch

A squash merge keeps one commit, with the host's message, so the branch commits' trailers would be lost.
`f10 finish` passes the commit's body on a squash, and on a merge commit on GitHub: the PR's description as the host holds it at the merge, a blank line, then one `Task:` line per member of the task, and no `Step:`, since the merge is the host's commit, not a step's.
A rebase merge lands the branch commits as they are, trailers included, and passes nothing; so does a GitLab merge commit, whose message the project fixes.

## The state block in a PR body

Trailers arrive with a push and never change, so they cannot say where a task stands now.
The PR body carries that in one hidden HTML comment, on a line of its own, rewritten by each step that runs after the PR is open:

```
<!-- state {"v":1,"task":"FT-71","step":"resolve","round":1,"open":2,"asks":1,"after":"FT-70"} -->
```

`f10 trace state` rewrites it; a step calls the command and never edits the block by hand.
The JSON is one line, the keys in this order, no spaces:

| key | value | when written |
|---|---|---|
| `v` | `1`, this version of the spec | always |
| `task` | the task id in the project's format, a bundle's lead | the branch names a task |
| `riders` | the bundle's other members, in order | a bundle |
| `step` | the step that wrote the block: `pr`, `review`, `resolve`, `judge`, `finish`, or a project-defined step | always |
| `round` | the review round the counts describe | a review ran |
| `open` | findings awaiting a verdict | above zero |
| `asks` | questions waiting on a person outside the run's own turn: a driven ask, a finding parked for a remote answer, a judge's rethink | above zero |
| `after` | the base task's id, the one `f10 start --after` recorded on the branch | the branch has one |

**A rewrite merges.**
`step` is set every time; `task`, `riders` and `after` are read from the branch every time; `round`, `open` and `asks` keep the value the block already holds unless the step passes them, and an explicit `0` clears.
So a `pr` refresh or `f10 finish`, which know nothing about findings, leave a review's counts standing.

**Where it sits.**
A body without a block gets one appended after a blank line.
A rewrite replaces the first block where it is, removes any later one, and changes no other byte.
A `pr` refresh that drafts the description from scratch puts the block into the draft with `--body-file`, its counts carried from the body the host holds.
With `traces: off` the same call writes no block and copies the one the body holds into the draft as it is, so turning the switch off never drops the last state.

**Who writes it.**

- `pr`, when it opens or refreshes the PR.
- `review`, with the round and the findings awaiting a verdict.
- `resolve`, every resolve, with what is still open and what waits on a person.
- `judge` after the PR; a driven rethink raises `asks` until its answer.
- `f10 finish`, with `step: finish` just before the merge: the last state the PR keeps.

A branch with no PR yet, a PR no longer open, or one someone else opened is left alone: the command says so and exits 0, so a step calls it without checking the PR first.

**The commit never carries it.**
The squash or merge commit `f10 finish` lands takes the PR's description with the block stripped out: the block describes the PR, the trailers describe the commit.

## Reading them back

A reader parses a whole commit message leniently, since a squash merge concatenates several messages and a trailer can sit mid-body under a `* subject` line:

- A trailer is a whole line matching `^(Task|Step|Review):[ \t]+(\S.*?)[ \t]*$`, the key compared case-insensitively, a trailing carriage return ignored; a blank value is no trailer.
  Nothing anchors it to the last paragraph.
- `Task:` values accumulate in order, one spelling per id, compared case-insensitively, the first spelling kept.
- Every `Step:` line is kept in order; the last is the commit's step.
- Every `Review:` line is kept in order; the last is the commit's review.
  Its value is `key=value` pairs separated by spaces; a pair with an unknown key, or a value that is not an integer, is ignored.
- A message with no trailer parses to nothing, never to an error.
  Nothing in a message can fail the parser.

A reader of a PR body takes the first `<!-- state {...} -->` it holds, the marker's spaces free, and reads the JSON:

- A block inside a fenced code block or a backtick span is text, not the state: a body quoting this spec keeps its example.
- No block, JSON that does not parse, a `v` other than `1`, or a count below zero is no state, never an error.
- A key the reader does not know is ignored; a key missing is its zero.

## Conformance

`cli/internal/trace/testdata/trailers/<name>.msg` is a commit message as git prints it; `<name>.json` is what parsing it yields.
A reader passes when every `.msg` parses to its `.json`.
The set at version 1 covers a plain commit, a bundle, a resolve fix, a squash of two commits with trailers mid-body, a squash whose body is a PR description ending in `Task:`, a message with no trailers, lowercase keys, CRLF line ends, a `Review:` with an unknown pair and a non-integer, one id spelled in two cases, and blank values.

`cli/internal/trace/testdata/state/<name>.md` is a PR body as the host returns it; `<name>.json` is the state it yields, or `null` for no state.
The set at version 1 covers a block at the end, one mid-body, no block, malformed JSON, another version, CRLF line ends, an unknown key, a bundle with riders and a base, two blocks, the smallest block, a marker with extra spaces, a block naming no task, a fenced example beside the block, a fenced example alone, and a count below zero.

## Changing this spec

The vocabulary changes here and in the fixtures first, with a version bump at the top, then in the implementation; nothing a step writes by hand can change it.
Nothing in a trailer, a block, a fixture or this file names f10, a plan or a findings file: that is what makes them allowed under stealth (`conventions/context.md`).
Version 1 covers the trailers and the state block together.
