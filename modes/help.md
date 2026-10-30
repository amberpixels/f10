# Mode · help - the skill's full argument list

A skill's `argument-hint` shows only the common call.
The `--help` token shows every argument the skill takes.

## Activation

The token **`--help`** anywhere in the argument turns help mode on.
In help mode, load nothing, run no step and write nothing.
Do not call `f10-state.sh` either.

## What to print

Print the list in a plain fenced block:

```
/f10:<skill> <the argument-hint>

<token or case>   <what it does, in the reader's terms>
...
```

Give one row to each item of the skill's **Arguments** list, in the list's order.
Each row says what the item does for the user, not how the skill does it.
For example, write "report what would run, change nothing", never "follow `modes/dry-run.md`".
Some flags are only ever sent by another session, such as `--driven`.
Mark each of them `(sent by f10 start / f10 drive)`.
Then stop.

## Skills that only run the binary

A skill that only runs the `f10` binary (start, drive, finish and board, as `modes/binary.md` describes) prints no list.
It runs `f10 <command> --help` in the foreground instead, and prints the output verbatim.
For the read skill, the command is `f10 task read --help`.
