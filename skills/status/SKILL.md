---
name: status
description: "f10 status skill - where an f10 run is (this session's, or a task's in its own worktree), why it stopped, and the question a driven agent is waiting on, relayed and answered. Use when the user says \"/f10:status\" or \"/f10:status <task-id>\", asks where a run is, why it stopped, what the status-line badge means, or what an agent in another workspace is asking."
allowed-tools: Bash, AskUserQuestion
argument-hint: "[task-id]"
---

# f10 · status

Where a run is, in words: the task, the three phases, the step a stopped run stopped on, the
reason, and what unblocks it. The status-line badge draws the same facts as three glyphs.

**Bare `/f10:status`** normally never reaches you: the plugin's `UserPromptExpansion` hook
answers it from `f10 status` and ends the turn before a model runs. With no argument, the hook
did not fire. The answer is below, already run: print it verbatim in a fenced block, add
nothing, and stop.

!`command -v f10 >/dev/null 2>&1 && f10 status || "${CLAUDE_PLUGIN_ROOT}/bin/f10-state.sh" show`

**`/f10:status <task-id>`** asks about another run: the one the task's agent reports from the
checkout that has its branch, usually a worktree `f10 start` opened. The hook lets this form
through, because the answer may need you.

1. Run `f10 status <task-id>` and print its output verbatim in a fenced block. A failure (`f10`
   missing, no branch for the task, no live run there) is printed as is, since it names the
   cause, and ends the turn.
2. **When the output carries an `ask` row**, the agent is driven
   (`${CLAUDE_PLUGIN_ROOT}/modes/driven.md`) and stopped on that question. Put it to the user in
   **one `AskUserQuestion`**: one question per numbered item, the bracketed options as the
   choices, the first as recommended. Then send the answers back as the agent's next prompt,
   numbered as the ask was:

   ```
   f10 forward <task-id> "1. <answer> 2. <answer>"
   ```

   Print forward's block (task, workspace, sent) in a fenced `yaml` block and stop. The agent
   resumes in its own tab; this session's badge stays as it is.
3. No `ask` row: the status is the whole answer. Stop.

If `f10` is not on `PATH`, or fails with `flag provided but not defined`, the binary is missing
or older than this plugin: say so, quote `f10 --version` where it ran, and point at
`go install github.com/amberpixels/f10/cli/cmd/f10@latest`.
