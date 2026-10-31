# Lore, version 1

Code loses its "why".
The reasons behind a change live in PR threads, plan files and agent sessions that are gone by the time someone, human or agent, is about to edit the line.
Lore keeps that knowledge as plain markdown entries next to the code, linked to commits by a trailer, and answers two questions:

- **why is this line the way it is** (`f10 lore why <path:line>`)
- **what must I know before editing this file** (`f10 lore lessons <path>`)

This file is the spec.
`cli/internal/lore` is the implementation and `f10 lore` its CLI.
The package imports nothing else from f10, so it can be copied out as a standalone tool.

## Glossary

- **Change**: the unit of work, one PR.
  Its id is the task id (`FT-138`) for the task's first PR, and `<id>.<n>` for its `n`-th (`FT-138.2`), because each PR is reviewed, judged and checked on its own.
  f10 counts a task's PRs from the review series `f10 finish` archives, and `f10 lore change` prints the id the current one writes to.
  It is a directory holding every entry for that change.
- **Entry kinds**, following the life of a change:
  - `intent`: what we are doing and why, written before code.
  - `decision`: a choice made: the option taken, the options rejected, and why.
    A catchup's decision body opens `catchup: <path>`, which the pr step renders into the PR's Catchup section.
  - `finding`: something a reviewer challenged: where, how severe, and how it was settled.
  - `verdict`: a gate's judgment on the change at a given commit.
  - `lesson`: lasting knowledge anchored to code, which outlives the change.
- **Anchor**: where an entry points in the code: `path:line[-end]@commit`, plus an optional symbol.
- **Trailer**: `Change: <id>` on a commit, the link from code to its change.
  f10's `Task: <id>` trailer is accepted as an alias.
  f10 writes `Change:` beside `Task:` only from a task's second PR on, on its commits and its squash commit, because `Task:` alone would lead to the first PR's change.

## Rules

- **Entries are immutable.**
  A correction or a status change is a new entry with `supersedes: <id>`, and the latest entry in a chain is the effective one.
  Settling a finding is a new `finding` entry with `status: fixed` that supersedes the original.
- **One file per entry**, at `<root>/<change-id>/<ulid>-<kind>.md`, so concurrent branches never conflict.
  The id is the ULID in the filename, never a frontmatter field.
- **Every entry has a `human`**, the accountable person, taken from `git config user.email`.
  `agent` is optional and set only when an agent did the work.
- **Plain files are the source of truth.**
  Nothing is written but the entry files; everything shown is computed from them and from git.
