---
name: resolve
description: "f10 resolve skill - settle the findings a review wrote: a verdict per finding (fix, skip or ask), fixes made and verified, every ask batched into one questionnaire; with ci, reads in the latest CI review first and marks it settled. Use when the user says \"/f10:resolve <task-id | PR | this branch> [ci]\", asks to go through review findings, or wants a review's findings, or the CI review on the PR, fixed or answered. Producing the findings is /f10:review."
allowed-tools: Bash, Read, Write, Edit, Grep, Glob, AskUserQuestion, Skill
argument-hint: "<task-id | PR | this branch> [ci]"
---

# f10 · resolve

Producing the findings is `/f10:review`.
This skill does not authorize a remote `+reply` (`gh pr comment` / `glab mr note`): ask once before the first post.
Landing fixes on an open PR or MR (the step's point 9): a `--driven` run commits and pushes, since the user started its chain; otherwise ask once, inside the step's questionnaire, **commit and push**, **commit only**, or **leave uncommitted**.
With `review_traces: on`, the step's point 10 status and comment need no confirmation of their own: the project declared them. They are withheld only when fixes exist and were not pushed.
Running `/f10:resolve ci` authorizes the `f10 review ack` and `f10 review settle` its **CI review** section makes.

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- `--dry-run`: follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` instead of running anything, and stop.
  It reports which steps the rest of the argument would run.
- `--driven`: follow `${CLAUDE_PLUGIN_ROOT}/modes/driven.md`; everything below still runs.
- `ci`: strip it and run the step's **CI review** section around the rest.
- `<subject>`: a task id, PR or branch; the step's point 1 reads it.
- No argument: the current branch.
  Say in one line which findings file you took, then start.

**Run**
1. Run `f10-load-project.sh review resolve` first, on every call of this skill: both steps, since the findings file's shape lives in the review step.
2. Follow the **resolve** step it prints.

**Exit**
- Stop once the verdicts are written, verify is green, and the fixes are landed or left as point 9 says.
- Next: `/f10:review`, when nothing is left to settle.
