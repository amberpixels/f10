---
name: brainstorm
description: "f10 brainstorm skill - think an idea through with a senior engineer before it becomes a task. Use when the user says \"/f10:brainstorm <idea>\" or wants to discuss what to build, whether to build it at all, and how, without creating a task or a plan yet."
allowed-tools: Bash, Read, Grep, Glob, Agent, AskUserQuestion, WebSearch, WebFetch
argument-hint: "<idea, problem, or half-formed proposal>"
---

# f10 · brainstorm

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- `--dry-run`: follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` instead of running anything, and stop.
  It reports which steps the rest of the argument would run.
- `<idea>`: the idea to think through.
  An argument that names no idea ("let's brainstorm") counts as none.
- No argument:
  - An open question in this conversation: that is the idea.
    Say in one line which question you took, then start.
  - Nothing open: ask for the idea in one line and wait.

**Run**
- The conventions are in this context already: the plugin's hooks inject them when a skill starts and again after compaction. Never run `conventions.sh` here.
- `${CLAUDE_PLUGIN_ROOT}/bin/bundle.sh brainstorm`, every run.
- Follow the **brainstorm** step it prints; read nothing from `steps/`.

**Exit**
- Stay in the discussion until the user closes it.
- Next: `/f10:capture`, once a shape is settled.
