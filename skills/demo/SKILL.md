---
name: demo
description: "f10 demo skill - see what a shipped change actually does, before you merge it. Use when the user says \"/f10:demo <PR | task-id | this branch>\", asks to be shown what a feature does, wants screenshots or a before/after of a change, or wants a short scenario to click through themselves. Add --hands-on to be handed the script and a running app instead of screenshots, and --publish or --local to override where the report goes."
argument-hint: "<PR number or url | task-id | this branch> [--hands-on] [--publish | --local]"
---

# f10 · demo

Show **what** a change does, not how it was built. Says what changed in plain words, derives a
demo script that confirms it, then either runs that script and captures evidence or seeds the
state and hands it to you. **Changes no code**, reaches no verdict, never merges - `/f10:judge`
is the verdict and `/f10:review` the bug hunt.

This skill declares no `allowed-tools` on purpose. The thing that drives the app is whatever the
project's `demo` overlay binds - a browser MCP server, a project skill, a plain command - and f10
cannot know those tool names in advance, so a concrete list here would silently disable the
executor in exactly the projects that configured one.

**Dry run:** if the argument contains `--dry-run`, follow
`${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` - resolve and report, execute nothing.

**Hands-on:** if the argument contains `--hands-on`, strip the token and run the step's hands-on
execution - derive the script and seed the state as always, then leave the app running and hand
the script over instead of capturing evidence.

First load the context per `${CLAUDE_PLUGIN_ROOT}/conventions/context.md` - two calls:
`${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh` (skip if this context already holds the bundle),
then `${CLAUDE_PLUGIN_ROOT}/bin/resolve.sh demo`, every run. Then follow the **demo** step that
bundle just printed - it carries the step file, so there is nothing left to read from `steps/`.

**Route by the stripped argument:**
- **A PR / MR** (number, url, or "this branch"): the diff and description via the host CLI, plus
  the task and plan the branch name resolves to.
- **A task id** (the project's id format, an id, or a tracker url): the task via the fetch
  adapter, its plan at `<storage root>/plans/<TASK-ID>.md`, and the branch or PR it resolves to.
- **No argument**: the current branch against its base. This is the case the skill exists for -
  do not ask what to demo, name the change in one line and demo it.

A project with no `demo` overlay is **offered one** rather than turned away: the step looks for
what the project already has - an e2e config, a dev-server recipe, a seed task, a documented test
user - and puts what it found in a single questionnaire, each answer pre-filled, then writes
`.f10/instructions/demo.md` and carries on with the run that prompted it. Declining degrades that
one run to hands-on and names the fact that is missing - a normal end, not a failure.

The same questionnaire settles where the report goes: a local `report.html` under
`<storage root>/demo/<TASK-ID>/`, a published page as well, or only a published one. Publishing
makes a **private** page on your own account, with a url you may share or not; `--publish` and
`--local` override the project's choice for one run. Nothing reaches the PR or the tracker either
way.
