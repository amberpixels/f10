---
name: finish
description: "f10 finish skill - close a task without leaving this session: merge its PR or MR, archive the plan, close the Herdr workspace, remove the worktree and its branch, pull main. Use when the user says \"/f10:finish <task-id> [--yes] [--merge | --local]\" or asks to finish, close out or clean up a merged task, or to clean up after reviewing someone else's PR."
allowed-tools: Bash, AskUserQuestion
argument-hint: "<task-id> [--merge | --local]"
---

# f10 · finish

Finish a task: `f10 finish` merges its PR or MR, archives its plan, closes its workspace and removes its worktree and branch.
This skill only runs the command and relays its output; it runs no step.

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- Everything else goes to `f10 finish` verbatim.

```
f10 finish <arguments>
```

Then print the command's report block in a fenced `yaml` block (`conventions/report.md`) and any line beneath it, and stop.

**Two refusals are questions.**
When the command refuses because it would close its own workspace (the message names the workspace), ask the user once with `AskUserQuestion`: close this workspace and finish, or stop.
On yes, rerun the same command with `--yes` appended and relay its report; the workspace then closes, ending this session.
On no, stop.

When the command exits 3 because someone else opened the PR (the message names the author, `--merge` and `--local`), ask once with `AskUserQuestion`: clean up locally and leave the PR open (`--local`), or merge it (`--merge`), or stop.
Rerun the same command with the chosen flag appended and relay its report.

When the command exits 7 because the task ships in a bundle (the message names the riders and `--close`), ask once with one multi-select `AskUserQuestion`: which riders does the PR deliver, one option per rider, every one recommended.
Rerun the same command with `--close <the chosen ids, comma-separated>`, or `--close none` when none is chosen, and relay its report: the lead and the chosen riders close on merge, the rest stay open.

Every other refusal is final: print the message verbatim and stop; it names the cause and what to do.
A `flag provided but not defined` or an unknown command means a stale `f10` earlier on `PATH` shadows the plugin's: say so, quote `f10 --version`, and point at `f10 doctor`. A missing binary fails inside the plugin's `f10` with its own one-line message: relay it.
