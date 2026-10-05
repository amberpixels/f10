---
name: start
description: "f10 start skill - start the next task without leaving this session: a worktree, a Herdr workspace and a claude agent in it, already prompted. Use when the user says \"/f10:start <task-id> [--plan | --local] [--after <task-id> | --base <branch>]\" or asks to start a task in a new worktree or workspace."
allowed-tools: Bash
argument-hint: "<task-id>[-suffix] [--plan | --local] [--after <task-id> | --base <branch>] [-C <project>]"
---

# f10 · start

Start a task beside this one: `f10 start` creates the branch and worktree, opens them as a
Herdr workspace, starts a claude agent in its root pane and prompts it. This skill only runs the
command and relays what it printed - it runs no step and never waits on the new agent.

Run, with the user's arguments verbatim:

```
f10 start <arguments>
```

Then print the command's report block in a fenced `yaml` block (`conventions/report.md`) and any
line it printed beneath it, and stop. Add nothing: the work now happens in the other workspace.

If the command fails, print its message verbatim and stop - it names the cause (outside Herdr,
no branch for `--after` or `--base`, several branches for the task) and what to do. If `f10` is not on `PATH`,
say so and point at `go install github.com/amberpixels/f10/cli/cmd/f10@latest`.
