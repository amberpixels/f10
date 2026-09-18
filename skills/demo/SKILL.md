---
name: demo
description: "f10 demo skill - see what a shipped change actually does, before you merge it. Use when the user says \"/f10:demo <PR | task-id | this branch>\", asks to be shown what a feature does, wants screenshots or a before/after of a change, or wants a short scenario to click through themselves. Add --hands-on to be handed the script and a running app instead of screenshots."
argument-hint: "<PR number or url | task-id | this branch> [--hands-on]"
---

# f10 · demo

Show **what** a change does, not how it was built. Derives a demo script from the task, the plan
and the diff, then either runs it and captures evidence or seeds the state and hands it to you.
**Changes no code**, reaches no verdict, never merges - `/f10:judge` is the verdict and
`/f10:review` the bug hunt.

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

A project with no `demo` overlay still runs: with no declared way to drive the app there is
nothing to capture, so the run degrades to hands-on and names the fact that was missing. That is
a normal end, not a failure.

The evidence and its `report.html` stay under `<storage root>/demo/<TASK-ID>/`. Nothing is posted
to the PR, the tracker or any hosted page unless the user asks for it outright.
