# Mode · forward - a task another worktree holds

A task started with `f10 start` has a worktree, a workspace and an agent of its own.
A task-id command run for it here would work on the wrong branch.
So `/f10:plan` and `/f10:ship` hand the command over to that task's agent before doing anything else.

## Activation

Forward mode applies when the argument is a task id, or a bundle of task ids joined by `+`.
`f10 forward` takes a bundle whole and finds the checkout of its lead task.

Before loading anything, make one call, naming the skill that was invoked:

```
f10 forward <task-id> "/f10:<skill> <the argument verbatim>"
```

## The exit code

- **exit 0** - another checkout holds the task's branch, and the command went to its agent.
  Print the command's output (task, workspace, sent) in a fenced `yaml` block, then stop.
  Join `f10-state.sh clear` to the same call (`f10 forward ... && f10-state.sh clear`).
  It retires the badge that was set when the command was typed, because this session has no run.
- **exit 3** - the task is here, as stderr says.
  Either its branch is this checkout's, or it has no worktree.
  Continue with the skill.
- **Any other exit** is a failure, handled as `conventions/failure.md` says.
  Examples are a session outside Herdr, no workspace showing the worktree, or an agent that is idle and not reporting.
  Report the failure and stop.
