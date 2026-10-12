---
name: brainstorm
description: "f10 brainstorm skill - think an idea through with a senior engineer before it becomes a task. Use when the user says \"/f10:brainstorm <idea>\" or wants to discuss what to build, whether to build it at all, and how, without creating a task or a plan yet."
allowed-tools: Bash, Read, Grep, Glob, Agent, AskUserQuestion, WebSearch, WebFetch
argument-hint: "<idea, problem, or half-formed proposal>"
---

# f10 · brainstorm

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md`.
- `--dry-run`: follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md`.
- `<idea>`: the idea to think through. An argument that names no idea ("let's brainstorm")
  counts as none.
- No argument:
  - An open question in this conversation: that is the idea. Say in one line which question
    you took, then start.
  - Nothing open: ask for the idea in one line and wait.

Load the context (`${CLAUDE_PLUGIN_ROOT}/conventions/context.md`):
`${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh` unless this context already holds it, then
`${CLAUDE_PLUGIN_ROOT}/bin/bundle.sh brainstorm` every run, for the stack, guardrails and roles
the options must respect. Follow the **brainstorm** step it prints; read nothing from `steps/`.

**Exit**
- Stay in the discussion until the user closes it.
- Next: `/f10:capture`, once a shape is settled.
