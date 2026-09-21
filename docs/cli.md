# The `f10` binary

A read-only lookaround: it explains the resolved configuration and reads the things it points
at. One exception writes, `init`, and it never overwrites.

```bash
go install github.com/amberpixels/f10/cli/cmd/f10@latest
```

## Commands

```bash
f10 init                 # register this checkout: write project.md from detection
f10 config [--json]      # the effective configuration, with the origin of every value

f10 task read [id]       # the task as markdown
f10 task open [id]       # in the browser (--print writes the url instead)
f10 task search <query>  # rows, not a picker
f10 plan read [id]       # the plan saved for that task
f10 plan open [id]       # in your editor
f10 demo open [id]       # the demo report, in the browser
f10 pr open              # this branch's PR or MR, gh or glab decided by the remote
f10 status [--all] [--json]   # where the run is: this session's, or every live one
```

A verb means one thing under every noun. `read` writes content, `open` follows an address,
`search` finds by description. The matrix stays sparse where a verb has no meaning for a noun.

## References

With no id, a reference resolves in one cascade: the id you passed, else the id in the current
branch name, else the task this session recorded. The branch lookup needs the project's task id
format, declared or implied by the host.

`-C` answers for another checkout. A path always works. A bare name works once `F10_ROOTS`
says where to look:

```bash
f10 -C ../r3 task read 12
export F10_ROOTS=~/code/github.com/*/*
f10 -C r3 task read 12
```

## Output

`read` picks its presentation from where it writes: a pager when `$PAGER` is set and stdout
is a terminal, raw markdown into any pipe. `f10 task read | glow` works because a pipe is not
a terminal. `search` prints a table to a terminal and JSON to a pipe.

## Trackers

The host's issues need nothing declared: the git remote says GitHub or GitLab, and `gh` or
`glab` does the rest. Anything else goes through an executable at `.f10/driver`, per
[the driver contract](driver-contract.md). A driver that declines a verb falls back to the
host's issues.
