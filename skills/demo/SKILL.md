---
name: demo
description: "f10 demo skill - see what a shipped change actually does, before you merge it. Use when the user says \"/f10:demo <PR | task-id | this branch>\", asks to be shown what a feature does, wants screenshots or a before/after of a change, or wants a short scenario to click through themselves. --hands-on hands over the script and a running app instead of screenshots; --publish or --local override where the report goes."
argument-hint: "<PR number or url | task-id | this branch> [--hands-on] [--publish | --local]"
---

# f10 · demo

Show **what** a change does, not how it was built: say what changed in plain words, derive a
demo script that confirms it, then either run it and capture evidence or seed the state and
hand it over. **Changes no code**, reaches no verdict, never merges; `/f10:judge` is the verdict
and `/f10:review` the bug hunt.

No `allowed-tools` on purpose: the executor is whatever the `demo` overlay binds (a browser MCP
server, a project skill, a plain command), and a fixed list would disable it.

**Dry run:** with `--dry-run` in the argument, follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md`.

**Hands-on:** with `--hands-on` in the argument, strip the token and run the step's hands-on
execution.

Load the context (`${CLAUDE_PLUGIN_ROOT}/conventions/context.md`):
`${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh` unless this context already holds it, then
`${CLAUDE_PLUGIN_ROOT}/bin/bundle.sh demo` every run. Follow the **demo** step it prints; read
nothing from `steps/`. The step routes the stripped argument (a PR / MR, "this branch", a task
id).

**No argument**: the current branch against its base. Do not ask what to demo: name the change
in one line and demo it.

A project with no `demo` overlay is **offered one**, and the same questionnaire settles where
the report goes (step points 4 and 10). `--publish` and `--local` override that choice for one
run.
