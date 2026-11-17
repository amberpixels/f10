# Step · review - the diff to a findings file

Input: the diff against its base, the task text when a task id is known, and the prior round's findings file when one exists.
Output: the **findings file**, and nothing else.
`steps/resolve.md` settles it.
Context: `project.md → Review`.
It adds categories and lists what never to flag, and it names who answers an `ask`.
Badge: none.
Inside a ship pipeline, the leaf is `review` for every reviewer.

This step picks the reviewers and owns what they share.
Each reviewer's procedure is a step of its own, loaded only when that reviewer runs, because the whole review does not fit one load.

## The reviewers

The flow entry or the standalone argument names who reviews:

- **The blind reviewer**: `review`, `review:local`, or `/f10:review` with no reviewer named.
  A fresh agent reads the diff, as **The blind reviewer** below says.
- **The host engine**: `review:host` or `/f10:review host`, which runs the host's own code review.
  It does not combine with `ci`.
  Load `f10-load-project.sh review-host` and follow it.
- **The CI review**: `review:ci` or `/f10:review ci`, which reads the remote review that arrived on the PR or MR.
  Load `f10-load-project.sh review-ci` and follow it.
- **Both**: `review:local+ci` writes the blind reviewer and the CI review into one round, as the review-ci step's **Local and CI** says.

A `?` after `ci` makes the CI review optional.
`f10 flow <name>` settles it before the entry reaches this step, keeping the CI review only when Review declares a remote reviewer and a `pr` or `push` comes before the entry.
A `?` that still reaches this step, from a binary too old to settle it, is settled here by the same rule, and the report says so in one line.

**Only the change checked out here.**
Whatever the argument names, the change reviewed is always the current branch against its base.
The argument only says which task the change belongs to:

- A task id names the task, which `f10 task read <id>` reads.
  It comes in the project's id format, as an id or as a tracker url.
- A PR or MR names its head branch and its task, which `f10 pr view <PR>` prints on its `branch:` and `task:` lines.
  It comes as a number, a url or "this branch".

When the branch of that task or PR is not the current one, decline in one line.
Name the worktree that holds the branch (`git worktree list`), or say that none does.
That decline is a normal end.

## The findings file

**Path.**
The file is `<storage root>/reviews/<TASK-ID>/<round>.md`, and `f10 review round` prints its parts:

- `round:` and `path:` - this round's number and file, `1.md` or `2.md`, because there is no third round
- `latest:` - the latest round's file
- `dir:` - the directory, named by the task id, else by the branch name.
  Create it when it is missing.
- `archive:` - where the directory's earlier rounds moved, because the PR they reviewed merged or closed
- `pr:` - the value for the header's `pr:` line

A directory holds one series of rounds, and a series belongs to one PR.
So a follow-up PR on the same task starts again at round 1.
`f10 finish` and the janitor archive the directory too, to `reviews/archive/<TASK-ID>.<N>/`.

**Shape.**
Every findings file has the same shape, whoever reviewed it:

```markdown
task: GH-13
round: 2
reviewed: e6af491 dirty
base: main e6af491
pr: #204
reviewer: local
verdict: findings

## Prior round

### 1.3 Asks fire once per round, not once per entry - explanation accepted
The reason cites the pipeline rule that a round with a fix holds its asks.
where: steps/resolve.md:62
verdict: skip +reply
reason: accepted as written; nothing to send, the reviewer is local

## Categories

- correctness and logic: 1
- architecture and guidelines: none found
- security and performance: none found
- tests and docs: none found

## Findings

### 2.1 A second review on an unsettled round burns the last round
summary: the round number comes from the file count, so a review before any resolve writes 2.md
where: steps/review.md:128
category: correctness and logic
failure: /f10:review run twice before any resolve writes 2.md, and round two can only say still open
confidence: confirmed
Round two exists so fixes get read; written before a resolve it can only say still open for
every id. Fix: decline when 1.md holds findings without a verdict.
verdict: fix
reason: the Rounds section now declines on an unsettled round one
```

