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

**Relay the output verbatim, never redrawn.**
Never reorder it, summarise it, or turn it into a table, because the binary already aligned it and a redraw drops rows and colors.

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- No argument: the answer is below, already run: the current task's (the session's, else the branch's), as `f10 status <task-id>` prints it, or the session's run when no task is named.
  In a checkout whose branch names no task, the main one, a session that started or drove tasks gets a `launched here` tree instead: a row per task still live, each start's and each running drive's, nested by `After:` as a parent's tree is.
  Print it as point 1 does, then follow points 2-4 with the task the output names; a `launched here` tree is followed as point 3 follows a parent's.
  The hook answers a bare call itself, with no model turn, unless the output holds a question: an `ask` row in a block, or an `asking` row in a tree.
- A task id: another run, the one the task's agent reports from the checkout that has its branch, usually a worktree `f10 start` opened.
  The hook answers this form itself too, with no model turn, on the same rule: you see it only when the output holds a question, when the argument names no task, or when reading the task takes longer than 3 seconds.
  Follow the numbered points below.

!`command -v f10 >/dev/null 2>&1 && f10 status --plain || "${CLAUDE_PLUGIN_ROOT}/bin/f10-state.sh" show`

1. Run `f10 status <task-id> --plain`.
   Print the output verbatim in a fenced block, as the rule above says.
   Print nothing above the block, not even a summary line, because the block is the answer and a sentence over it only restates it.
   One task always prints a block, whether its run is live, `run none` or finished.
   A block opens with `task:`, so it goes in a ```` ```yaml ```` fence, which colors it as the ship report is colored.
   Several tasks (a parent's children, `launched here`) always print a tree, which goes in a plain fence, because its titles break the highlighting.
   A failure is printed as is and ends the turn.
2. **When the output carries an `ask` row**, the agent is driven (`${CLAUDE_PLUGIN_ROOT}/modes/driven.md`) and stopped on that question.
   Put it to the user in **one `AskUserQuestion`**: one question per numbered item, the bracketed options as the choices, the first as recommended.
   Then write the answers through the asks inbox, numbered as the ask was, each answer the option's text, quoted:

   ```
   f10 asks answer <task-id> --by skill 1=<answer> 2=<answer>
   ```

   Print one line saying where the answer went, as the command printed it (sent to the agent, a drive resumed), and stop.
   An `already answered ...` refusal is a normal end: name who answered.
   The agent resumes in its own tab; this session's badge stays as it is.
3. **A parent, or the session's launches, prints a tree**, a row per child, instead of a block, each row with its task url and PR url on `url` and `pr` lines.
   A child's files are one call away: `f10 status <child-id>` prints its block.
   An `asking` child carries its question, whole, on an `ask` line under its row: put every one to the user in **one `AskUserQuestion`**, the questions grouped by child id, and answer each child's ask with its own answers (`f10 asks answer <child-id> --by skill 1=<answer>`), one command per child in one Bash call.
   No `asking` child: the tree is the whole answer.
   Stop.
4. No `ask` row: the status is the whole answer.
   Stop.
   A task with no live run of its own prints the facts block, with its `plan` and `pr` rows where they exist, then `run none for <id>`, what the agent in its checkout is on now (`agent`), and the PR's state (`pr state`).
   The `pr state` row also shows under a live run once the task has a PR, and for an open PR it carries the head's checks (`#128 open · checks red (lint)`).
   Relay it as it reads.
   That block never carries an `ask` row.
   A finished task, its branch gone, prints the same block from what `f10 finish` archived: the plan, each findings round and verdict file, the handoff and the demo report, a row each, then `pr state` and its gates as step rows.
