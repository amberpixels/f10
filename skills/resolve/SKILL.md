---
name: resolve
description: "f10 resolve skill - settle the findings a review wrote: a verdict per finding (fix, skip or ask), fixes made and verified, every ask batched into one questionnaire. Use when the user says \"/f10:resolve <task-id | PR | this branch>\", asks to go through review findings, or wants a review's findings fixed or answered. Producing the findings is /f10:review."
allowed-tools: Bash, Read, Write, Edit, Grep, Glob, AskUserQuestion, Skill
argument-hint: "<task-id | PR number or url | this branch>"
---

# f10 · resolve

Settle the findings a review wrote. Producing them is `/f10:review`.

**Dry run:** with `--dry-run` in the argument, follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md`.

Load the context (`${CLAUDE_PLUGIN_ROOT}/conventions/context.md`):
`${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh` unless this context already holds it, then
`${CLAUDE_PLUGIN_ROOT}/bin/bundle.sh review resolve` every run: **both steps**, since the
findings file's shape lives in the review step. Follow the **resolve** step it prints; read
nothing from `steps/`. The step's point 1 routes the argument (a task id, a PR / MR, "this
branch") to the findings under `<storage root>/reviews/<TASK-ID>/`.

**No argument**: the current branch. Do not ask which review: name the file in one line and
settle it.

With nothing left to settle, offer `/f10:review` in one line as the next step.

Stop once the verdicts are written and verify is green, and report per
`${CLAUDE_PLUGIN_ROOT}/conventions/report.md`. Nothing is committed or pushed from here. This
skill does not authorize a remote `+reply` (`gh pr comment` / `glab mr note`): ask once before
the first post.
