---
name: capture
description: "f10 capture skill - turn a freely written idea into a tracker task. Use when the user says \"/f10:capture <description>\" or wants to create a task from a description without planning or building it yet."
allowed-tools: Bash, Read, Write, Edit, Grep, Glob, Agent, AskUserQuestion, Skill
argument-hint: "<free-text task description>"
---

# f10 · capture

Create a well-scoped task from a description. Runs the **capture** step only.

**Dry run:** if the argument contains `--dry-run`, follow
`${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` instead of executing - resolve the context, create
adapter, and drafted target, report them, and change nothing (no task created).

First load the context per `${CLAUDE_PLUGIN_ROOT}/conventions/context.md` - two calls:
`${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh`, **unless this context already holds the conventions
bundle**, then `${CLAUDE_PLUGIN_ROOT}/bin/resolve.sh capture`, every run. Then read and
follow `${CLAUDE_PLUGIN_ROOT}/steps/capture.md`, using the user's argument as the description.

Stop once the task is created and report it - task id and url - per
`${CLAUDE_PLUGIN_ROOT}/conventions/report.md`. Do not plan or implement - that's `/f10:plan` and
`/f10:ship`.
