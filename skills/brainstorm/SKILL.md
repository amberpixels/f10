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
- `<idea>`: the idea to think through.
- No argument: the open question this conversation is on, named back in one line.
- No idea at all (empty with nothing open, or only an invitation to talk): ask for it in one
  line and wait.

Load the context (`${CLAUDE_PLUGIN_ROOT}/conventions/context.md`):
`${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh` unless this context already holds it, then
`${CLAUDE_PLUGIN_ROOT}/bin/bundle.sh brainstorm` every run, for the stack, guardrails and roles
the options must respect. Follow the **brainstorm** step it prints; read nothing from `steps/`.

Stay in the discussion until the user closes it; capturing or planning it is their call.
