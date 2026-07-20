---
name: capture
description: "f10 pipeline step 1 — turn a task description into a tracker task. Use when the user says \"/f10:capture <description>\" or wants to create a ticket from a description without planning or building it yet."
allowed-tools: Bash, Read, Write, Edit, Grep, Glob, Agent, AskUserQuestion, Skill
argument-hint: "<free-text task description>"
---

# f10 · capture

Create a well-scoped task from a description. Runs the **capture** step only.

**Dry run:** if the argument contains `--dry-run`, follow
`${CLAUDE_PLUGIN_ROOT}/steps/dry-run.md` instead of executing — resolve the context, create
adapter, and drafted target, report them, and change nothing (no task created).

First load the project context per `${CLAUDE_PLUGIN_ROOT}/steps/context.md`, then read and
follow `${CLAUDE_PLUGIN_ROOT}/steps/capture.md`, using the user's argument as the description.

Stop once the task is created and report its identifier (task id / url). Do not plan
or implement — that's `/f10:plan` and `/f10:ship`.
