---
name: judge
description: "f10 judge skill - a senior architect's verdict on an idea, a task, a plan or a PR: proceed, proceed with changes, rethink, or stop. Use when the user says \"/f10:judge <idea | task-id | PR>\", asks to roast, sanity-check or challenge something, or doubts whether a change treats the cause or a symptom. Add --blind to judge in a fresh agent with no conversation context."
allowed-tools: Bash, Read, Grep, Glob, Agent, AskUserQuestion, WebSearch, WebFetch
argument-hint: "<idea | task-id | PR number or url | this branch | commit or range> [--blind]"
---

# f10 · judge

Judge the whole thing, not the highlighted spots: the real problem, symptom or cause, what
already exists, the longer-lived shape. **Creates nothing**: no task, no plan edit, no tracker
comment, no code. It ends in a one-word verdict with the argument under it.

**Dry run:** with `--dry-run` in the argument, follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md`.

**Blind:** with `--blind` in the argument, strip the token and judge in a fresh subagent per the
step's blind mode: the subject and the repo, never this conversation.

Load the context (`${CLAUDE_PLUGIN_ROOT}/conventions/context.md`):
`${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh` unless this context already holds it, then
`${CLAUDE_PLUGIN_ROOT}/bin/bundle.sh judge` every run. Follow the **judge** step it prints,
routing the stripped argument by its point 1; read nothing from `steps/`. With no argument, do
not ask what to judge.

Stay in the discussion until the user closes it, per the step. Where the verdict implies a next
f10 skill, offer it in one line (`/f10:capture` after **proceed** on a raw idea, `/f10:plan`
after **rethink** on a task) and leave the call to the user. Never roll on into capturing,
planning, or writing code.
