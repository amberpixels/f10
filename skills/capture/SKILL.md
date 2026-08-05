---
name: capture
description: "f10 capture skill - turn a freely written idea into a tracker task. Use when the user says \"/f10:capture <description>\" or wants to create a task from a description without planning or building it yet."
allowed-tools: Bash, Read, Write, Edit, Grep, Glob, Agent, AskUserQuestion, Skill
argument-hint: "<free-text task description>"
---

# f10 · capture

Create a well-scoped task from a description. Runs the **capture** step only.

**Dry run:** if the argument contains `--dry-run`, follow
`${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` - resolve and report, execute nothing.

First load the context per `${CLAUDE_PLUGIN_ROOT}/conventions/context.md` - two calls:
`${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh` (skip if this context already holds the bundle),
then `${CLAUDE_PLUGIN_ROOT}/bin/resolve.sh capture`, every run. Then follow the **capture** step
that bundle just printed, with the user's argument as the description - it carries the step
file, so there is nothing left to read from `steps/`.

Stop once the task is created and report it - task id and url - per
`${CLAUDE_PLUGIN_ROOT}/conventions/report.md`. Do not plan or implement - that's `/f10:plan` and
`/f10:ship`.
