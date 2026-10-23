---
name: status
description: "f10 status skill - where an f10 run is (this session's, a task's in its own worktree, or each child's of a parent), why it stopped, and the question a driven agent is waiting on, relayed and answered. Use when the user says \"/f10:status\" or \"/f10:status <task-id>\", asks where a run is, why it stopped, how a parent's subtasks are going, what the status-line badge means, or what an agent in another workspace is asking."
allowed-tools: Bash, AskUserQuestion
argument-hint: "[task-id]"
---

# f10 · status

Where a run is, in words: the task, the three phases, the step a stopped run stopped on, the reason, and what unblocks it.
The status-line badge draws the same facts as three glyphs.

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- No argument: the answer is below, already run.
  Print it verbatim in a fenced block and stop.
- A task id: another run, the one the task's agent reports from the checkout that has its branch, usually a worktree `f10 start` opened.
  The hook lets this form through, because the answer may need you.
  Follow the numbered points below.

!`command -v f10 >/dev/null 2>&1 && f10 status || "${CLAUDE_PLUGIN_ROOT}/bin/f10-state.sh" show`

1. Run `f10 status <task-id>` and print its output verbatim in a fenced block.
   A failure is printed as is and ends the turn.
2. **When the output carries an `ask` row**, the agent is driven (`${CLAUDE_PLUGIN_ROOT}/modes/driven.md`) and stopped on that question.
   Put it to the user in **one `AskUserQuestion`**: one question per numbered item, the bracketed options as the choices, the first as recommended.
   Then send the answers back as the agent's next prompt, numbered as the ask was:

   ```
   f10 forward <task-id> "1. <answer> 2. <answer>"
   ```

   Print forward's block (task, workspace, sent) in a fenced `yaml` block and stop.
   The agent resumes in its own tab; this session's badge stays as it is.
3. **A parent prints one row per child** (id, stage, title, step, PR) instead of a block.
   Each `ask:` line sits under an `asking` child's row: put every one to the user in **one `AskUserQuestion`**, the questions grouped by child id, and forward each child its own answers (`f10 forward <child-id> "1. <answer>"`), one forward per child in one message.
   No `ask:` line: the rows are the whole answer.
   Stop.
4. No `ask` row: the status is the whole answer.
   Stop.
   A task with no live run of its own prints `run none for <id>` instead: what the agent in its checkout is on now (`agent`), and its `plan` and `pr` where they exist.
   That block never carries an `ask` row.
   A finished task, its branch gone, prints its single `done` row with the merged PR.

A `flag provided but not defined` or an unknown command means a stale `f10` earlier on `PATH` shadows the plugin's: say so, quote `f10 --version`, and point at `f10 doctor`. A missing binary fails inside the plugin's `f10` with its own one-line message: relay it.
