# Mode · binary - a skill that only runs the f10 binary

Some skills do all their work through one `f10` command: asks, board, drive, finish, read, start and status.
The procedure of each is to run that command and relay its output.
Such a skill runs no step and loads no conventions.
Its `--help` runs the command's own help (`modes/help.md`).

## Relaying the output

- A line that starts with `!!!` goes first, verbatim, above everything else the command printed.
- A refusal is final, unless the skill names it as a question to put to the user.
  Print the message verbatim and stop, because the message already names the cause and what to do about it.
- The error `flag provided but not defined`, or an unknown command, means an older `f10` earlier on `PATH` shadows the plugin's own.
  Say so, quote the output of `f10 --version`, and point the user at `f10 doctor`.
- When the binary is missing, the plugin's `f10` fails with a one-line message of its own.
  Relay that message.
