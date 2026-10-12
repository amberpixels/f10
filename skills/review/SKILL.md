---
name: review
description: "f10 review skill - a blind local review of a change into a findings file; with ci, reads in the remote review on the PR or MR. Use when the user says \"/f10:review <task-id | PR | this branch> [ci]\", asks for a code review of a branch or PR, wants bugs found before a PR is opened, or wants the CI review's findings read in. Settling the findings is /f10:resolve."
allowed-tools: Bash, Read, Write, Grep, Glob, Agent, AskUserQuestion
argument-hint: "<task-id | PR | this branch> [ci]"
---

# f10 · review

`/f10:resolve` settles the findings; `/f10:judge` is the verdict on the shape. Running
`/f10:review ci` authorizes the `f10 review ack` the remote half makes.

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- `--dry-run`: follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` instead of running anything,
  and stop. It reports which steps the rest of the argument would run.
- `--driven`: everything below runs; `${CLAUDE_PLUGIN_ROOT}/modes/driven.md` changes how questions
  are asked.
- `ci`: strip it and run the step's remote half.
- `<subject>`: a task id, PR or branch; the step's **Only the change checked out here**
  paragraph reads it.
- No argument: the current branch against its base. Say in one line which branch and base you
  took, then start.

Load the context (`${CLAUDE_PLUGIN_ROOT}/conventions/context.md`):
`${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh` unless this context already holds it, then
`${CLAUDE_PLUGIN_ROOT}/bin/bundle.sh review` every run. Follow the **review** step it prints;
read nothing from `steps/`.

**Exit**
- Stop once the findings file is written.
- Next: `/f10:resolve`.
