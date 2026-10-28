---
name: judge
description: "f10 judge skill - a senior architect's verdict on an idea, a task, a plan or a PR: proceed (green), proceed with changes (yellow), rethink (orange), or stop (red). Use when the user says \"/f10:judge [<idea | task-id | PR>]\", asks to roast, sanity-check or challenge something, or doubts whether a change treats the cause or a symptom. Add --blind to judge in a fresh agent with no conversation context."
allowed-tools: Bash, Read, Write, Edit, Grep, Glob, Agent, AskUserQuestion, WebSearch, WebFetch
argument-hint: "[<idea | task-id | PR | this branch>] [--blind]"
---

# f10 · judge

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- `--dry-run`: follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` instead of running anything, and stop.
  It reports which steps the rest of the argument would run.
- `--driven`: follow `${CLAUDE_PLUGIN_ROOT}/modes/driven.md`; everything below still runs.
- `--blind`: strip it and follow the step's **Blind** paragraph.
- `<subject>`: an idea, task id, PR, branch, sha or range; the step's point 1 reads it.
  An argument that names no subject ("let's judge something") counts as none.
- No argument:
  - A subject in this conversation: judge that.
    Say in one line which subject you took, then start.
  - Nothing open, on a branch other than the default branch: that branch against its base (the base `steps/review.md` names), when it has commits or uncommitted changes past it.
    No change yet: the task `f10 task current` prints, judged as `task <id>`.
    Say in one line which subject you took, then start.
  - Otherwise: ask for the subject in one line and wait.

**Run**
1. Run `f10-load-project.sh judge` first, on every call of this skill.
2. Follow the **judge** step it prints.
   Every run writes its verdict file (point 8).
   On a PR nothing else leaves chat: pushing changes and posting a status or comment happen only in a judge entry of `/f10:ship`.

**Exit**
- Stay in the discussion until the user closes it, talking normally while they argue.
- Next: `/f10:capture` after **proceed** on a raw idea, `/f10:plan` after **rethink** on a task.
