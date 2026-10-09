---
name: brainstorm
description: "f10 brainstorm skill - think an idea through with a senior engineer before it becomes a task. Use when the user says \"/f10:brainstorm <idea>\" or wants to discuss what to build, whether to build it at all, and how, without creating a task or a plan yet."
allowed-tools: Bash, Read, Grep, Glob, Agent, AskUserQuestion, WebSearch, WebFetch
argument-hint: "<idea, problem, or half-formed proposal>"
---

# f10 · brainstorm

Think an idea through with someone who has read the code. **Creates nothing**: no task, no plan
file, no code. It ends in a shape the user can hand to `/f10:capture`, or in the decision not to
build.

**Dry run:** with `--dry-run` in the argument, follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md`.

Load the context (`${CLAUDE_PLUGIN_ROOT}/conventions/context.md`):
`${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh` unless this context already holds it, then
`${CLAUDE_PLUGIN_ROOT}/bin/bundle.sh brainstorm` every run, for the stack, guardrails and roles
the options must respect. Follow the **brainstorm** step it prints, with the user's argument as
the idea; read nothing from `steps/`.

**No argument:** the idea is whatever the conversation is already about. Do not ask what to
brainstorm: name the open question as you understand it and start.

Stay in the discussion until the user closes it. When the shape settles, **stop there**: name
it, offer `/f10:capture` in one line, and never roll on into capturing, planning, or writing
code. Those are the user's calls.
