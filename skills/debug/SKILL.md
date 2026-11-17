---
name: debug
description: "f10 debug skill - investigate a report (an error, a stack trace, log lines, a screenshot of a user complaint, or a description) down to its cause in the code, reproducing it only when that is the cheapest proof; each confirmed bug becomes a task and a plan, and a report that holds no bug gets an answer. Use when the user says \"/f10:debug <report>\", pastes an error, a trace, logs or a screenshot of a complaint to chase, asks why something happened or did not, or says something broke (\"works on staging, not prod\") and no task exists yet."
allowed-tools: Bash, Read, Write, Edit, Grep, Glob, Agent, AskUserQuestion, Skill, ExitPlanMode
argument-hint: "<error | trace | logs | screenshot | description>"
---

# f10 · debug

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- `--dry-run`: follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` instead of running anything, and stop.
  It reports which steps the rest of the argument would run.
- `--driven`: follow `${CLAUDE_PLUGIN_ROOT}/modes/driven.md`; everything below still runs.
- `<report>`: an error, a stack trace, log lines, a screenshot or a complaint in plain words, run through the **debug** step.
  Each bug with a confirmed cause takes the path the debug step chooses (its point 6).
  The usual path runs **capture** with its debug brief, then **fetch**, **investigate** and **plan** on the new task id, one bug after another.
  For an urgent bug with a small fix, a quicker flow the project declares may be suggested, and it runs only when the user picks it.
  An argument that reports nothing ("let's debug something") counts as none.
  A bug that already is a task needs no call here: investigate runs the same step file on it before planning.
- No argument:
  - An error, a trace, logs or a screenshot already in this conversation: that is the report.
    Say in one line which one you took, then start.
  - Nothing in the conversation: ask for the report in one line and wait.

**Run**
1. Run `f10-load-project.sh debug capture fetch investigate plan` first, on every call of this skill.
2. Follow the **debug** step it prints.
   For each bug with a confirmed cause, run the path it chose.
   The usual path runs **capture**, then **fetch**, **investigate** and **plan**.
   The other is the quicker flow the user picked.

**Exit**
- Stop at the saved plans, unless the argument chains a skill: then run it.
  A quicker flow the user picked ends where `/f10:ship` ends.
  Stop at debug's report when the report holds no bug, or when its cause stays unsettled.
- Next: `/f10:ship <id>`, once a plan is saved.
