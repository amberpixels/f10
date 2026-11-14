# The ledger

The ledger is `~/.f10/f10.db`, a SQLite database outside every repo.
Its `events` table records what runs did, one row per event, and rows are never changed or deleted.
`f10 ledger dump` prints them, and `f10 ledger schema` prints the schema of their payloads, which is checked in as [ledger.schema.json](ledger.schema.json).

## A row

The columns hold what every event has: `id`, `machine`, `at`, `project`, `project_local`, `checkout`, `branch`, `session`, `task`, `kind` and `source`.
`payload` holds the rest, a JSON object whose shape depends on `kind`.
In the schema, `$defs/<kind>` lists the kind's payload versions, oldest first.

## Regressions

Two kinds record where a bug came from, both on the bug task's row and both written by `f10 task regression` ([cli.md](cli.md#regression)).
`regression_found` is what the debug step established: the origin task, the commit, the faulty lines, how review let the fault through, and the reproduction outcome.
`regression_claimed` is the origin the task's own text named when it was captured.
The two can disagree, and both stand.

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
