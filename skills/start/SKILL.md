---
name: start
description: "f10 start skill - start the next task without leaving this session: a worktree, a Herdr workspace and a claude agent in it, already prompted; a plan the main checkout saved for the task moves into the worktree and the agent ships it; a PR link picks up someone else's PR. Use when the user says \"/f10:start <task-id | PR url | #n> [--plan | --local | --discuss] [--after <task-id> | --base <branch>] [--task <id>] [--model <model>]\" or asks to start a task in a new worktree or workspace, including one to discuss before planning, one on another model, or a PR handed over to continue or review."
allowed-tools: Bash
argument-hint: "<task-id | PR url | #n> [--plan | --local | --discuss] [--task <id>]"
---

# f10 · start

Start a task beside this one: `f10 start` creates the branch and worktree, opens them as a Herdr workspace, starts a claude agent in its root pane and prompts it, marked `--driven` (`${CLAUDE_PLUGIN_ROOT}/modes/driven.md`) unless `--discuss`.
A plan the main checkout saved for the task (`/f10:plan` after a discussion there) moves into the new worktree, and the agent ships it instead of planning again.
`--model` picks the agent's model (`opus`, `fable`, `opus-5.5`); the command corrects near misses.
A PR link or `#n` picks up that PR: its branch checked out tracking origin's and linked to its task, and an agent that summarises it and waits.
This skill only runs the command and relays its output; it runs no step and never waits on the new agent.

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- Everything else goes to `f10 start` verbatim.

```
f10 start <arguments>
```

Then print the command's report block in a fenced `yaml` block (`conventions/report.md`) and any line beneath it, and stop.
A line starting `!!!` goes first, verbatim, above the block.

**Exit 3 is a question**: a PR whose body, title and branch name no task.
Ask the user for the task id once with `AskUserQuestion` (the PR's url in the question; the id comes through Other), rerun the same command with `--task <id>` appended, and relay its report.

If the command fails otherwise, print its message verbatim and stop; it names the cause and what to do.
If `f10` is not on `PATH`, or fails with `flag provided but not defined`, the binary is missing or older than this plugin: say so, quote `f10 --version` where it ran, and point at `go install github.com/amberpixels/f10/cli/cmd/f10@latest`.
