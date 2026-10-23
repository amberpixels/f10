---
name: board
description: "f10 board skill - where every task of this project stands: done lately, in progress at which stage and who owes the next move, and what comes next, with your own next moves offered one per line. Use when the user says \"/f10:board\", asks for the board, where every task is, what is in flight, what is blocked or waiting on them, or what to pick up next."
allowed-tools: Bash
argument-hint: "[--since <date>] [--remote] [-C <checkout>]"
---

# f10 · board

The board, in a session: `f10 board` reads the host, the tracker and this machine, and draws one line per task with its stage and next action.
This skill only runs the command and relays its output; it runs no step.

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- `--json`: print the Board JSON the command emits verbatim in a fenced block, offer nothing, and stop.
- `--since <date>`: the window `done` rows cover, a `YYYY-MM-DD` date or RFC 3339 time; default seven days.
- `--remote`: the host and the tracker only, what a viewer without the checkout sees.
- `-C <checkout>`: another checkout, by path, or by name once `F10_ROOTS` is set.
- `--all`, `--group <name>`: `f10 board` does not take them yet, one checkout per call; say so in one line and stop.
- No argument: this checkout, the last seven days.

Everything goes to `f10 board` verbatim:

```
f10 board <arguments>
```

1. Print the command's output verbatim in a fenced block.
   Into the session it is plain text: a `<source>  failed  <error>` or `<source>  skipped` line per source that did not read, then one line per row under `in progress`, `next` and `done`, or the one line `nothing in flight, nothing next, nothing done since <date>` for an empty board.
   None of those is a failure; a failure is printed as is and ends the turn.
2. **Offer your moves**, one line each under the block, in the board's order.
   A row ends in a tail of up to three fields: `#<n>` for its request, who (an agent name and its status, an assignee, or the author), and its next action where it has one; `done` rows have none.
   The next action is a command (`f10 finish FT-71`, `/f10:resolve FT-71`) or a sentence (`waiting on review`, `at implement`).
   Offer a line only for a row whose tail ends in a command starting with `f10 ` or `/f10:`: the command as written, then the row's title.
   A sentence is someone else's move, and no next action is no move.
   The `next` group is folded: one line, its first row's command, with `and <N> more` when it has more rows, since the board ranks them by priority.
   Run none of them; the call is the user's.
3. Nothing else: no stage or reason the board did not print, no summary of the block.

A `flag provided but not defined` or an unknown command means a stale `f10` earlier on `PATH` shadows the plugin's: say so, quote `f10 --version`, and point at `f10 doctor`. A missing binary fails inside the plugin's `f10` with its own one-line message: relay it.
