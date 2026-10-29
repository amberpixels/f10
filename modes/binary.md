# Mode · binary - a skill that only runs the f10 binary

**Activation:** a skill whose procedure is one `f10` command and its output (asks, board, drive, finish, read, start, status).
It runs no step and loads no conventions; `--help` runs the command's own (`modes/help.md`).

- A line starting `!!!` goes first, verbatim, above everything else the command printed.
- A refusal the skill does not name as a question is final: print the message verbatim and stop; it names the cause and what to do.
- `flag provided but not defined` or an unknown command means a stale `f10` earlier on `PATH` shadows the plugin's: say so, quote `f10 --version`, and point at `f10 doctor`.
- A missing binary fails inside the plugin's `f10` with its own one-line message: relay it.
