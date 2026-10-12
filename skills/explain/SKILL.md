---
name: explain
description: "f10 explain skill - explain a change (what it was, what it is now) or a concept, file, package or function, to a peer who knows the project. Use when the user says \"/f10:explain <this branch | PR | task-id | concept | path | symbol>\", asks what a branch or a PR is about, asks what some term, module or function in this codebase is, or wants catching up without running anything."
allowed-tools: Bash, Read, Grep, Glob
argument-hint: "<PR | task-id | this branch | concept | symbol>"
---

# f10 · explain

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- `--dry-run`: follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` instead of running anything, and stop.
  It reports which steps the rest of the argument would run.
- `<subject>`: a change or a concept, path or symbol; the step's **Route** paragraph reads it.
- No argument: the current branch against its base.
  Say in one line which branch and base you took, then start.

**Run**
1. Run `f10-load-project.sh explain` first, on every call of this skill.
2. Follow the **explain** step it prints.

**Exit**
- Stay in the conversation, talking normally: a follow-up question is a question, not a request to explain again.
- Next: `/f10:judge` for a doubt about the shape, `/f10:review` for a bug worth confirming, `/f10:demo` for a thing worth seeing run.
