---
name: status
description: "f10 status skill - where an f10 run is (this session's, a task's in its own worktree, each child's of a parent, or everything this session started or drove), why it stopped, and the question a driven agent is waiting on, shown and answered when the user replies. Use when the user says \"/f10:status\" or \"/f10:status <task-id>\", asks where a run is, why it stopped, how a parent's subtasks are going, what the status-line badge means, or what an agent in another workspace is asking."
allowed-tools: Bash
argument-hint: "[task-id]"
---

# f10 · status

Where a run is, in words: the task, the three phases, the step a stopped run stopped on, the reason, and what unblocks it.
The status-line badge draws the same facts as three glyphs.
This skill runs the command and relays its output (`${CLAUDE_PLUGIN_ROOT}/modes/binary.md`); it runs no step.

**Relay the output verbatim, never redrawn.**
Never reorder it, summarise it, or turn it into a table, because the binary already aligned it and a redraw drops rows and colors.

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- No argument: the answer is below, already run: the current task's (the session's, else the branch's), as `f10 status <task-id>` prints it, or the session's run when no task is named.
  In a checkout whose branch names no task, the main one, a session that started or drove tasks gets a `launched here` tree instead: a row per task still live, each start's and each running drive's, nested by `After:` as a parent's tree is.
  Print it as point 1 does, then follow points 2-4 with the task the output names; a `launched here` tree is followed as point 3 follows a parent's.
  The hook answers a bare call itself, with no model turn.
  The exception is output that holds a question, which is an `ask` row in a block or an `asking` row in a tree.
- A task id: another run, the one the task's agent reports from the checkout that has its branch, usually a worktree `f10 start` opened.
  The hook answers this form itself too, with no model turn, on the same rule.
  You see it only in three cases.
  The output holds a question, the argument names no task, or reading the task takes longer than 3 seconds.
  Follow the numbered points below.

!`command -v f10 >/dev/null 2>&1 && f10 status --plain || "${CLAUDE_PLUGIN_ROOT}/bin/f10-state.sh" show`

1. Run `f10 status <task-id> --plain`.
   Print the output verbatim in a fenced block, as the rule above says.
   Print nothing above the block, not even a summary line, because the block is the answer and a sentence over it only restates it.
   One task always prints a block, whether its run is live, `run none` or finished.
   A block opens with `task:`, so it goes in a ```` ```yaml ```` fence, which colors it as the ship report is colored.
   Several tasks (a parent's children, `launched here`) always print a tree.
   A tree goes in a plain fence, because its titles break the highlighting.
   A failure is printed as is and ends the turn.
2. **When the output carries an `ask` row**, the agent is driven (`${CLAUDE_PLUGIN_ROOT}/modes/driven.md`) and stopped on that question.
   The block already shows the question.
   Add one line under it saying the user can answer by replying here, or in the agent's own pane.
   Never open an `AskUserQuestion` for it, because the user asked where the run is rather than to answer it now.
   It is also not asked here because the question may already be answered in the pane.
   Then stop.
   When the user's next message answers it, write the answers through the inbox.
   Number them as the ask was, and quote each answer as the option's text:

   ```
   f10 inbox answer <task-id> --by skill 1=<answer> 2=<answer>
   ```

   Print one line saying where the answer went, as the command printed it (sent to the agent, a drive resumed), and stop.
   An `already answered ...` refusal is a normal end: name who answered.
   The agent resumes in its own tab; this session's badge stays as it is.
3. **A parent, or the session's launches, prints a tree** instead of a block, with a row per child.
   Each row carries its task url and PR url on `url` and `pr` lines.
   A child's files are one call away: `f10 status <child-id>` prints its block.
   An `asking` child carries its question, whole, on an `ask` line under its row.
   Add the same one line as point 2 and stop, with no `AskUserQuestion`.
   When the user's reply answers them, answer each child's ask with its own answers (`f10 inbox answer <child-id> --by skill 1=<answer>`), one command per child in one Bash call.
   No `asking` child: the tree is the whole answer.
   Stop.
4. No `ask` row: the status is the whole answer.
   Stop.
   A task with no live run of its own prints the facts block, with its `plan` and `pr` rows where they exist.
   Then comes `run none for <id>`.
   After it come an `agent` row, saying what the agent in its checkout is on now, and a `pr state` row with the PR's state.
   The `pr state` row also shows under a live run once the task has a PR, and for an open PR it carries the head's checks (`#128 open · checks red (lint)`).
   Relay it as it reads.
   That block never carries an `ask` row.
   A finished task whose branch is gone prints the same block from what `f10 finish` archived.
   The plan, each findings round and verdict file, the handoff and the demo report get a row each.
   Then come `pr state` and its gates as step rows.
