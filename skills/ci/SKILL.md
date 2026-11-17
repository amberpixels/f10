---
name: ci
description: "f10 ci skill - triage a red CI run on the task's PR: read the first real failure's log, run verify, then make one fix round when the change broke it, rerun the failed jobs once when it flaked, and ask when the default branch is red too. Use when the user says \"/f10:ci [<task-id>]\", asks why CI is red on a PR, or wants a red run fixed or rerun."
allowed-tools: Bash, Read, Edit, Write, Grep, Glob, AskUserQuestion
argument-hint: "[<task-id>] [steering]"
---

# f10 · ci

Running this skill authorizes three actions, for this task's PR only:
- the fix round's commit and its push, which is never forced
- rerunning the PR's failed jobs
- the push of a catchup the step's guard integrates

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop.
  Nothing below applies.
- `--dry-run`: follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` instead of running anything, and stop.
- `--driven`: follow `${CLAUDE_PLUGIN_ROOT}/modes/driven.md`.
  Everything below still runs.
  The drive and the watcher send it this way.
- `<task-id>`: follow `${CLAUDE_PLUGIN_ROOT}/modes/forward.md` first, before loading anything.
  Stop if it forwards the task.
  Otherwise the subject is the PR of the branch checked out here.
- No argument: the PR of the branch checked out here.
- Extra text is steering ("rerun only", "the lint job is known to flake").

**Run**
1. Run `f10-load-project.sh ci catchup` first, on every call of this skill.
2. Follow the **ci** step it prints.

**Exit**
- Stop at the step's report.
  The report shows a fix pushed, jobs rerun, an ask, or checks no longer red.
  Unless the step asked, run `f10-state.sh done ci` before the report, because the drive and the watcher read it as the end of the turn they sent.
