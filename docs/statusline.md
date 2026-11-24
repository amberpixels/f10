# The status line

A run happens where you cannot see it.
Three glyphs in Claude Code's [status line](https://code.claude.com/docs/en/statusline) say where it is: captured, planned, shipping.

## Wiring

Claude Code applies only `agent` and `subagentStatusLine` from a plugin's settings, so a plugin cannot ship a `statusLine`.
The edit below to your own status-line script is yours to make.
f10 re-points `~/.claude/f10/statusline` at its current install on every session start, so the path survives plugin updates.

```sh
input=$(cat)
f10=$(printf '%s' "$input" | "$HOME/.claude/f10/statusline" 2>/dev/null)
printf '%s@%s%s %s' "$user" "$host" "$f10" "$dir"
```

The segment brings its own leading space and is empty when no run is live, so it concatenates unconditionally.
Set `"refreshInterval": 2` on `statusLine` in `~/.claude/settings.json` as well: without it the line re-renders only when an assistant message arrives, and a long implement step sits on a stale glyph for minutes.

```bash
f10 doctor            # is the statusLine wired and the symlink in place, beside the rest of the install
f10-state.sh doctor   # where state lives, whether the symlink is in place, what renders now
```

## Reading it

| glyph | state |
|---|---|
| `○` | upcoming |
| `◎` | running |
| `●` | done |
| `◉` | prior: done before this run, a saved plan reused |
| `◌` | skipped: will not happen this run |
| <img src="glyphs/partial.svg" width="14" alt="partial"> | partial: stopped, but work survived (committed but the push failed, a PR open with red CI) |
| `✗` | failed: stopped with nothing usable |
| <img src="glyphs/blocked.svg" width="14" alt="blocked"> | blocked: a judge verdict ended the run, so nothing broke and the user decided |

A stopped run adds the step it stopped on, so `◉ ●` <img src="glyphs/blocked.svg" width="14" alt="blocked"> `judge` reads "blocked at judge".
Open asks of this session's project add the first asking task's id, in file name order, and a count of the rest: ` f10 · 󰚇 FT-12` for one, ` f10 · 󰚇 FT-12 +2` for three, shown with or without a run.
The id is a link to its ask's file and `+2` a link to the inbox directory, so Ctrl+click opens the question, or a picker over the open asks, in a terminal that supports OSC 8 links; any other shows plain text.
The count means a driven agent is waiting on you (`f10 inbox`, `/f10:inbox`).
Its icon is md-inbox (U+F0687).
The session start hook writes the project to `<session>.project` beside the run state, as the hash every ask's file name starts with.
A session with no such record, such as one outside git, counts every project's asks.
The rest, the task, the reason, what unblocks it, and the question a driven agent is waiting on (`ask`), is `f10 status`; `f10 status <task-id>` reads the same for a task running in its own worktree, from the task's own run only; when the agent there has moved on to another task, it names that task and falls back to the task's plan file and PR.
For a finished task, its worktree gone, it prints the same block from what `f10 finish` archived.
Bare `f10 status` in a checkout whose branch names no task prints a `launched here` tree, a row per live task this session started or drove.
Otherwise it is `f10 status <task-id>` for the task this session's run records, else the one the branch or worktree name carries; with neither, it prints the session's run, or this repo's live runs.

Each state has its own shape, and color only reinforces it, because red and green look the same in a daltonized theme.
Done is green, and running is bright cyan.
Partial is yellow, because the work survived.
Blocked is magenta, because nothing broke.

### Why circles, and the three Nerd Font glyphs

When your font lacks a codepoint, the terminal takes the glyph from another font with its own baseline, and the badge renders off the line.
So the other glyphs are shapes that JetBrains Mono, Fira Code and Hack all carry.
`◐` is missing from all three, so it is not used.
If a glyph still lands wrong, `F10_STATE_GLYPHS` replaces the set.