The **header** holds one field per line in the example's order.
These rules are what the example does not show:

- `task:` is the branch name when there is no task id.
- `reviewed:` adds `dirty` when the working tree differed from HEAD, as in a flow like `implement → review → pr`.
- `pr:` is `none` while the branch has no open PR.
  Every round writes it, because it says which series the round belongs to.
- `reviewer:` is `local`, or the remote identity that the frontmatter's `review.reviewer` declares.
  A round both reviews wrote lists both, as `local, claude[bot]`.
- Optional lines go between `reviewer:` and `verdict:`, in this order:
  - `engine: host <level>` on a host engine round
  - `depth: light` on a light round (the review-local step's **Depth**)
  - `review:` on a remote round, the url where a `+reply` posts
  - `revision:` on a remote round, pick's revision of the review's text
- `verdict:` is `clean` when every category found nothing.

The sections follow the header:

- **`## Prior round`** comes only in round two, with one block per round-one id.
  The block's heading is the finding's title, then ` - `, then one status: `fixed`, `still open`, `fix introduced a problem`, `explanation accepted` or `explanation disputed`.
  A line of evidence follows, then a `where:` line, which `fix introduced a problem` requires.
  Three statuses **reopen** the finding, so it needs a verdict again: `still open`, `fix introduced a problem` and `explanation disputed`.
  A finding's last verdict is the one under the latest block that names its id.
- **`## Categories`** lists the four core categories first, then the project's, each with a count or `none found`.
  The core categories are **correctness and logic**, **architecture and guidelines**, **security and performance**, and **tests and docs**.
- **`## Findings`** holds one block per finding, its lines in the example's order.
  A `lore: <id>` line follows `confidence:` once the lore entry exists.
  `failure:` is one line: a concrete input or state, and the wrong output, crash or cost it leads to.
  `confidence:` is `confirmed` when the reviewer traced that failure through the code.
  It is `plausible` when the scenario is concrete but one link in it could not be traced, and the body then names that link.
  The body says what is wrong, why it matters, and a concrete fix.

Two more kinds of line:

- In a file both reviews wrote, every block carries `source:`, either `local` or `remote`, and a prior block keeps the `source:` of its round-one block.
  A block without `source:` belongs to the one reviewer the header names.
- Resolve appends `verdict:`, with its `+reply` or `+note` attachments, and `reason:`.
  A block holds **one** `verdict:`, and resolve rewrites it when an `ask` is answered.

A malformed file fails the step.
A file is malformed when it has no header, when a finding lacks its id, `where:` or `category:`, or when a round-two file is missing a round-one id.
A block without `failure:` or `confidence:` is not malformed, because older files and remote reviews may give neither.

## The base and the diff

The base is what `f10 pr base` prints: the open PR's target, else the branch `f10 start --after` recorded for a stacked task, else the default branch.
The diff is `f10 pr diff`, plus every file `git ls-files --others --exclude-standard` lists.
`f10 pr diff` compares the base with the base after the working tree's work is merged into it.
So a change main already holds drops out, even after a squash merge, and a follow-up PR on a reused branch shows only its new commits.
A branch that conflicts with its base is diffed from the point where it left the base instead.
Read an untracked text file whole when it is under a few hundred KB.
Leave a larger file, or a binary one (a `Binary files differ` line), unread.
List it as one tests-and-docs finding: "not reviewed: `<path>`, `<size>`, untracked".
Read the full diff every round.

## The blind reviewer

The main agent first gathers these facts:

- the checkout root, the base ref and its sha, and the diff command
- the task id and its text (`f10 task read <id>`)
- the round number, the prior round's path and the output path.
  `f10 review round` prints them as `round:`, `latest:` and `path:`.
- the `pr:` value for the header
- each lesson that implement's hand-off in this run said it overrode, with the lesson id, its path and the reason.
  There are none when the hand-off noted none, or when the review runs standalone.
- the review roles: the base role where Roles replaces it, and the added roles.
  They come from the sources that `conventions/context.md` → Roles ranks, the plan's `Roles` line first.
- the depth: `full`, or `light` when the flow entry carries a `depth light` dial from the run's pace
- whether the CI review joins this round (`local+ci`)

It then spawns **one fresh `general-purpose` agent** and hands it those facts.
Never use `fork`, because a fork inherits the whole conversation.
The agent's instruction is to run `f10-load-conventions.sh` and `f10-load-project.sh review review-local` as two Bash calls, then follow the **review-local** step.
The Agent tool call carries the `model` and `effort` that the load's `review` profile line names (`conventions/context.md` → Agent profiles).

When the agent returns, the main agent checks the file's header, and in round two it checks that every prior id has a status.
It then relays the agent's summary unchanged.

## Lore and the state block

Every reviewer writes the lore entries for the blocks it wrote, once its findings file is on disk.
The main agent then writes one state block per round, once the file is final:

- after the blind reviewer returns and its file passes the main agent's checks
- after the host engine's lore entries
- after the CI review's ack, or after a stop that ends the CI review, which in a `local+ci` round is the round's only state block

**Lore** (`docs/lore.md`) needs a task id, so a round keyed by a branch name writes none.
Run `f10 lore change` once after `f10 review round`.
It prints the `<change>`: the task id for the task's first PR, and `<id>.<n>` for a later one.

- Each finding this round adds gets `f10 lore add finding --change <change> --source <local|remote> --severity <s> --status open --anchor <where> -m <summary>`.
  `--source` is the block's source, as **Two more kinds of line** says.
  Whoever wrote the block picks the severity: `blocker`, `major`, `minor` or `nit`, and `minor` when it cannot tell.
- Each reopened prior block gets `f10 lore add finding --change <change> --supersedes <its round-one block's lore id> --status open -m <the evidence line>`.
  A round-one block with no `lore:` line writes nothing.

Each call prints the new entry's id, which becomes the block's `lore:` line, so resolve can supersede that entry later.
A failing `lore add` is one line in the report and never a failed step, because the findings file stays the record that `f10 status` falls back to.

**The state block** (`docs/traces.md`) is `f10 trace state --step review --round <n> --open <count>`.
The count is this round's findings plus the reopened prior blocks, so a clean round passes `--open 0`.
No block is written with `traces: off`, or when no PR or MR is open.
A non-zero exit marks the run `partial` (`conventions/failure.md` rule 7), and is never a failed step.

## Rounds

**One round is the default.**
A `rounds` dial on the flow entry, from the run's pace, comes first.
`rounds 1` means one round, whatever round one's resolve fixed.
`rounds 2` means round two whenever round one's resolve fixed something.
Without the dial, round two runs only when round one was heavy: its resolve fixed six or more findings.

Otherwise, never run, suggest or offer a second round.
A resolve that settles every finding, with verify green, ends the review.
A user who asks for round two by name gets it.

When `f10 review round` exits 3, that is a **decline**, for one of three reasons:

- The branch adds nothing to its base ("already merged, nothing to review"), as after its PR was squash-merged.
- Round one still holds findings without a verdict, so a round two could only say `still open`.
- Round two already exists.
Print its `decline:` line and the findings it names.
This is a normal end.
Never delete or rewrite a round.

**Inside a ship pipeline**, a `review` entry runs this step and then `steps/resolve.md`.
One resolve settles each round.
Round two runs the review and its resolve again only when round two applies (above), and that resolve recorded a `fix` of its own before any questionnaire.
Otherwise the first round ends the entry, and a fix chosen in the final questionnaire never opens round two.
The `ask` verdicts of both rounds go to the user in resolve's one questionnaire, before the next pipeline step.
With `traces: on`, the entry's trace on the PR is made the same way, by the entry's last resolve, a clean entry's included.

**Report** per `conventions/report.md`: a `findings` row, then the verdict and the count per category, in the reviewer's words.

**On failure:** the reviewer never returns, writes no file, or writes a malformed one (`conventions/failure.md`).
