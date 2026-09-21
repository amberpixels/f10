# The status line

A run happens where you cannot see it. Three glyphs in Claude Code's
[status line](https://code.claude.com/docs/en/statusline) say where it is: captured, planned,
shipping.

## Wiring

A plugin cannot ship a `statusLine`, so this edit to your own status-line script is yours to
make. f10 re-points `~/.claude/f10/statusline` at its current install on every session start,
so the path survives plugin updates.

```sh
input=$(cat)
f10=$(printf '%s' "$input" | "$HOME/.claude/f10/statusline" 2>/dev/null)
printf '%s@%s%s %s' "$user" "$host" "$f10" "$dir"
```

The segment brings its own leading space and is empty when no run is live, so it concatenates
unconditionally. Set `"refreshInterval": 2` on `statusLine` in `~/.claude/settings.json` as
well: without it the line re-renders only when an assistant message arrives, and a long
implement step sits on a stale glyph for minutes.

```bash
f10-state.sh doctor   # where state lives, whether the symlink is in place, what renders now
```

## Reading it

| glyph | state |
|---|---|
| `○` | pending |
| `◎` | running |
| `●` | done |
| `◉` | prior: done before this run, a saved plan reused |
| `◌` | skipped: will not happen this run |
| `󰅚` | partial: stopped, but work survived |
| `✗` | failed: stopped with nothing usable |
| `󰜺` | blocked: a judge verdict ended the run |

A stopped run adds the step it stopped on, so `◉ ● 󰜺 judge` reads "blocked at judge". The
rest, the task, the reason, what unblocks it, is `f10 status`.

The keycap, partial and blocked glyphs are Nerd Font codepoints. On an unpatched font they
show as an empty box while the circles still read. `F10_STATE_GLYPHS` replaces the set if one
lands wrong in your font.

## Environment

| variable | effect |
|---|---|
| `NO_COLOR` or `F10_STATE_COLOR=0` | drop the color, keep the badge |
| `F10_STATE_GLYPHS` | replace the glyph set |
| `F10_STATE_DIR` | where state files live, default `~/.claude/f10/state` |
| `F10_STATE_TTL` | seconds before a badge reads as stale, default one day |

State is one small file per session, outside every repo, so stealth mode keeps nothing
f10-shaped inside a project. `/clear` starts a new session and retires the badge.
