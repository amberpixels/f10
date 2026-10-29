---
name: status
description: "f10 status skill - where an f10 run is (this session's, a task's in its own worktree, each child's of a parent, or everything this session started or drove), why it stopped, and the question a driven agent is waiting on, relayed and answered. Use when the user says \"/f10:status\" or \"/f10:status <task-id>\", asks where a run is, why it stopped, how a parent's subtasks are going, what the status-line badge means, or what an agent in another workspace is asking."
allowed-tools: Bash, AskUserQuestion
argument-hint: "[task-id]"
---

# f10 · status

Where a run is, in words: the task, the three phases, the step a stopped run stopped on, the reason, and what unblocks it.
The status-line badge draws the same facts as three glyphs.
This skill runs the command and relays its output (`${CLAUDE_PLUGIN_ROOT}/modes/binary.md`); it runs no step.

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- No argument: the answer is below, already run: the current task's (the session's, else the branch's), as `f10 status <task-id>` prints it, or the session's run when no task is named.
  In a checkout whose branch names no task, the main one, a session that started or drove tasks gets a `launched here` tree instead: a row per task still live, each start's and each running drive's, nested by `After:` as a parent's tree is.
  Print it as point 1 does, then follow points 2-4 with the task the output names; a `launched here` tree is followed as point 3 follows a parent's.
- A task id: another run, the one the task's agent reports from the checkout that has its branch, usually a worktree `f10 start` opened.
  The hook lets this form through, because the answer may need you.
  Follow the numbered points below.

!`command -v f10 >/dev/null 2>&1 && f10 status --plain || "${CLAUDE_PLUGIN_ROOT}/bin/f10-state.sh" show`

1. Run `f10 status <task-id> --plain`.
   Print the output verbatim in a plain fenced block, whatever it is: a parent's tree, a finished task's tree, or a run's block.
   Never redraw, reorder or turn it into a table.
   A failure is printed as is and ends the turn.
2. **When the output carries an `ask` row**, the agent is driven (`${CLAUDE_PLUGIN_ROOT}/modes/driven.md`) and stopped on that question.
   Put it to the user in **one `AskUserQuestion`**: one question per numbered item, the bracketed options as the choices, the first as recommended.
   Then send the answers back as the agent's next prompt, numbered as the ask was:

   ```
   f10 forward <task-id> "1. <answer> 2. <answer>"
   ```

   Print forward's block (task, workspace, sent) in a fenced `yaml` block and stop.
   The agent resumes in its own tab; this session's badge stays as it is.
3. **A parent, or the session's launches, prints a tree**, a row per child, instead of a block.
   An `asking` child carries its question, whole, on an `ask` line under its row: put every one to the user in **one `AskUserQuestion`**, the questions grouped by child id, and forward each child its own answers (`f10 forward <child-id> "1. <answer>"`), one forward per child in one message.
   No `asking` child: the tree is the whole answer.
   Stop.
4. No `ask` row: the status is the whole answer.
   Stop.
   A task with no live run of its own prints `run none for <id>` instead: what the agent in its checkout is on now (`agent`), and its `plan` and `pr` where they exist.
   That block never carries an `ask` row.
   A finished task, its branch gone, prints a one-row tree, `done` with the merged PR, its gates as lines under it.
