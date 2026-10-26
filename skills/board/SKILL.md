---
name: board
description: "f10 board skill - what changed in this project today (else the last working day): started, updated, reviewed, finished, new tasks; or, with --state, where every task stands and who owes the next move, with your own next moves offered one per line. Use when the user says \"/f10:board\", asks what changed, what happened yesterday or this week, for the board, where every task is, what is in flight, what is blocked or waiting on them, or what to pick up next."
allowed-tools: Bash
argument-hint: "[today | yesterday | last-workday | this-week | last-week | <date>] [--state] [--remote] [-C <checkout>]"
---

# f10 · board

The board, in a session: `f10 board` reads the host, the tracker and this machine, and draws what changed in a window (the feed), or with `--state` one line per task with its stage and next action.
This skill only runs the command and relays its output; it runs no step.

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- `--json`: print the Board JSON the command emits verbatim in a fenced block, offer nothing, and stop.
- A window, as the argument or `--since <window>`: `today`, `yesterday`, `last-workday`, `this-week`, `last-week`, a `YYYY-MM-DD` date, or an RFC 3339 time.
  The user's words map onto these ("what happened yesterday" is `yesterday`, "this week" is `this-week`).
- `--state`: where every task stands instead of what changed; a window with it sets where `done` rows start, seven days back without one.
  The user asking where tasks are, what is in flight, blocked or next means `--state`.
- `--remote`: the host and the tracker only, what a viewer without the checkout sees.
- `-C <checkout>`, `--all`, `--group <name>`: other checkouts, by path, or by name once `F10_ROOTS` is set.
- No argument: this checkout's feed for today, or the last working day while today has no activity yet.

Everything goes to `f10 board` verbatim:

```
f10 board <arguments>
```

1. Print the command's output verbatim in a fenced block.
   Into the session it is plain text.
   The feed: a header naming the span (`today · <date>`, or `last-workday · <date> (no activity today)`), a `<source>  failed  <error>` line per source that did not read, `local only` for a checkout with no origin remote, then one line per row under `started`, `updated`, `reviewed`, `finished` and `new tasks`, each ending in what happened and who owes the next move, and a closing line counting the open requests nobody touched; or `no activity in the window`.
   The state: the source lines, then one line per row under `in progress`, `next` and `done`, or `nothing in flight, nothing next, nothing done since <date>`.
   None of those is a failure; a failure is printed as is and ends the turn.
2. **The feed offers nothing**: it reports what happened. Under it, say in one line that `/f10:board --state` shows the moves.
3. **The state offers your moves**, one line each under the block, in the board's order.
   A row ends in a tail of up to three fields: `#<n>` for its request, who (an agent name and its status, an assignee, or the author), and its next action where it has one; `done` rows have none.
   The next action is a command (`f10 finish FT-71`, `/f10:resolve FT-71`) or a sentence (`waiting on review`, `at implement`).
   Offer a line only for a row whose tail ends in a command starting with `f10 ` or `/f10:`: the command as written, then the row's title.
   A sentence is someone else's move, and no next action is no move.
   The `next` group is folded: one line, its first row's command, with `and <N> more` when it has more rows, since the board ranks them by priority.
   Run none of them; the call is the user's.
4. Nothing else: no group, stage or reason the board did not print, no summary of the block.

A `flag provided but not defined` or an unknown command means a stale `f10` earlier on `PATH` shadows the plugin's: say so, quote `f10 --version`, and point at `f10 doctor`. A missing binary fails inside the plugin's `f10` with its own one-line message: relay it.
