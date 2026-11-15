# The ledger

The ledger is `~/.f10/f10.db`, a SQLite database outside every repo.
Its `events` table records what runs did, one row per event, and rows are never changed or deleted.
`f10 ledger dump` prints them, and `f10 ledger schema` prints the schema of their payloads, which is checked in as [ledger.schema.json](ledger.schema.json).

## A row

The columns hold what every event has: `id`, `machine`, `at`, `project`, `project_local`, `checkout`, `branch`, `session`, `task`, `kind` and `source`.
`payload` holds the rest, a JSON object whose shape depends on `kind`.
Each kind's payload is a message in [`proto/f10/v1/events.proto`](../proto/f10/v1/events.proto) (the asks' in `proto/agents/v1/asks.proto`), the same message the API streams in `f10.v1.Event`.
It is stored as protojson under the proto field names, with every scalar, list and map key written even when empty, so `json_extract` reads a count as a number and an empty value as `""`.
In the schema, which is generated from those messages, `$defs/<kind>` lists the kind's payload versions, oldest first, and `$defs/<kind>.v<n>` is version `n`.

## Regressions

Two kinds record where a bug came from, both on the bug task's row and both written by `f10 task regression` ([cli.md](cli.md#regression)).
`regression_found` is what the debug step established: the origin task, the commit, the faulty lines, how review let the fault through, and the reproduction outcome.
`regression_claimed` is the origin the task's own text named when it was captured.
The two can disagree, and both stand.
`f10 history` ([cli.md](cli.md#history)) reads `regression_found` to count the bugs traced to the files it is asked about, or to the tasks that touched them.
`f10 audit map` ([cli.md](cli.md#audit)) reads it for each area's regressions and lineages.

## Readers

- `f10 history` and `f10 audit map` read, per task, `task_sized`, `review_round_opened`, `finding_verdict`, `plan_superseded`, `task_halted`, `judge_verdict`, `regression_found` and the step windows, through one shared count, so a round or a fix counts the same in both.
- `f10 standup` reads the project's sessions active in the period: the `state_*` claims (asks, stops, the step a run is on), `task_halted`, `plan_saved`, `plan_superseded`, `spike_saved`, `review_round_opened`, `finding_verdict`, `judge_verdict`, `regression_found` and the step windows ([cli.md](cli.md#standup)).
- The API's `WatchEvents` streams every row in insertion order. Ask rows (`ask_raised`, `ask_answered`) have no project column, so the stream matches them to a project by their `main` label; `watch_reported` rows carry the project the watcher ran in, and no task for a report about the whole project.
- `spike_saved` is a spike report written under the storage root's `spikes/`, recorded as `plan_saved` is for a plan.

## Versions

Every payload carries `v`, its version, which f10 sets when it writes the row.
A payload with no `v` is version 1, because rows written before versions existed have none.

Within a version, a payload message only gains fields, because a reader of an older row simply finds the new keys unset.
`buf breaking` refuses a renumbered, renamed or removed field (`proto/README.md`), so a key never changes meaning in place.
A real break, such as renaming or retyping a key, is a new message for the kind and a new version, with a function that turns the previous version's message into it.
f10 reads an old row through those functions, so a reader always sees the current shape, and the stored row stays as it was written.

`run_started` and `session_stopped` are at version 2 although one message reads both versions: their second version only added keys, before payloads were messages, and f10 keeps writing `v: 2` because older f10s sharing the database read that number.

Rows written before payloads were messages left empty keys out and stored an unset enum, such as an unanswered ask's `answered_by`, as `""`.
Both still read: a missing key is an empty field, and an empty enum reads as `*_UNSPECIFIED`.

A row whose `v` is above the newest version this f10 knows comes from a newer f10 sharing the database.
It reads as far as the current shape allows, and the reader is told the row is partial, so the row is never dropped.

## Indexed payload keys

Some payload keys are also generated columns, so a query can filter on them with an index:

| column      | payload key | index                            |
|-------------|-------------|----------------------------------|
| `p_path`    | `path`      | `events_kind_p_path_id (kind, p_path, id)` |
| `p_round`   | `round`     | `events_kind_p_round (kind, p_round, p_finding)` |
| `p_finding` | `finding`   | `events_kind_p_round`            |

The columns are virtual: SQLite computes them from `payload`, so old rows have them too.
A version that renames one of these keys changes its column, in the same release, to a `COALESCE` over the old and the new name, so rows of both versions keep their value.

## What ran a run, and what it cost

`run_started` carries the run context: `f10_version`, `model`, `agent` (the agent kind, `claude` unless a profile names another) and `profile` (the `kind`, `model` and `effort` that `f10 start` or `f10 drive` recorded on the branch).
A key the hook could not learn is empty, and an interactive session has no `profile`.
`session_stopped` fills the same keys only when the context changed since the session's last row that carried one, such as after a `/model` switch.
On every other stop row they are present and empty, so a reader finds the context in force by taking the latest row with a non-empty `model`, never by testing whether the key exists.
A stop row cannot tell "unchanged" apart from "changed to unknown".

`turn_used` is the tokens one turn spent, read from the session's transcript as a delta since the previous turn, so a task's cost is the sum of its rows:

- `usage.input`, `usage.output`, `usage.cache_write` and `usage.cache_read` are the session's own messages.
  One API message spans several transcript lines that each repeat its usage, so each message counts once.
- `usage.by_model` splits the same four by model when the turn ran on more than one, and is empty (`{}`) otherwise.
- `usage.subagents` holds the same four for the subagents the turn ran, from inline sidechain lines and from the session's `subagents/` transcripts.
- `usage.through` is the uuid of the last transcript entry counted.

Counting starts when a run starts, which is when a capture, plan, ship or debug skill starts in the session, so tokens spent before it are not attributed to the run.
Another skill in a run that is still counting keeps counting from where it was.
The turn in which the run reaches done is the last one counted, so later turns count for no task until the next run starts.
A run that failed or stopped partway keeps counting, because a drive follows it up in the same session with skills such as catchup that start no run.
A session with no run writes no `turn_used`, so skills that start no run on their own, such as `/f10:review` run by hand, record no usage.
A turn whose transcript is missing or unreadable writes no `turn_used` row, and its `session_stopped` row is written as usual.
No prompt or response text reaches the ledger.
