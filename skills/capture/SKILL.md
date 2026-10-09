---
name: capture
description: "f10 capture skill - turn a freely written idea into a tracker task. Use when the user says \"/f10:capture <description>\" or wants to create a task from a description without planning or building it yet."
allowed-tools: Bash, Read, Write, Edit, Grep, Glob, Agent, AskUserQuestion, Skill
argument-hint: "<free-text task description>"
---

# f10 · capture

Create a well-scoped task from a description. Runs the **capture** step only.

**Dry run:** with `--dry-run` in the argument, follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md`.

Load the context (`${CLAUDE_PLUGIN_ROOT}/conventions/context.md`):
`${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh` unless this context already holds it, then
`${CLAUDE_PLUGIN_ROOT}/bin/bundle.sh capture` every run. Follow the **capture** step it prints,
with the user's argument as the description; read nothing from `steps/`.

Stop once the task is created and report its id and url per
`${CLAUDE_PLUGIN_ROOT}/conventions/report.md`. Do not plan or implement; that is `/f10:plan` and
`/f10:ship`.
