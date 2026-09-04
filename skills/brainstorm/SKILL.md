---
name: brainstorm
description: "f10 brainstorm skill - think an idea through with a senior engineer before it becomes a task. Use when the user says \"/f10:brainstorm <idea>\" or wants to discuss what to build, whether to build it at all, and how, without creating a task or a plan yet."
allowed-tools: Bash, Read, Grep, Glob, Agent, AskUserQuestion, WebSearch, WebFetch
argument-hint: "<idea, problem, or half-formed proposal>"
---

# f10 · brainstorm

Think an idea through with someone who has read the code. **Creates nothing** - no task, no plan
file, no code. It ends in a shape the user can hand to `/f10:capture`, or in the decision not to
build.

**Dry run:** if the argument contains `--dry-run`, follow
`${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` - resolve and report, execute nothing.

First load the context per `${CLAUDE_PLUGIN_ROOT}/conventions/context.md` - two calls:
`${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh` (skip if this context already holds the bundle),
then `${CLAUDE_PLUGIN_ROOT}/bin/resolve.sh brainstorm`, every run. The project facts are what
keep the options honest - the stack, the guardrails, the roles this project attaches. Then
follow the **brainstorm** step that bundle just printed, with the user's argument as the idea;
it carries the step file, so there is nothing left to read from `steps/`.

**No argument:** the idea is whatever the conversation is already about. Do not ask what to
brainstorm - name what you take the open question to be and start on it.

Stay in the discussion until the user closes it. When the shape settles, **stop there**: name
it, offer `/f10:capture` in one line, and do not roll on into capturing, planning, or writing
code. Those are the user's calls, and the moment before they are made is the entire point of
this skill.
