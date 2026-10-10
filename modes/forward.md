# Mode · forward - a task another worktree holds

A task started with `f10 start` has its own worktree, workspace and agent, and its plan sits in
that worktree's storage root. Running a task-id command for it here would use the wrong branch
and no plan, so `/f10:plan` and `/f10:ship` hand it over first.

**Activation:** the argument is a task id. Before loading anything, one call, with the skill
that was invoked:

```
f10 forward <task-id> "/f10:<skill> <the argument verbatim>"
```

- **exit 0** - another checkout holds the task's branch and the command went to its agent.
  Print the command's block (task, workspace, sent) in a fenced `yaml` block, add nothing, and
  stop. Join `f10-state.sh clear` to the same call to retire the badge seeded when the command
  was typed: this session has no run.
- **exit 3** - the task is here (its branch is this checkout's, or it has no worktree), as
  stderr says. Continue with the skill.
- anything else is a failure per `conventions/failure.md` (outside Herdr, no workspace shows
  the worktree, the agent is idle and not reporting). Report it and stop.

Without the `f10` binary on `PATH`, read `git worktree list --porcelain` once. A worktree other
than this checkout whose branch is `<id>`, `<id>/...` or `<id>-...` holds the task out of reach:
fail, pointing at `go install github.com/amberpixels/f10/cli/cmd/f10@latest`. Otherwise continue
with the skill.
