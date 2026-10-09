---
name: explain
description: "f10 explain skill - explain a change (what it was, what it is now) or a concept, file, package or function, to a peer who knows the project. Use when the user says \"/f10:explain <this branch | PR | task-id | concept | path | symbol>\", asks what a branch or a PR is about, asks what some term, module or function in this codebase is, or wants catching up without running anything."
allowed-tools: Bash, Read, Grep, Glob
argument-hint: "<this branch | PR | task-id | commit or range | local | concept | path | function>"
---

# f10 · explain

Explain one thing to someone who knows the project but not this thing. A change: what it
**was** and what it **is** now. A concept, a file, a package, a function: what it **is**, in a
few sentences from the code. **Creates nothing**: no file, no tracker comment, no code, no PR
comment. It reaches no verdict (`/f10:judge`), hunts no bugs (`/f10:review`) and runs nothing
(`/f10:demo`).

**Dry run:** with `--dry-run` in the argument, follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md`.

Load the context (`${CLAUDE_PLUGIN_ROOT}/conventions/context.md`):
`${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh` unless this context already holds it, then
`${CLAUDE_PLUGIN_ROOT}/bin/bundle.sh explain` every run. Follow the **explain** step it prints,
routing by its **Route** paragraph; read nothing from `steps/`.

**No argument**: the current branch against its base. Do not ask what to explain: name the
subject in one line and explain it.

Stay in the conversation afterwards, talking normally: a follow-up question is a question, not
a request to explain again. Where it leads somewhere (a doubt about the shape, a bug worth
confirming, a thing worth seeing run), offer the skill in one line (`/f10:judge`,
`/f10:review`, `/f10:demo`) and leave the call to the user.
