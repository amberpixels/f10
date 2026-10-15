---
name: standup
description: "f10 standup skill - a status report of what you did since your last standup, built from your commits and PRs grouped by task: a section per day, upkeep folded into one line, reviews of others' work, and a Next section from one questionnaire, drafted through the adapter your settings or the project's overlay names. Use when the user says \"/f10:standup\", asks for a standup, a status report or a daily update, or what they did yesterday or last week."
argument-hint: "[--since <date> | --week] [-C <checkout>]... [--group <name>]... [--all] [--other]"
---

# f10 · standup

No `allowed-tools` on purpose: the Deliver adapter is whatever `~/.f10/standup.md` or a project's `standup` overlay binds (an MCP tool, a skill, a command), and a fixed list would disable it.

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- `--dry-run`: follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` instead of running anything, and stop.
  It reports which steps the rest of the argument would run.
- `--driven`: follow `${CLAUDE_PLUGIN_ROOT}/modes/driven.md`; everything below still runs.
- `--since <date>`: the period starts at that date (`YYYY-MM-DD`, local midnight) or RFC 3339 time.
- `--week`: the period starts at last week's Monday, the summary for a Monday.
- `--other`: also show the activity that carries no task id.
- `-C <checkout>`, `--group <name>`, `--all`: the scope, passed to `f10 standup` as given: checkouts by path or name, a group from `~/.f10/standup.md` (whose own section there applies when it is the only group named), or every checkout under `F10_ROOTS`.
  Without them, this checkout.
- `<group>`: any other bare word is a group name, read as `--group <word>`.
- No argument: the period since the last standup you reported.

**Run**
1. Run `f10-load-project.sh standup` first, on every call of this skill.
2. Follow the **standup** step it prints.

**Exit**
- Stop at the printed report, its draft where a Deliver adapter applies, and the marked period.