Three glyphs are Material Design icons that only [Nerd Fonts](https://www.nerdfonts.com) carry:

- The label is `md-keyboard_f10` (U+F12B4), because it draws a whole keycap in one cell.
- Partial is `md-close-circle-outline` (U+F015A), because no crossed circle exists in all of those fonts: `⊗` is absent from Fira Code.
- Blocked is `md-cancel` (U+F073A), because `⊘` is missing even from the Nerd Fonts.

In any other font, each of the three shows as one empty box, and the circles still read.
GitHub shows them as empty boxes too.
So the docs draw partial and blocked as images, and the README draws the keycap in `statusline.svg`.
To copy the real characters, take them from the `F10_STATE_GLYPHS` default below.

## Environment

| variable | effect |
|---|---|
| `NO_COLOR` or `F10_STATE_COLOR=0` | drop the color, keep the badge |
| `F10_STATE_GLYPHS` | replace the glyph set: all eight positions, in the order upcoming, running, done, skipped, failed, prior, partial, blocked; default `○ ◎ ● ◌ ✗ ◉ 󰅚 󰜺`, whose last two are the real U+F015A and U+F073A characters |
| `F10_STATE_LINK` | `0` drops the hyperlinks on the asks' task id and count |
| `F10_STATUS_GLYPHS` | replace the four icons in `f10 status` trees: open PR, merged PR, review and judge ([cli](cli.md#status)) |
| `F10_STATE_DIR` | where state files live, default `~/.claude/f10/state` |
| `F10_STATE_TTL` | seconds before a badge reads as stale, default one day |
| `F10_INBOX_DIR` | where open asks are counted from, default `~/.claude/f10/inbox` |

State is one small file per session, outside every repo, so stealth mode keeps nothing f10-shaped inside a project.
`/clear` starts a new session and retires the badge.

## `/f10:status` and driven agents

`/f10:status` and `/f10:status <id>` are answered by the plugin's hook before a model runs, so they cost no tokens.
Three cases go to the model instead:

- a status that prints a question a driven agent waits on
- an argument that names no task
- a task whose read takes longer than 3 seconds

Inside a session, `! f10 status` already has the session id.

An agent in another worktree runs **driven** ([modes/driven.md](../modes/driven.md)).
Nobody watches its pane, so it writes its questions to run state as one `ask` row and stops blocked.
`/f10:status <id>` puts those questions to you, and writes your answers through `f10 inbox answer`, which sends them to that agent.

## How the badge stays current

Two writers keep it current.
Steps report precisely, and hooks report reliably.

- **Hooks** (`hooks/hooks.json`): every state hook runs `f10 hook`, which reads the payload once.
  - `UserPromptExpansion` and `PreToolUse` catch a skill starting, whether you typed it or the model invoked it.
    They read its argument to tell a description, which is routed through capture, from a task id.
  - `UserPromptExpansion` also answers `/f10:status [id]`: it writes a blocking verdict on stdout, which ends the turn.
  - The same two events, and `SessionStart` after a compaction, inject the conventions through `f10-inject.sh`.
    They send them in chunks under Claude Code's per-hook cap, once per session and again after a compaction dropped them.
    A per-session marker records whether the session holds them.
  - `PostToolUse` turns what a run produces into typed observations: an issue or PR created, a commit, a push, a step loaded, `f10 finish`, a plan or findings file written.
    A plan file written sets "plan done" and the task id.
    A ship step loaded sets that step running.
    The issue capture creates names the task.
  - `PreToolUse` on `Bash`, `Monitor` and `Agent` runs async and records background work a turn launches.
    The turn's `Stop` keeps the record.
    A drive reads an agent whose turn ended on that work as working, not stalled, until a later turn clears the record.
  - `Stop` closes out whatever is still running, such as a run whose last badge call the agent dropped.
  - `SessionStart` prunes dead state and refreshes the symlink.
- **Steps**: each step makes one `f10-state.sh set` call as each phase opens and closes.
  The calls are best-effort, as `conventions/report.md` says.
  The script forwards each call to `f10 state`, and with no binary installed yet, the call does nothing.

A phase left `upcoming` means that nobody reported on it.

Both writers also append to a local [ledger](ledger.md), `~/.f10/f10.db`, a SQLite file outside every repo.
It records what the hooks observed, what the artifacts hold, every badge call as the model claimed it, and every halt and finish a drive made itself.
Each row is tagged with its source.
A write that fails goes to `~/.f10/ledger.spool`, and the next write replays it.
`f10 ledger dump` prints the rows.

## Task ids as links

Claude Code's [`footerLinksRegexes`](https://code.claude.com/docs/en/settings#footer-link-badges) setting is not part of f10, but it pairs well with it.
It turns a task id in a reply into a clickable badge in the footer, with one regex per tracker and no script.
