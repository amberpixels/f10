// Package tasksource reads and writes a project's tasks through one
// interface: the tracker driver, the host's issues, or a refusal naming what
// is missing, picked by TasksFor. It also holds what the sources return: task
// info, raw children and the subtask tree.
package tasksource
