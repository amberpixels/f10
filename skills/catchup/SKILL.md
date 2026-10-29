---
name: catchup
description: "f10 catchup skill - bring the current branch up to date with its base (the branch `f10 start --after` recorded, else the default branch), conflicts settled keeping both sides' intent, verify run after. Use when the user says \"/f10:catchup\", asks to catch up with main, rebase or merge the base in, or is mid-merge or mid-rebase with conflicts to settle. Nothing is pushed."
allowed-tools: Bash, Read, Edit, Grep, Glob, AskUserQuestion
argument-hint: "[steering]"
---

# f10 · catchup

Running this skill authorizes the merge commit or rebased commits it produces, nothing else.

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- `--dry-run`: follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` instead of running anything, and stop.
  It reports which steps the rest of the argument would run.
- `--driven`: follow `${CLAUDE_PLUGIN_ROOT}/modes/driven.md`; everything below still runs, and the step settles every conflict itself instead of asking (its Driven paragraph).
- `<steering>`: how to settle the run ("take theirs for the lockfile", "ask me before touching the migration").
  The subject is always the branch checked out here.
- No argument: catch the branch checked out here up with its base; a merge or rebase already in progress is settled in place instead, nothing fetched, its other side the base.

**Run**
1. Run `f10-load-project.sh catchup` first, on every call of this skill.
2. Follow the **catchup** step it prints.

**Exit**
- Stop once the branch is on its base and verify is green; nothing is pushed.
- Next: `/f10:ship <id>`, when the run came from ship's failure report.
