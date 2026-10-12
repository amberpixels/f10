---
name: demo
description: "f10 demo skill - see what a shipped change actually does, before you merge it. Use when the user says \"/f10:demo <PR | task-id | this branch>\", asks to be shown what a feature does, wants screenshots or a before/after of a change, or wants a short scenario to click through themselves. --hands-on hands over the script and a running app instead of screenshots; --publish or --local override where the report goes."
argument-hint: "<PR | task-id | this branch> [--hands-on]"
---

# f10 · demo

Evidence of what a change does: not a verdict (`/f10:judge`), not a bug hunt (`/f10:review`).

No `allowed-tools` on purpose: the executor is whatever the `demo` overlay binds (a browser MCP server, a project skill, a plain command), and a fixed list would disable it.

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- `--dry-run`: follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` instead of running anything, and stop.
  It reports which steps the rest of the argument would run.
- `--hands-on`: strip it and run the step's hands-on execution.
- `--publish` / `--local`: strip it; the step's point 10 acts on it.
- `<subject>`: a PR / MR, "this branch", or a task id; the step reads it.
- No argument: the current branch against its base.
  Say in one line which branch and base you took, then start.

**Run**
- `${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh`, unless an earlier call's output is still in this context word for word; a summary of it does not count.
- `${CLAUDE_PLUGIN_ROOT}/bin/bundle.sh demo`, every run.
- Follow the **demo** step it prints; read nothing from `steps/`.

**Exit**
- Stop once the report is written; a hands-on run leaves the app running.
