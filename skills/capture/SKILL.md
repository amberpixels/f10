---
name: capture
description: "f10 capture skill - turn a freely written idea into a tracker task. Use when the user says \"/f10:capture <description>\" or wants to create a task from a description without planning or building it yet."
allowed-tools: Bash, Read, Write, Edit, Grep, Glob, Agent, AskUserQuestion, Skill
argument-hint: "<free-text task description>"
---

# f10 · capture

Runs the **capture** step only: no plan, no code.

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md`.
- `--dry-run`: follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md`.
- The description is the argument, else the shape this conversation settled (a brainstorm's
  closing statement). Neither holds one: ask for it in one line and wait.

Load the context (`${CLAUDE_PLUGIN_ROOT}/conventions/context.md`):
`${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh` unless this context already holds it, then
`${CLAUDE_PLUGIN_ROOT}/bin/bundle.sh capture` every run. Follow the **capture** step it prints;
read nothing from `steps/`.

Stop once the task is created; `/f10:plan` takes it from there.
