---
name: finish
description: "f10 finish skill - close a task without leaving this session: merge its PR or MR, archive the plan, close the Herdr workspace, remove the worktree and its branch, pull main. Use when the user says \"/f10:finish <task-id> [--yes]\" or asks to finish, close out or clean up a merged task."
allowed-tools: Bash, AskUserQuestion
argument-hint: "<task-id>"
---

# f10 · finish

Finish a task: `f10 finish` merges its PR or MR, archives its plan, closes its workspace and
removes its worktree and branch. This skill only runs the command and relays its output; it
runs no step.

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md`.
- Everything else goes to `f10 finish` verbatim.

```
f10 finish <arguments>
```

Then print the command's report block in a fenced `yaml` block (`conventions/report.md`) and any
line beneath it, and stop.

**One refusal is a question.** When the command refuses because it would close its own
workspace (the message names the workspace), ask the user once with `AskUserQuestion`: close
this workspace and finish, or stop. On yes, rerun the same command with `--yes` appended and
relay its report; the workspace then closes, ending this session. On no, stop.

Every other refusal is final: print the message verbatim and stop; it names the cause and what
to do. If `f10` is not on `PATH`, or fails with `flag provided but not defined`, the binary is missing or older than this plugin: say so, quote `f10 --version` where it ran, and point at `go install github.com/amberpixels/f10/cli/cmd/f10@latest`.
