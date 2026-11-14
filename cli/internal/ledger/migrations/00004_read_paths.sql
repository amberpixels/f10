-- +goose Up
-- The bounded reads: a set of a project's sessions in id order, a project's
-- rows of one kind in id order, a task's sessions read from the index alone,
-- a task's review rows by round and finding, and the newest verdict at a path
-- without a sort.
CREATE INDEX IF NOT EXISTS events_session_project_id ON events (session, project, id);
CREATE INDEX IF NOT EXISTS events_project_kind_id ON events (project, kind, id);
CREATE INDEX IF NOT EXISTS events_project_task ON events (project, task, session);
CREATE INDEX IF NOT EXISTS events_task_kind_round ON events (task, kind, p_round, p_finding, id);
CREATE INDEX IF NOT EXISTS events_kind_p_path_id ON events (kind, p_path, id);
DROP INDEX IF EXISTS events_kind_p_path;

-- +goose Down
CREATE INDEX IF NOT EXISTS events_kind_p_path ON events (kind, p_path);
DROP INDEX IF EXISTS events_kind_p_path_id;
DROP INDEX IF EXISTS events_task_kind_round;
DROP INDEX IF EXISTS events_project_task;
DROP INDEX IF EXISTS events_project_kind_id;
DROP INDEX IF EXISTS events_session_project_id;
