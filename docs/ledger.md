# The ledger

The ledger is `~/.f10/f10.db`, a SQLite database outside every repo.
Its `events` table records what runs did, one row per event, and rows are never changed or deleted.
`f10 ledger dump` prints them, and `f10 ledger schema` prints the schema of their payloads, which is checked in as [ledger.schema.json](ledger.schema.json).

## A row

The columns hold what every event has: `id`, `machine`, `at`, `project`, `project_local`, `checkout`, `branch`, `session`, `task`, `kind` and `source`.
`payload` holds the rest, a JSON object whose shape depends on `kind`.
In the schema, `$defs/<kind>` lists the kind's payload versions, oldest first.

## Versions

Every payload carries `v`, its version, which f10 sets when it writes the row.
A payload with no `v` is version 1, because rows written before versions existed have none.

A payload changes shape only through a new version: renaming or retyping a key adds a version and a function that turns the previous version into it.
f10 reads an old row through those functions, so a reader always sees the current shape, and the stored row stays as it was written.

A row whose `v` is above the newest version this f10 knows comes from a newer f10 sharing the database.
It reads as far as the current shape allows, and the reader is told the row is partial, so the row is never dropped.

## Indexed payload keys

Some payload keys are also generated columns, so a query can filter on them with an index:

| column      | payload key | index                            |
|-------------|-------------|----------------------------------|
| `p_path`    | `path`      | `events_kind_p_path (kind, p_path)` |
| `p_round`   | `round`     | `events_kind_p_round (kind, p_round, p_finding)` |
| `p_finding` | `finding`   | `events_kind_p_round`            |

The columns are virtual: SQLite computes them from `payload`, so old rows have them too.
A version that renames one of these keys changes its column, in the same release, to a `COALESCE` over the old and the new name, so rows of both versions keep their value.

## What ran a run, and what it cost

`run_started` carries the run context: `f10_version`, `model`, `agent` (the agent kind, `claude` unless a profile names another) and `profile` (the `kind`, `model` and `effort` that `f10 start` or `f10 drive` recorded on the branch).
A key the hook could not learn is absent, and an interactive session has no `profile`.
`session_stopped` carries the same keys only when the context changed since the session's last row that carried one, such as after a `/model` switch.

`turn_used` is the tokens one turn spent, read from the session's transcript as a delta since the previous turn, so a task's cost is the sum of its rows:

- `usage.input`, `usage.output`, `usage.cache_write` and `usage.cache_read` are the session's own messages.
  One API message spans several transcript lines that each repeat its usage, so each message counts once.
- `usage.by_model` splits the same four by model, only when the turn ran on more than one.
- `usage.subagents` holds the same four for the subagents the turn ran, from inline sidechain lines and from the session's `subagents/` transcripts.
- `usage.through` is the uuid of the last transcript entry counted.

Counting starts when an f10 skill first starts in the session, so tokens spent before it are not attributed to the run, and a session with no f10 run writes no `turn_used`.
A turn whose transcript is missing or unreadable writes no `turn_used` row, and its `session_stopped` row is written as usual.
No prompt or response text reaches the ledger.
