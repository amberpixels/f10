---
name: debug
description: "f10 debug skill - reproduce and diagnose a bug from an error, a stack trace or a description; only a confirmed bug becomes a task and a plan. Use when the user says \"/f10:debug <error | trace | description>\", pastes an error or stack trace to chase, or says something broke (\"works on staging, not prod\") and no task exists yet."
allowed-tools: Bash, Read, Write, Edit, Grep, Glob, Agent, AskUserQuestion, Skill, ExitPlanMode
argument-hint: "<error | stack trace | description>"
---

# f10 · debug

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- `--dry-run`: follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` instead of running anything, and stop.
  It reports which steps the rest of the argument would run.
- `--driven`: follow `${CLAUDE_PLUGIN_ROOT}/modes/driven.md`; everything below still runs.
- `<error | trace | description>`: the bug report, run through the **debug** step.
  A confirmed cause runs **capture** with the debug brief, then **fetch** and **plan** on the new task id.
  An argument that reports no bug ("let's debug something") counts as none.
- **No argument**:
  - An error or stack trace already in this conversation: that is the report.
    Say in one line which one you took, then start.
  - Nothing in the conversation: ask for the report in one line and wait.
- A `&& /f10:<skill> ...` tail is a chain, run once the plan is saved (`conventions/report.md`).

**Run**
1. Run `f10-load-project.sh debug capture fetch plan` first, on every call of this skill.
2. Follow the **debug** step it prints; on a confirmed cause, **capture**, **fetch** and **plan**, in that order.

**Exit**
- Stop at the saved plan, or at debug's report when the bug does not reproduce.
- Next: `/f10:ship <id>`, once a plan is saved.
