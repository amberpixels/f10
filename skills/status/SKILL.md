---
name: status
description: "f10 status skill - where the current f10 run is: task, phases, the step it stopped on, why, and what unblocks it. Use when the user says \"/f10:status\" or asks where the run is, why it stopped, or what the status-line badge means."
allowed-tools: Bash
---

# f10 · status

Where this session's run is, in words: the task, the three phases, the step a stopped run
stopped on, the reason, and what unblocks it. The same facts the status-line badge draws as
three glyphs.

Normally this skill never reaches you: the plugin's `UserPromptExpansion` hook answers
`/f10:status` itself, from `f10 status`, and ends the turn before a model runs. You are reading
this because the hook did not fire. The answer is below, already run; print it verbatim in a
fenced block, add nothing before or after it, and stop.

!`command -v f10 >/dev/null 2>&1 && f10 status || "${CLAUDE_PLUGIN_ROOT}/bin/f10-state.sh" show`
