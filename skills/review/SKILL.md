---
name: review
description: "f10 review skill - a blind local review of a change: a fresh agent reads the diff against base and writes a findings file, one block per finding with a concrete fix. With ci, reads the remote review that arrived on the PR or MR instead. Use when the user says \"/f10:review <task-id | PR | this branch> [ci]\", asks for a code review of a branch or PR, wants bugs found before a PR is opened, or wants the CI review's findings read in. Settling the findings is /f10:resolve."
allowed-tools: Bash, Read, Write, Grep, Glob, Agent, AskUserQuestion
argument-hint: "<task-id | PR number or url | this branch> [ci]"
---

# f10 · review

Find the bugs, not the verdict: a reviewer that never saw this conversation reads the full diff
against base and writes what it found to disk, in a shape the settling half reads without
knowing who wrote it. **Changes no code** and settles nothing - that is `/f10:resolve`.
`/f10:judge` is the verdict on the shape; this is the hunt in the code.

**Dry run:** if the argument contains `--dry-run`, follow
`${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` - load and report, execute nothing.

First load the context per `${CLAUDE_PLUGIN_ROOT}/conventions/context.md` - two calls:
`${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh` (skip if this context already holds the bundle),
then `${CLAUDE_PLUGIN_ROOT}/bin/bundle.sh review`, every run. Then follow the **review** step
that bundle just printed - it carries the step file, so there is nothing left to read from
`steps/`.

**Remote:** if the argument contains the word `ci`, strip it and run the step's remote half -
the review the declared reviewer left on the PR or MR, read through `f10 review pick|wait|ack`
per the four facts under `project.md → Review`, written in the same findings shape. A project
declaring no remote review ends in the step's failure, not a local review in its place. The
trigger is never performed from here: a manual one is named back to you.

**Route by the argument.** The change reviewed is always the one checked out here - the step
diffs this working tree and writes under this checkout's storage root - so the argument names
the task, never another tree:
- **A task id** (the project's id format, an id, or a tracker url): the task via the fetch
  adapter. Its branch must be the current one; when it is not, say which worktree holds it
  (`git worktree list`), or that none does, and stop - a normal end.
- **A PR / MR** (number, url, or "this branch"): the same, with the head branch read via the
  host CLI and the task the branch name points to.
- **No argument**: the current branch against its base. Do not ask what to review - name the
  change in one line and review it.

The findings land at `<storage root>/reviews/<TASK-ID>/<round>.md`. A second run on the same
task needs a settled first round: it reads the first round's file, with its verdicts, and
reports a status for every finding there before anything new. A second run on a first round
nobody resolved declines and points at `/f10:resolve`, and so does a third run, for what is
still open - a normal end, not a failure.

Stop once the file is written and report it per `${CLAUDE_PLUGIN_ROOT}/conventions/report.md` -
a `findings` row with its path, the verdict and the count per category as prose. Then offer
`/f10:resolve` in one line and leave the call to the user. Never fix a finding from here.
