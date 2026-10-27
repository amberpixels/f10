-- +goose Up
-- A task's title and url as f10 last read them, so naming another task in
-- outgoing text costs no tracker call. id is "<project> <task>": r3 takes one
-- primary key column, and the ledger holds every project.
CREATE TABLE IF NOT EXISTS task_refs (
    id      TEXT PRIMARY KEY,
    project TEXT NOT NULL,
    task    TEXT NOT NULL,
    title   TEXT NOT NULL DEFAULT '',
    url     TEXT NOT NULL DEFAULT '',
    seen_at TIMESTAMP NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS task_refs;