- **The root is a parameter.**
  The package takes it; where f10 points it is under [Where the root lives](#where-the-root-lives).

### Supersedes

- The target must exist in the same change and be of the same kind.
- A superseding `finding` that leaves out `severity`, `source` or its anchors takes them from the entry it supersedes, so a settle names only its new status, resolution and body.
- An entry is **effective** when nothing supersedes it: a chain `A <- B <- C` leaves `C`.
- Superseding an entry that is already superseded forks the chain.
  `f10 lore add` refuses it and names the chain's head; a fork that arrives anyway (two branches each settling the same finding) is a `validate` error, and reads keep the later entry by `at`, then by id.
- A dangling `supersedes` (the target is missing) is a `validate` error; reads treat the entry as effective.
- `retired: true` is allowed only on a superseding `lesson`.
  A retired lesson ends its chain: `lessons` and `why` no longer show it.
- A change has one effective `intent`, the entry `show` and `why` print first.
  A second one must supersede it; `f10 lore add` refuses one that does not, naming the standing intent's id.

## Entry format

YAML frontmatter, then a markdown body.
The body is free markdown, the human-readable reason, and is never empty.
An unknown frontmatter field is an error.

Common fields:

| field | value | |
|---|---|---|
| `kind` | `intent`, `decision`, `finding`, `verdict` or `lesson`; matches the filename | required |
| `change` | the change id; matches the directory | required |
| `human` | the accountable person's email | required |
| `agent` | `model`, `via`, `session`, each optional | optional |
| `at` | UTC, RFC 3339, seconds | required |
| `anchors` | a list of `path`, `line`, `end_line`, `commit`, `symbol`; `path` repo-relative, `line` from 1, `end_line` not before `line`, `commit` a sha; `end_line` and `symbol` optional | optional, except on `lesson` |
| `supersedes` | the id of the entry this one replaces | optional |

Fields per kind; a kind's field on another kind is an error:

| kind | fields |
|---|---|
| `intent` | none |
| `decision` | `chosen` (required); `rejected`, a list of `option` and `reason`, both required |
| `finding` | `severity`: `blocker`, `major`, `minor` or `nit`; `status`: `open`, `fixed`, `wontfix` or `duplicate`; `resolution`, a sha or a reason, required unless the status is `open`; `source`, optional: `local` or `remote`, the review it came from |
| `verdict` | `outcome`: `proceed`, `proceed with changes`, `rethink` or `stop`; `gate`: `judge`, `review` or `human`; `commit`, the sha judged; all required; `judged`, optional: `task`, `plan` or `change`, what it judged |
| `lesson` | at least one anchor; `retired: true` only with `supersedes` |

The `verdict` outcomes are the four a judge's verdict file holds (`cli/internal/verdict`), spelled the same.

### Who writes what

Each kind is written by the f10 step that holds the knowledge when it is fresh, with a task id.
A failing `lore add` is one line in the step's report, never a failed step.

| kind | written by | |
|---|---|---|
| `intent` | the plan step, once the plan file is saved; a planless ship from its inline plan | a replan supersedes it; a judge's revision of the plan in place does not |
| `decision` | the ship skill at its flow's end, from the forks implement's hand-off noted; the catchup step, once a conflict settled by taking one side or by a named reconciliation is concluded | anchored to the committed code, so the lines no longer move |
| `finding` | the review step, one per finding (`source`); the resolve step, a superseding one per verdict | beside `reviews/<task>/<round>.md` |
| `verdict` | the judge step (`judged`) | beside `verdicts/<key>/<n>.md` |
| `lesson` | the ship skill, after its flow's last entry, from the findings, their outcomes and the run; most changes leave none | a later change that proves one wrong supersedes or retires it inside the change that wrote it |

Decisions and lessons come from a ship run: a change that reaches `f10 finish` another way (a PR picked up, a branch shipped by hand, a ship run that died before its end) has none, and finish's report says so in one line counting the change's intent, decisions and lessons.

### Who reads what

- The **implement** step reads `lessons` once per file before its first edit and follows them, or records an override as a `decision`.
- The **review** step reads `lessons` over every file in the diff and `why` at the base for a line it doubts; a hunk that breaks a lesson is a finding naming the lesson's id.
- `f10 finish` runs `check` right before a merge, under `project.md`'s `lore_gate`: `warn` notes the reasons, `block` refuses.
- `f10 status` reads lore first and the review and verdict files for runs from before lore; lore is the one meant to last.

## Verbs

```bash
f10 lore add <kind> --change <id> [--anchor path:line[-end][@commit][#symbol]]... [--supersedes <id>] [kind flags] [-m <text> | -F <file> | stdin]
f10 lore change [<task-id>]
f10 lore show <change> [--json]
f10 lore why <path:line[@rev]> [--json]
f10 lore lessons <path> [--json]
f10 lore check <change>
f10 lore validate [<change>]
```

Every verb takes `--root <dir>`.

- **`add`** validates and writes one entry and prints its id.
  Kind flags: `--chosen`, `--rejected 'option: reason'` (repeatable) for a decision; `--severity`, `--status`, `--resolution`, `--source` for a finding; `--outcome`, `--gate`, `--commit`, `--judged` for a verdict; `--retired` for a lesson.
  A repeatable flag takes one value per occurrence, commas included: `--rejected 'fixed window: edge bursts, twice the limit'` is one option, and several anchors are several `--anchor` flags.
  An anchor without `@commit` takes `HEAD`'s, and fails before the first commit.
  `human` is `git config user.email`.
  Agent fields come from `--agent-model`, `--agent-via` and `--agent-session`, else `F10_AGENT_MODEL`, `F10_AGENT_VIA` and `F10_AGENT_SESSION`, else the Claude Code session: its id, and the model its SessionStart hook recorded (the model it started on; a later `/model` switch is not seen); `via` is `claude-code` inside Claude Code; `agent` is written only when a model or a session is known.
- **`change`** prints the change the task's current PR writes to: the task id, or `<id>.<n>` once `n - 1` of its PRs have finished.
  A series whose PR merged without `f10 finish` is archived first, as `f10 review round` does.
- **`show`** prints the change's effective entries, the intent first, then by time.
  A bare task id prints each of its PRs' changes, newest first, and `--json` flattens them into one array in that order; `show <id>.<n>` prints that one change.
- **`why`** blames the line, reads the commit's `Change:` trailers (else its `Task:` trailers), loads each change, and prints the intent, the latest verdict, and the effective decisions, findings and lessons with an anchor on the same path that covers the line, or that share a symbol with such an anchor.
  A commit with no trailer, or whose changes hold no entries, falls back to every change's entries with an anchor on the same path whose `commit` is the blamed one, or with an anchor on the line whose `commit` is an ancestor of it (an anchor written before its code was committed); the output says the link came from an anchor, a weaker one than a trailer.
  `why` and `lessons` skip a file that does not parse; `validate` reports it.
  Nothing found: `no record`, exit 0.
  A line not yet committed is `no record` too; a path git does not know is an error.
  `path:line@<rev>` reads the line as that revision has it, the base sha for a reviewer asking about a line a diff rewrote, so the answer is the earlier change; a path absent at the revision is `no record`.
- **`lessons`** prints the effective, non-retired lessons anchored to the path, across every change.
  An agent reads it before editing a file.
- **`check`** exits 1, naming each reason, when the intent is missing, no verdict exists, the latest verdict is `rethink` or `stop`, or a `blocker` or `major` finding is still effectively `open`.
  It exits 0 otherwise.
  `f10 finish` runs it right before a merge, under `project.md`'s `lore_gate` (`docs/project-setup.md`): `warn`, the default, notes the reasons and merges; `block` refuses, which suits a flow with a `judge` entry, the one writer of verdicts.
- **`validate`** lints every entry of the change, or of every change: parse errors, missing or misplaced fields, bad anchors, a filename or directory that disagrees with the frontmatter, dangling `supersedes`, cross-kind `supersedes` and forks.
  It exits 1 when it finds any.

## Where the root lives

`project.md`'s frontmatter key `lore` decides (`docs/project-setup.md`):

- **`local`** (default): `lore/` under the main checkout's storage root (`.f10/lore`), or under `~/.f10/<project>/` with out-of-tree storage.
  Every worktree of the project writes there, so a task's entries outlive its worktree.
  Under stealth the storage root is never committed, so lore stays on this machine: `why` answers only here.
- **`committed`**: `.lore/` at the checkout root, committed with the code, so `why` works on any clone.
  This is an explicit opt-in, so it holds under `visibility: stealth` too.

`--root <dir>` overrides both and is how lore runs outside an f10 project.

## Example

One change, `FT-1`, rate limiting a public API, with all five kinds: an intent, a decision, a finding settled by a superseding entry, a verdict, and a lesson corrected by a superseding one.
`f10 lore validate FT-1` passes on it; a test lifts these files out of this document and checks that.

`FT-1/01M5C8KYM0248H248H248H248H-intent.md`

```markdown
---
kind: intent
change: FT-1
human: dev@example.com
at: 2026-10-20T09:00:00Z
---

Rate limit the public API per key, so one client's burst cannot starve the others.
Requests over the limit get 429 with a Retry-After header.
```

`FT-1/01M5CCX950248H248H248H248H-decision.md`

```markdown
---
kind: decision
change: FT-1
human: dev@example.com
agent:
    model: claude-opus-5-5
    via: claude-code
at: 2026-10-20T10:15:00Z
anchors:
    - path: api/limit.go
      line: 12
      end_line: 30
      commit: a1b2c3d
      symbol: Allow
chosen: token bucket per key
rejected:
    - option: fixed window counter
      reason: lets a client send twice the limit across a window edge
    - option: a shared limiter service
      reason: a network hop on every request for a single-instance API
---

A bucket per key refills at the plan's rate and holds one second of burst.
Buckets live in memory: the API runs as one instance.
```

`FT-1/01M5FC5ZR0248H248H248H248H-finding.md`

```markdown
---
kind: finding
change: FT-1
human: reviewer@example.com
at: 2026-10-21T14:00:00Z
anchors:
    - path: api/limit.go
      line: 22
      commit: a1b2c3d
      symbol: Allow
severity: major
status: open
---

The bucket map is never pruned: every key ever seen stays in memory.
```

`FT-1/01M5FMRMT0248H248H248H248H-finding.md`

```markdown
---
kind: finding
change: FT-1
human: dev@example.com
at: 2026-10-21T16:30:00Z
anchors:
    - path: api/limit.go
      line: 22
      commit: a1b2c3d
      symbol: Allow
supersedes: 01M5FC5ZR0248H248H248H248H
severity: major
status: fixed
resolution: 9f8e7d6
---

Buckets idle for ten minutes are dropped by a sweep on each refill tick.
```

`FT-1/01M5HM93W0248H248H248H248H-verdict.md`

```markdown
---
kind: verdict
change: FT-1
human: dev@example.com
agent:
    model: claude-opus-5-5
    via: claude-code
at: 2026-10-22T11:00:00Z
outcome: proceed with changes
gate: judge
commit: 9f8e7d6
---

Sound for one instance.
Name the single-instance assumption where the limiter is built, so a second replica does not silently double every limit.
```

`FT-1/01M5HMJ8V0248H248H248H248H-lesson.md`

```markdown
---
kind: lesson
change: FT-1
human: dev@example.com
at: 2026-10-22T11:05:00Z
anchors:
    - path: api/limit.go
      line: 12
      end_line: 30
      commit: 9f8e7d6
      symbol: Allow
---

Limits are per process.
Running a second replica doubles every key's effective limit.
```

`FT-1/01M5M1H1E0248H248H248H248H-lesson.md`

```markdown
---
kind: lesson
change: FT-1
human: dev@example.com
at: 2026-10-23T09:30:00Z
anchors:
    - path: api/limit.go
      line: 12
      end_line: 30
      commit: 9f8e7d6
      symbol: Allow
supersedes: 01M5HMJ8V0248H248H248H248H
---

Limits are per process.
Running N replicas multiplies every key's effective limit by N; move the buckets to a shared store before scaling out.
```

`f10 lore show FT-1` prints five entries: the intent, the decision, the fixed finding, the verdict and the corrected lesson.
`f10 lore check FT-1` passes: the intent exists, the verdict is `proceed with changes`, and the major finding is fixed.
