-- +goose Up
-- IF NOT EXISTS throughout: two processes racing the first open both succeed.
CREATE TABLE IF NOT EXISTS events (
    id            TEXT PRIMARY KEY,
    machine       TEXT NOT NULL,
    at            TIMESTAMP NOT NULL,
    project       TEXT NOT NULL DEFAULT '',
    project_local BOOLEAN NOT NULL DEFAULT 0,
    checkout      TEXT NOT NULL DEFAULT '',
    branch        TEXT NOT NULL DEFAULT '',
    session       TEXT NOT NULL DEFAULT '',
    task          TEXT NOT NULL DEFAULT '',
    kind          TEXT NOT NULL,
    source        TEXT NOT NULL,
    payload       TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS events_project_at ON events (project, at);
CREATE INDEX IF NOT EXISTS events_task ON events (task);
CREATE INDEX IF NOT EXISTS events_session ON events (session, kind);

-- +goose Down
DROP TABLE IF EXISTS events;
