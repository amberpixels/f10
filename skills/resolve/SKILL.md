---
name: resolve
description: "f10 resolve skill - settle the findings a review wrote: a verdict per finding (fix, skip or ask), fixes made and verified, every ask batched into one questionnaire. Use when the user says \"/f10:resolve <task-id | PR | this branch>\", asks to go through review findings, or wants a review's findings fixed or answered. Producing the findings is /f10:review."
allowed-tools: Bash, Read, Write, Edit, Grep, Glob, AskUserQuestion, Skill
argument-hint: "<task-id | PR | this branch>"
---

# f10 · resolve

Producing the findings is `/f10:review`.
This skill does not authorize a remote `+reply` (`gh pr comment` / `glab mr note`): ask once before the first post.

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- `--dry-run`: follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` instead of running anything, and stop.
  It reports which steps the rest of the argument would run.
- `--driven`: follow `${CLAUDE_PLUGIN_ROOT}/modes/driven.md`; everything below still runs.
- `<subject>`: a task id, PR or branch; the step's point 1 reads it.
- No argument: the current branch.
  Say in one line which findings file you took, then start.

**Run**
- `f10-load.sh review resolve`, every run: both steps, since the findings file's shape lives in the review step.
- Follow the **resolve** step it prints; read nothing from `steps/`.

**Exit**
- Stop once the verdicts are written and verify is green.
- Next: `/f10:review`, when nothing is left to settle.
