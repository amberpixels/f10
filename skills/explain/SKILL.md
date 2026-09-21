---
name: explain
description: "f10 explain skill - explain one thing to a peer who knows the project but not this thing: a change (what it was, what it is now) or a concept, file, package or function (what it is, in a few sentences from the code). Use when the user says \"/f10:explain <this branch | PR | task-id | concept | path | symbol>\", asks what a branch or a PR is about, asks what some term, module or function in this codebase is, or wants catching up without running anything."
allowed-tools: Bash, Read, Grep, Glob
argument-hint: "<this branch | PR | task-id | commit or range | local | concept | path | function>"
---

# f10 · explain

Explain one thing to someone who knows the project and has not met this thing. A change: what
it **was** and what it **is** now. A concept, a file, a package, a function: what it **is**, the
way a senior dev answers a peer in a hallway - a few sentences from the code, and done.
**Creates nothing** - no file, no tracker comment, no code, no PR comment. It reaches no verdict
(`/f10:judge`), hunts no bugs (`/f10:review`) and runs nothing (`/f10:demo`).

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
- **Anything else is a thing, not a change.** A path that exists is a file or a package; a
  bare word or phrase is a concept or a symbol, found by grepping the code. Explained as a thing:
  what it is, what it drags along that the name hides, where to look. Prose around the subject
  ("very short", "is it just X?") is the asker's framing - honour it, do not parse it.

For a change the subject is the **diff**: where the task or the plan says something the diff
does not do, the diff wins and the difference gets said. For a thing the subject is the **code**:
where the README or a comment says something the code does not do, the code wins.

Stay in the conversation afterwards, talking normally: the explanation was given once, and the
follow-up question is a question, not a request to explain it again. Where it leads somewhere -
a doubt about the shape, a bug worth confirming, a thing worth seeing run - offer the skill in
one line (`/f10:judge`, `/f10:review`, `/f10:demo`) and leave the call to the user.
