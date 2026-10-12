---
name: resolve
description: "f10 resolve skill - settle the findings a review wrote: a verdict per finding (fix, skip or ask), fixes made and verified, every ask batched into one questionnaire. Use when the user says \"/f10:resolve <task-id | PR | this branch>\", asks to go through review findings, or wants a review's findings fixed or answered. Producing the findings is /f10:review."
allowed-tools: Bash, Read, Write, Edit, Grep, Glob, AskUserQuestion, Skill
argument-hint: "<task-id | PR | this branch>"
---

# f10 · resolve

Producing the findings is `/f10:review`. This skill does not authorize a remote `+reply`
(`gh pr comment` / `glab mr note`): ask once before the first post.

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md`.
- `--dry-run`: follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md`.
- The step's point 1 routes the argument. No argument: the current branch; name the findings
  file in one line.

Load the context (`${CLAUDE_PLUGIN_ROOT}/conventions/context.md`):
`${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh` unless this context already holds it, then
`${CLAUDE_PLUGIN_ROOT}/bin/bundle.sh review resolve` every run: **both steps**, since the
findings file's shape lives in the review step. Follow the **resolve** step it prints; read
nothing from `steps/`.

With nothing left to settle, offer `/f10:review` in one line as the next step.

Stop once the verdicts are written and verify is green.
