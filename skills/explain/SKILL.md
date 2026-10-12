---
name: explain
description: "f10 explain skill - explain a change (what it was, what it is now) or a concept, file, package or function, to a peer who knows the project. Use when the user says \"/f10:explain <this branch | PR | task-id | concept | path | symbol>\", asks what a branch or a PR is about, asks what some term, module or function in this codebase is, or wants catching up without running anything."
allowed-tools: Bash, Read, Grep, Glob
argument-hint: "<PR | task-id | this branch | concept | symbol>"
---

# f10 · explain

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md`.
- `--dry-run`: follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md`.
- `<subject>`: a change or a concept, path or symbol; the step's **Route** paragraph routes it.
- No argument: the current branch against its base, named in one line.

Load the context (`${CLAUDE_PLUGIN_ROOT}/conventions/context.md`):
`${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh` unless this context already holds it, then
`${CLAUDE_PLUGIN_ROOT}/bin/bundle.sh explain` every run. Follow the **explain** step it prints;
read nothing from `steps/`.

Stay in the conversation afterwards, talking normally: a follow-up question is a question, not
a request to explain again. Where it leads somewhere (a doubt about the shape, a bug worth
confirming, a thing worth seeing run), offer the skill in one line (`/f10:judge`,
`/f10:review`, `/f10:demo`) and leave the call to the user.
