---
name: judge
description: "f10 judge skill - a senior architect's verdict on an idea, a task, a plan or a PR: proceed, proceed with changes, rethink, or stop. Use when the user says \"/f10:judge <idea | task-id | PR>\", asks to roast, sanity-check or challenge something, or doubts whether a change treats the cause or a symptom. Add --blind to judge in a fresh agent with no conversation context."
allowed-tools: Bash, Read, Grep, Glob, Agent, AskUserQuestion, WebSearch, WebFetch
argument-hint: "<idea | task-id | PR | this branch> [--blind]"
---

# f10 · judge

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md`.
- `--dry-run`: follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md`.
- `--blind`: strip it and follow the step's **Blind** paragraph.
- `<subject>`: an idea, task id, PR, branch, sha or range; the step's point 1 routes it.
- No argument: what this conversation is about, named back in one line.
- No subject at all (empty with nothing open, or only an invitation): ask for it in one line
  and wait.

Load the context (`${CLAUDE_PLUGIN_ROOT}/conventions/context.md`):
`${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh` unless this context already holds it, then
`${CLAUDE_PLUGIN_ROOT}/bin/bundle.sh judge` every run. Follow the **judge** step it prints; read
nothing from `steps/`.

Stay in the discussion until the user closes it, talking normally while they argue. Where the
verdict implies a next f10 skill, offer it in one line (`/f10:capture` after **proceed** on a
raw idea, `/f10:plan` after **rethink** on a task); the call is the user's.
