---
name: judge
description: "f10 judge skill - a senior architect's verdict on an idea, a task, a plan or a PR: proceed, proceed with changes, rethink, or stop. Use when the user says \"/f10:judge <idea | task-id | PR>\", asks to roast, sanity-check or challenge something, or doubts whether a change treats the cause or a symptom. Add --blind to judge in a fresh agent with no conversation context."
allowed-tools: Bash, Read, Grep, Glob, Agent, AskUserQuestion, WebSearch, WebFetch
argument-hint: "<idea | task-id | PR number or url | this branch | commit or range> [--blind]"
---

# f10 · judge

Step back and judge the thing, not the highlighted spots: the real problem, symptom or cause,
what already exists, the longer-lived shape. **Creates nothing** - no task, no plan edit, no
tracker comment, no code. It ends in a one-word verdict with the argument under it.

**Dry run:** if the argument contains `--dry-run`, follow
`${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` - resolve and report, execute nothing.

**Blind:** if the argument contains `--blind`, strip the token and judge in a fresh subagent per
the step's blind mode - the subject and the repo, never this conversation.

First load the context per `${CLAUDE_PLUGIN_ROOT}/conventions/context.md` - two calls:
`${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh` (skip if this context already holds the bundle),
then `${CLAUDE_PLUGIN_ROOT}/bin/resolve.sh judge`, every run. Then follow the **judge** step
that bundle just printed - it carries the step file, so there is nothing left to read from
`steps/`.

**Route by the stripped argument:**
- **Free text**: the idea as written is the subject.
- **A task id** (the project's id format, an id, or a tracker url): the task via the fetch
  adapter, plus the saved plan at `<storage root>/plans/<TASK-ID>.md` when it exists.
- **A PR / MR** (number, url, or "this branch"): the diff and description via the host CLI, plus
  the task and plan the branch name resolves to.
- **A commit or range** (a sha, `<a>..<b>`, or "last N commits"): the diff and messages via
  `git show` / `git diff`, plus the task and plan the commits' branch or messages resolve to.
- **No argument**: whatever this conversation is about. Do not ask what to judge - name the
  target in one line and judge it.

Stay in the discussion until the user closes it, talking normally: the verdict was delivered
once, and the step's banner returns only when an argument changes it. Where the verdict implies
a next f10 skill, offer it in one line - `/f10:capture` after
**proceed** on a raw idea, `/f10:plan` after **rethink** on a task - and leave the call to the
user. Never roll on into capturing, planning, or writing code.
