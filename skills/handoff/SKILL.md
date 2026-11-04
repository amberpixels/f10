---
name: handoff
description: "f10 handoff skill - what a change leaves for whoever ships it: schema changes and backfills, jobs to run by hand, settings and when to set them, deploy order, breaks for consumers and accepted debt, written to a file and rewritten only when the branch head moves. Use when the user says \"/f10:handoff [<task-id | PR | this branch>]\", or asks what to run, set or warn about after a merge or deploy."
allowed-tools: Bash, Read, Write, Grep, Glob
argument-hint: "[<task-id | PR | this branch>]"
---

# f10 · handoff

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- `--dry-run`: follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` instead of running anything, and stop.
  It reports which steps the rest of the argument would run.
- `<subject>`: a task id, a PR / MR, or "this branch"; the step reads it.
- No argument: the current branch against its base.
  Say in one line which branch and base you took, then start.

**Run**
1. Run `f10-load-project.sh handoff` first, on every call of this skill.
2. Follow the **handoff** step it prints.

**Exit**
- Stop at the written or reused handoff file.
- Next: `/f10:finish <id>`.
