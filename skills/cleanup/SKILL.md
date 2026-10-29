---
name: cleanup
description: "f10 cleanup skill - tighten a change before review: comments cut to the why, markdown prose cut to plain rules, development leftovers removed from code, each target proving its diff stayed in scope and verify green after. Use when the user says \"/f10:cleanup [comments|prose|code] [<path>]\", asks to clean up or tighten comments, de-slop or shave a skill or prompt, or strip dead code and debug leftovers before a commit. Bug hunting is /f10:review."
allowed-tools: Bash, Read, Edit, Grep, Glob, Agent, AskUserQuestion
argument-hint: "[comments|prose|code] [<path>...]"
---

# f10 · cleanup

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- `--dry-run`: follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` instead of running anything, and stop.
  It reports which steps the rest of the argument would run.
- `--driven`: follow `${CLAUDE_PLUGIN_ROOT}/modes/driven.md`; everything below still runs.
- `comments`, `prose`, `code`: the targets to run, one or more; none named runs the ones that apply.
- `<path>`: a path, glob or file list, cleaned in full.
- No path: the current branch's change against its base.
  Say in one line which branch, base and targets you took, and which pass the line count chose, then start.

**Run**
1. Run `f10-load-project.sh cleanup` first, on every call of this skill.
2. Follow the **cleanup** step it prints.

**Exit**
- Stop at the step's report, nothing committed.
- Next: `/f10:review`.
