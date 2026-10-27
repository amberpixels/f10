# Mode · help - the skill's full argument list

The `argument-hint` shows the common call only; `--help` shows the rest.

**Activation:** the token **`--help`** anywhere in the argument.
Load nothing, run no step, write nothing, call no `f10-state.sh`.

Print, in a plain fenced block:

```
/f10:<skill> <the argument-hint>

<token or case>   <what it does, in the reader's terms>
...
```

One row per item of the skill's **Arguments** list, in its order.
Say the effect, not the mechanism: "report what would run, change nothing", never "follow `modes/dry-run.md`".
Mark a flag only another session sends (`--driven`) as `(sent by f10 start / f10 drive)`.
Then stop.

A skill that only runs the `f10` binary (start, drive, finish, board) runs `f10 <command> --help` in the foreground instead and prints its output verbatim; read runs `f10 task read --help`.
