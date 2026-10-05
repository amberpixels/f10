---
name: finish
description: "f10 finish skill - close a task without leaving this session: merge its PR or MR, archive the plan, close the Herdr workspace, remove the worktree and its branch, pull main. Use when the user says \"/f10:finish <task-id> [--yes]\" or asks to finish, close out or clean up a merged task."
allowed-tools: Bash, AskUserQuestion
argument-hint: "<task-id>[-suffix] [--yes] [-C <project>]"
---

# f10 · finish

Finish a task: `f10 finish` merges the PR or MR through the host CLI (or confirms it is already
merged), deletes the remote branch, pulls main and confirms the merge is in it, moves the task's
plan into main's `plans/archive/`, closes the Herdr workspace and removes the worktree and its
local branch. This skill only runs the command and relays what it printed - it runs no step.

Run, with the user's arguments verbatim:

```
f10 finish <arguments>
```

Then print the command's report block in a fenced `yaml` block (`conventions/report.md`) and any
line it printed beneath it, and stop.

**One refusal is a question.** When the command refuses because it would close its own
workspace (the message says so and names the workspace), ask the user once with
`AskUserQuestion`: close this workspace and finish, or stop. On yes, run the same command again
with `--yes` appended and relay its report; the workspace closes right after it, which ends this
session. On no, stop.

Every other refusal is final: print the message verbatim and stop. It names the cause (outside
Herdr, a dirty worktree file by file, no branch or worktree for the task, the host's reason for
not merging, a PR stacked on another task's branch, a closed PR) and what to do. If `f10` is
not on `PATH`, say so and point at `go install github.com/amberpixels/f10/cli/cmd/f10@latest`.
