---
name: audit
description: "f10 audit skill - a big-picture review of what shipped over a window (since the last audit, a ref, a date, or the whole codebase): reviewer agents split by area plus cross-cutting and history lenses, every claimed bug verified in the code, one report under the storage root, and an offer to capture it sized to the findings: nothing when it found nothing, one task with a short checklist when everything is minor, a parent with subtasks only for heavy themes. Use when the user says \"/f10:audit [since <ref | date> | all]\", asks for an audit, a codebase health check, how recent work fits together, or what to refactor after a run of features."
allowed-tools: Bash, Read, Write, Edit, Grep, Glob, Agent, AskUserQuestion, Skill
argument-hint: "[since <ref | date> | all]"
---

# f10 · audit

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- `--dry-run`: follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` instead of running anything, and stop.
  It reports which steps the rest of the argument would run.
- `since <ref | date>`: the window starts at that ref, or at the last commit before that date.
- `all`: the whole codebase at HEAD.
- No argument: the window starts where the last audit ran, else at the last tag.
  Say in one line which start you took, then begin.

**Run**
1. Run `f10-load-project.sh audit capture` first, on every call of this skill.
2. Follow the **audit** step it prints, and its **Capture** section after the report.

**Exit**
- Stop at the report, or once the capture it offered has created the tasks.
- Next: `/f10:start <task>` for one task, or `/f10:drive <parent>` once a parent and its subtasks exist.
