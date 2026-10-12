---
name: start
description: "f10 start skill - start the next task without leaving this session: a worktree, a Herdr workspace and a claude agent in it, already prompted. Use when the user says \"/f10:start <task-id> [--plan | --local | --discuss] [--after <task-id> | --base <branch>]\" or asks to start a task in a new worktree or workspace, including one to discuss before planning."
allowed-tools: Bash
argument-hint: "<task-id>[-suffix] [--plan | --local | --discuss] [--after <task-id> | --base <branch>] [-C <project>]"
---

# f10 · start

Start a task beside this one: `f10 start` creates the branch and worktree, opens them as a
Herdr workspace, starts a claude agent in its root pane and prompts it, marked `--driven`
(`${CLAUDE_PLUGIN_ROOT}/modes/driven.md`) unless `--discuss`. This skill only runs the command
and relays its output; it runs no step and never waits on the new agent.

Run, with the user's arguments verbatim:

```
f10 start <arguments>
```

Then print the command's report block in a fenced `yaml` block (`conventions/report.md`) and any
line beneath it, and stop.

If the command fails, print its message verbatim and stop; it names the cause and what to do. If `f10` is not on `PATH`, or fails with `flag provided but not defined`, the binary is missing or older than this plugin: say so, quote `f10 --version` where it ran, and point at `go install github.com/amberpixels/f10/cli/cmd/f10@latest`.
