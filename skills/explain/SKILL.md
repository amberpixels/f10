---
name: explain
description: "f10 explain skill - what a change was, and what it is now, for a reader who knows the project but has not seen this diff. Use when the user says \"/f10:explain <this branch | PR | task-id>\", asks what a branch or a PR is about, wants catching up on what was just built, or wants what-was-what-is without running anything."
allowed-tools: Bash, Read, Grep, Glob
argument-hint: "<this branch | PR number or url | task-id | commit or range | local>"
---

# f10 · explain

Say what a change **was** and what it **is** now, to someone who knows the project and has not
seen this diff. **Creates nothing** - no file, no tracker comment, no code, no PR comment. It
reaches no verdict (`/f10:judge`), hunts no bugs (`/f10:review`) and runs nothing (`/f10:demo`).

**Dry run:** if the argument contains `--dry-run`, follow
`${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` - resolve and report, execute nothing.

First load the context per `${CLAUDE_PLUGIN_ROOT}/conventions/context.md` - two calls:
`${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh` (skip if this context already holds the bundle),
then `${CLAUDE_PLUGIN_ROOT}/bin/resolve.sh explain`, every run. Then follow the **explain** step
that bundle just printed - it carries the step file, so there is nothing left to read from
`steps/`.

**Route by the argument:**
- **No argument**: the current branch against its base. This is the case the skill exists for -
  do not ask what to explain, name the subject in one line and explain it.
- **A PR / MR** (number, url, or "this branch"): the diff and description via the host CLI, plus
  the task and plan the branch name resolves to.
- **A task id** (the project's id format, an id, or a tracker url): the task via the fetch
  adapter, its plan at `<storage root>/plans/<TASK-ID>.md`, and the branch or PR it resolves to.
- **A commit or range** (a sha, `<a>..<b>`, or "last N commits"): the diff and messages via
  `git show` / `git diff`.
- **`local`, `staged`, `uncommitted`**: the working tree, for the change that is not committed
  yet.
- **A path**: not a subject. A file is code to read, not a change to explain - say so in one line
  and name the changes on offer instead.

The subject is always a **diff**. Where the task or the plan says something the diff does not do,
the diff wins and the difference gets said.

Stay in the conversation afterwards, talking normally: the explanation was given once, and the
follow-up question is a question, not a request to explain it again. Where it leads somewhere -
a doubt about the shape, a bug worth confirming, a thing worth seeing run - offer the skill in
one line (`/f10:judge`, `/f10:review`, `/f10:demo`) and leave the call to the user.
