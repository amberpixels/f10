---
name: review
description: "f10 review skill - a blind local review of a change into a findings file; with ci, reads in the remote review on the PR or MR. Use when the user says \"/f10:review <task-id | PR | this branch> [ci]\", asks for a code review of a branch or PR, wants bugs found before a PR is opened, or wants the CI review's findings read in. Settling the findings is /f10:resolve."
allowed-tools: Bash, Read, Write, Grep, Glob, Agent, AskUserQuestion
argument-hint: "<task-id | PR number or url | this branch> [ci]"
---

# f10 · review

Find the bugs, not the verdict: a reviewer that never saw this conversation reads the full diff
against base and writes its findings to disk. **Changes no code** and settles nothing; that is
`/f10:resolve`. `/f10:judge` is the verdict on the shape.

**Dry run:** with `--dry-run` in the argument, follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md`.

Load the context (`${CLAUDE_PLUGIN_ROOT}/conventions/context.md`):
`${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh` unless this context already holds it, then
`${CLAUDE_PLUGIN_ROOT}/bin/bundle.sh review` every run. Follow the **review** step it prints;
read nothing from `steps/`.

**Remote:** with the word `ci` in the argument, strip it and run the step's remote half (the
review the declared reviewer left on the PR or MR, via `f10 review pick|wait|ack`). A project
declaring no remote review fails the step; never run a local review in its place. Never perform
the trigger; name a manual one back to the user.

**Route by the argument.** The argument names the task; the change reviewed is always the one
checked out here (the step's "Only the change checked out here"):
- **A task id** (the project's id format, an id, or a tracker url): the task via the fetch
  adapter.
- **A PR / MR** (number, url, or "this branch"): the head branch via the host CLI, and the task
  the branch name points to.
- **No argument**: the current branch against its base. Do not ask what to review: name the
  change in one line and review it.

Rounds, and when a run declines, follow the step's **Rounds** section.

Stop once the file is written and report it per `${CLAUDE_PLUGIN_ROOT}/conventions/report.md`.
Then offer `/f10:resolve` in one line and leave the call to the user. Never fix a finding from
here.
