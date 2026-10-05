# The `f10` binary

A lookaround, and the command that starts a task. The lookaround verbs explain the resolved
configuration and read the things it points at, changing nothing. Two verbs write: `init`
registers a checkout and never overwrites, and `start` creates the branch and worktree a task
is worked on in, and opens them in Herdr.

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

f10 start <id>[-suffix] [--plan | --local] [--on <id>]   # a worktree, a Herdr workspace, a prompted agent
```

A verb means one thing under every noun. `read` writes content, `open` follows an address,
`search` finds by description. The matrix stays sparse where a verb has no meaning for a noun.

## Start

`f10 start 42` is the three hand-offs of starting a task as one command. It derives the branch
name (the driver's `branch` verb, else the bare task id), creates the worktree beside the main
checkout - through `wt switch` when [worktrunk](https://github.com/max-sixty/worktrunk) is on
`PATH`, so the project's hooks keep firing, else with `git worktree add` at the same sibling
path - opens it as a Herdr workspace, starts a claude agent in its root pane and prompts it.
The command returns as soon as the prompt is submitted; the work happens in the new workspace.

- the prompt is `/f10:plan <id> && /f10:ship <id>` with every gap on its default; `--plan`
  prompts the plan alone; `--local` ships through the project's `local` pipeline, so nothing is
  pushed
- `--on <id>` bases the branch on that task's existing branch, local or on origin, and fails
  before creating anything when there is none
- a suffix glued to the id (`42-attempt2`, `42_v2`) salts the branch and the agent name, so a
  second worktree for one task can live beside the first
- a task that already has a branch gets its worktree reused, or created when the branch has
  none; a workspace Herdr already shows keeps its agent, and nothing is prompted twice
- `--local` refuses before creating anything when `project.md` declares no `local` pipeline,
  since ship never picks a pipeline unasked

It runs inside a Herdr session only. Outside one it stops before touching anything and points
at https://herdr.dev.

## References

A reference is a bare number, a `#123`, a prefixed id in any case, or a url whose last path
segment is one of those (`.../issues/42`, `.../browse/ABC-1042`). With no id, a reference
resolves in one cascade: the id you passed, else the id in the current branch name, else the
task this session recorded. The branch lookup needs the project's task id format, declared or
implied by the host.

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
