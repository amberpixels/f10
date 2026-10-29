---
name: finish
description: "f10 finish skill - close a task without leaving this session: merge its PR or MR (a branch never pushed merges into the local default branch), archive the plan, close the Herdr workspace, remove the worktree and its branch, pull main; a spike with no PR closes without a merge, its report archived and its experiment discarded or, with --keep, kept on a branch. Use when the user says \"/f10:finish [<task-id>] [--yes] [--merge | --local | --keep]\" or asks to finish, close out or clean up a merged task, or to clean up after reviewing someone else's PR."
allowed-tools: Bash, AskUserQuestion
argument-hint: "[<task-id>] [--merge | --local | --keep]"
---

# f10 · finish

Finish a task: `f10 finish` merges its PR or MR, or a branch never pushed into the local default branch, archives its plan, closes its workspace and removes its worktree and branch; a spike with no PR is closed without a merge, its report archived beside the plan.
This skill only runs the command and relays its output (`${CLAUDE_PLUGIN_ROOT}/modes/binary.md`); it runs no step.

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- `<task-id>`: the task to finish.
  Without it, the command finishes the branch's task and its report names it; off a task branch it refuses.
- `--merge` / `--local`: for someone else's PR, merge it, or clean up locally and leave it open (point 3 asks when neither is given).
- `--keep`: keep a spike's experiment, committed on a `kept/` branch, instead of discarding it.
- Everything else goes to `f10 finish` verbatim.

```
f10 finish <arguments>
```

1. Run it, then print the command's report block in a fenced `yaml` block (`conventions/report.md`) and any line beneath it, and stop.
2. **The own-workspace refusal is a question** (the message names the workspace; a bare call inside the task's workspace hits it too): ask the user once with `AskUserQuestion`, close this workspace and finish, or stop.
   On yes, rerun the same command with `--yes` appended and relay its report; the workspace then closes, ending this session.
   On no, stop.
3. **Exit 3 is a question**: someone else opened the PR (the message names the author, `--merge` and `--local`).
   Ask once with `AskUserQuestion`: clean up locally and leave the PR open (`--local`), or merge it (`--merge`), or stop.
   Rerun the same command with the chosen flag appended and relay its report.
4. **Exit 7 is a question**: the task ships in a bundle (the message names the riders and `--close`).
   Ask once with one multi-select `AskUserQuestion`: which riders does the PR deliver, one option per rider, every one recommended.
   Rerun the same command with `--close <the chosen ids, comma-separated>`, or `--close none` when none is chosen, and relay its report: the lead and the chosen riders close on merge, the rest stay open.
5. Every other refusal is final: print the message verbatim and stop.
