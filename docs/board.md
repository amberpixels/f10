# The board, version 2

What changed in a project over a calendar window (**the feed**, `view: feed`, the default), or where every task of it stands: done lately, in progress at which stage and who owes the next move, and what comes next (**the state**, `view: state`, `f10 board --state`).
`f10 board` prints it; `--json` prints the **Board JSON** this file specifies, `docs/board.schema.json` is its JSON Schema, and `cli/internal/lookaround` the one implementation.
A renderer (the terminal, the HTML report) draws the JSON and decides no group or stage; the board skill prints the terminal's drawing.

## Shape

The state view:

```json
{
  "version": 2,
  "generated_at": "2026-10-23T12:00:00Z",
  "view": "state",
  "project": { "id": "github.com/acme/widgets", "name": "widgets", "host": "gh", "checkout": "/code/widgets", "remote": false },
  "window": { "since": "2026-10-16T12:00:00Z" },
  "sources": [
    { "name": "host", "ok": true, "took_ms": 840 },
    { "name": "tracker", "ok": false, "error": "gh: To get started with GitHub CLI, please run:  gh auth login", "took_ms": 12 },
    { "name": "local", "ok": true, "took_ms": 30 }
  ],
  "rows": [
    {
      "key": "FT-71",
      "task": { "id": "FT-71", "title": "Rate limit the public API", "url": "https://…/issues/71", "status": "OPEN", "priority": "high" },
      "stage": "in-review",
      "stage_from": "trace",
      "who": { "agent": "ft-71-3f0312", "agent_status": "idle", "model": "opus", "author": "ann", "reviewers": ["bob"] },
      "where": { "pr": { "number": 80, "url": "https://…/pull/80", "state": "open", "head": "FT-71/rate-limit", "base": "main", "mergeable": "clean", "checks": "green" },
                 "branch": "FT-71/rate-limit", "worktree": "/code/widgets.FT-71-rate-limit", "workspace": "FT-71" },
      "review": { "step": "resolve", "round": 1, "open": 2 },
      "last_activity": "2026-10-22T09:12:00Z",
      "next": { "command": "/f10:resolve FT-71" }
    }
  ]
}
```

