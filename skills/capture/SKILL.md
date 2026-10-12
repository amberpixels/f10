---
name: capture
description: "f10 capture skill - turn a freely written idea into a tracker task. Use when the user says \"/f10:capture <description>\" or wants to create a task from a description without planning or building it yet."
allowed-tools: Bash, Read, Write, Edit, Grep, Glob, Agent, AskUserQuestion, Skill
argument-hint: "<free-text task description>"
---

# f10 · capture

Runs the **capture** step only: no plan, no code.

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- `--dry-run`: follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` instead of running anything, and stop.
  It reports which steps the rest of the argument would run.
- `<description>`: the task to create.
  An argument that describes no task ("let's capture something") counts as none.
- No argument:
  - A shape this conversation settled (a brainstorm's closing statement): that is the description.
  - Nothing settled: ask for the description in one line and wait.

**Run**
- `f10-load.sh capture`, every run.
- Follow the **capture** step it prints; read nothing from `steps/`.

**Exit**
- Stop once the task is created.
- Next: `/f10:plan <id>`.