| field | meaning |
|---|---|
| `version` | `2`, this version of the spec |
| `generated_at` | when the board was derived |
| `view` | `feed` or `state` |
| `project` | `id` is the normalised remote (`github.com/org/repo`); `name` the main checkout's basename; `host` `gh` or `glab`; `checkout` the main checkout, absent with `--remote`; `remote` true when the local collector was skipped; `local_only` true for a checkout with no origin remote |
| `window` | `since`, the start of the feed's span or of the period the state's `done` rows cover; for the feed, `end` (exclusive; absent for a span from a time until now), `chosen` (the window named, `since <time>` for a time, or the default's pick: `today` or `last-workday`) and `fallback` (true when the default fell back because today has no activity yet) |
| `sources` | one entry per collector, in order `host`, `tracker`, `local`: `ok`, `skipped` (on purpose), `error` (why it failed), `notes` (what it could not read, the rest kept; `local only` on a local-only checkout's skipped host), `took_ms` |
| `rows` | the state's rows, in stage order (below), `next` rows by priority then the tracker's order, the rest by last activity; empty in the feed |
| `feed` | the feed's rows and quiet line (below); absent in the state |

**A row** is one task, or one request naming no task.

**Several projects** (`f10 board --all`, `--group`, more than one `-C`) print a JSON array of these documents, one per project in scope order; a project nothing could read is a document with every source failed and no rows.

**People.** `who.person` is the one name a row groups under: the tracker assignee, else the request's author, else the first committer, each mapped through the user's people map (`~/.f10/board.md`, `docs/cli.md`) where one person claims that identity.
With no map, or none claiming it, `person` is that identity as its source spelled it, in the same order: a tracker name on a task-only row, a host login on a request's row, an email on a row with only commits, so one person can sit under two headings until the map joins them.

| field | meaning |
|---|---|
| `key` | the task id in the project's format; `#<number>` for a request naming no task |
| `task` | the tracker's row: `id`, `title`, `url`, `status`, `priority` (`urgent`, `high`, `medium`, `low`), `assignee` (the first, where the tracker gives one), `after` (the base its body names), `created` (when the tracker created it, where it says); absent for a request naming no task |
| `stage`, `stage_from` | below |
| `who` | `agent` and `agent_status` (the Herdr agent in the task's worktree: `idle`, `working`, `done`), `model` (the one `f10 start --model` recorded on the branch), `author` (the request's), `assignees` (the tracker's first, then the request's), `reviewers` (requested and not yet heard from), `committers` (the distinct author emails on the row's commits, where they were read), `person` (below) |
| `where` | `pr` (`number`, `title`, `url`, `state` `open`, `merged` or `closed`, `draft`, `head`, `base`, `mergeable` `clean`, `conflicts` or `blocked`, `checks` `green`, `red` or `pending`), `branch`, `worktree`, `workspace` |
| `review` | `step` (the step the state block names), `round`, `open` (findings awaiting a verdict), `asks` (questions waiting on a person), `approvals`, `changes_requested`, `remote_open` (the declared remote reviewer left one unhandled); absent with nothing to say |
| `blocked_by` | the base task holding the row, when `stage` is `blocked` for that reason |
| `last_activity` | the latest the sources saw: the request's update or merge, a run's report, a findings file's write |
| `next` | `command`, a command to run as written (`f10 finish FT-71`, `/f10:resolve FT-71`), else `text`, one sentence (`waiting on review`) |

## Stages

Rows are listed in this order:

| stage | meaning |
|---|---|
| `asks-waiting` | a question waits on a person: a driven ask, or `asks` above zero in the state block |
| `blocked` | a run stopped (failed or blocked), a judge said rethink or stop, or the base task is not done |
| `ready` | an open request with nothing left: reviewed with no finding open, checks not red, mergeable |
| `in-review` | reviewed or being reviewed, something still open |
| `pr-open` | an open request nobody has reviewed |
| `implementing` | code being written: a live run before `pr`, or a draft request |
| `planned` | a plan saved, no code yet |
| `merged-unfinished` | merged in the window, its worktree, local branch or host branch still there; releases its dependents like `done` |
| `next` | open on the tracker, not started |
| `done` | merged in the window, nothing left behind |

**Where the evidence came from**, `stage_from`:

- `local`: this machine's state: a live run, a worktree or branch, a plan, findings or verdict file, the ledger.
- `trace`: what the project's traces left on the host (`docs/traces.md`): the state block in the request's body, the trailers on its commits.
- `tracker`: the tracker's list alone: a `next` row, an open task with nothing started.
- `guessed`: the host's own facts alone: the request's state, draft flag, reviews, review decision, checks, mergeable state, whether its branch still exists.

On a repository nothing ever traced, read with `--remote`, every in-progress row is `guessed`.
Traces upgrade a row to `trace`; local state to `local`.

## The rules

One row at a time, in this order; the first rule that matches decides the stage, and the rule's evidence decides `stage_from`.
A request is the row's **current** one: its open request, else the one merged latest in the window.
A request merged before the window, or closed unmerged, is no request.

1. **Merged** in the window: `merged-unfinished` when a worktree or local branch is still here (`local`), or the head branch still exists on the host (`guessed`), unless the ledger holds the task finished; else `done` (`local` when the local collector ran, `guessed` otherwise).
2. **Asks**: a live run holding an ask (`local`); the state block's `asks` above zero (`trace`).
3. **Stopped**: a live run failed or blocked (`local`); the latest verdict file says rethink or stop, no run is live, and the task is still open or has a request (`local`).
4. **Ready**: an open request, not a draft, mergeable, checks not red, and the state block says a review round ran with nothing open (`trace`), or, with no block, the host's review decision is approved (`guessed`).
5. **In review**: a live run at `review`, `resolve` or `judge` (`local`); the latest findings file has open findings and the request is open (`local`); the block's step is one of those with `open` above zero (`trace`); a review submitted, a reviewer requested, changes requested, or the declared remote reviewer's review unhandled (`guessed`).
6. **Open**: an open request, not a draft: `trace` with a state block, `guessed` without.
7. **Implementing**: a live run in `ship` (`local`); a draft request (`guessed`); a worktree with commits past the default branch, for a task the tracker lists as open (`local`).
8. **Planned**: a plan file for a task the tracker lists as open, or a live run in `plan` (`local`).
9. **Next**: a task the tracker lists as open (`tracker`).
10. Nothing above: not a row.

**The base rule** runs after, in dependency order: a row whose base task (the `f10-after` the branch records, else the block's `after`, else the task body's `After:`) has a row neither `done` nor `merged-unfinished` is `blocked` with that base in `blocked_by`, unless a run is working on it or it is already done, merged or asking; `stage_from` is the base link's source.
Merging releases a dependent; the base's leftover worktree or branch is its own chore.
A base with no row on the board counts as done, so a reader with the checkout and one without agree.

## Next actions

| stage | next |
|---|---|
| `done` | none |
| `merged-unfinished`, `ready` | `f10 finish <id>` |
| `asks-waiting` | `/f10:status <id>` for a local ask; `waiting on an answer` for a traced one |
| `blocked` | `waiting on <base>`; the run's phase, status, step and note; `/f10:plan <id>` after rethink; `judged stop` |
| `in-review` | `/f10:resolve <id> ci` with a remote review unhandled; `/f10:resolve <id>` with findings open; `changes requested`; `at <step>` for a live run; else `waiting on review` |
| `pr-open` | `/f10:review <id>` |
| `implementing` | `at <step>` for a live run; `/f10:ship <id>` for a worktree; `in progress` for a draft |
| `planned` | `/f10:ship <id>` with a worktree, else `f10 start <id>` |
| `next` | `f10 start <id>` |

A row with no task id gets a sentence where a command would need one.

## The feed

```json
"feed": {
  "rows": [
    {
      "group": "started", "key": "FT-71", "task": { "id": "FT-71", "title": "Rate limit the public API" },
      "pr": { "number": 84, "url": "https://…/pull/84", "state": "open", "head": "FT-71/again", "base": "main" },
      "from": "host", "who": { "author": "ann", "person": "ann" }, "owes": "bob",
      "follow_up_of": [80], "summary": "second request on FT-71", "at": "2026-10-26T09:00:00Z"
    }
  ],
  "quiet": { "count": 12, "oldest": "2026-06-01T00:00:00Z" }
}
```

| field | meaning |
|---|---|
| `group` | `started`, `updated`, `reviewed`, `finished` or `new-tasks`; rows list in that order, the latest first within a group |
| `key` | the task id the request names, as a state row's; `#<number>` for a request naming none; a branch name for a local-only checkout's row naming none |
| `task`, `pr` | the tracker's row where the tracker lists the task; the request, as `where.pr` |
| `from` | where the group came from: `host` (a merge, a close, an opening), `trace` (a `Step:` trailer on a commit in the window), `tracker` (a new task), `local` (a local-only checkout's branch), `guessed` (the host's other facts) |
| `who` | `author`, `assignees`, `person` as a state row's |
| `owes` | who the next move waits on: the author after a review's verdict, a reviewer requested in the window and not heard from since |
| `abandoned` | closed without merging |
| `follow_up_of` | the task's other requests, opened before this one |
| `summary` | what happened, one sentence |
| `at` | the latest event in the window |
| `quiet` | `count`, the open requests with no activity in the window, and `oldest`, the earliest creation among them |

**The window.** The default is `today`, or the last working day while nothing happened today; every span is cut by [years](https://github.com/amberpixels/years), never by date code of f10's own.
Over a scope every project shares the span.

**The groups.** One request is one row, in the first group whose rule matches, its events read only where they fall in the window:

1. **Finished**: merged in the window (`merged`, `merged by <login>` when another merged it), or closed unmerged there (`abandoned`); `host`.
2. **Started**: opened in the window, or out of draft there; `host`.
   A request naming a task that already has another request, opened before it, open or merged, is a follow-up: `second request on <task>`.
3. **Reviewed**: a review approving or requesting changes submitted in the window, naming the reviewer, the author owing the next move (`guessed`); or a commit there whose `Step:` trailer names `review`, `resolve` or `judge` (`trace`).
4. **Updated**: commits, merges from the base (a commit with more than one parent) or force pushes in the window, counted, or a review requested there; `trace` when a commit's `Step:` trailer names the step, else `guessed`.
5. Nothing in the window (a comment, a label, activity outside it): no row; an open request counts in `quiet`.

**New tasks** are the tracker's open tasks whose `created` falls in the window: the host's issues' creation time, or a driver row's optional `created` (`docs/driver-contract.md`).
`merged-unfinished` never appears in the feed: its author sees it in `--state` and `f10 status`.

## Sources

Each collector fetches and interprets nothing, and fails on its own: a failed source carries its error and every other row still renders.
In the state view, the host collector reads every open request and every one merged since the window, the commits of a request nothing else names a task for (for the trailers' task), every branch the host has once a request merged in the window (for whether each merged head is still there), and the declared remote reviewer's unhandled review.
In the feed, it reads the requests updated since the window's start with their timelines in one paged query, the count of open requests nobody touched, and, for the tasks a request started there, their other requests in one more; nothing per request.
The tracker collector reads open tasks through the driver's `list`, else the host's issues.
The local collector, in the state view, reads the worktrees and branches with what `f10 start` recorded on them, the live runs in them, plan, findings and verdict files under the storage root, what the ledger closed, and Herdr's workspaces; the feed skips it, but on a local-only checkout, where it reads each branch's last commit time.
A local-only checkout skips the host, and the tracker unless a driver answers it.
Nothing is ever written to the host.

## Conformance

`cli/internal/lookaround/testdata/derive/<name>.facts.json` is one collector result; `<name>.board.json` the board it derives to at a fixed clock.
`cli/internal/lookaround/testdata/feed/<name>.board.json` is a feed derived from facts the test builds.
`cli/cmd/f10/testdata/board/<name>.json` is a recorded run: the git and host calls with their answers, tracker rows and local state, beside the board out; a `feed` one names its window, cut by years at the fixture's clock, and fails on any call it does not record.

## Changing this spec

A stage rule, a field's meaning or the next-action table changes here and in the fixtures first, with a version bump at the top, then in the implementation.
An optional field added with its meaning (`who.committers`, `who.person`) is written here and in the schema, the version kept.
Nothing in a board names a plan, a findings file or a storage path except `where.worktree`, which is the reader's own machine.
