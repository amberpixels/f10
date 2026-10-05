# The `f10` binary

A lookaround, and the commands that start and finish a task. The lookaround verbs explain the
resolved configuration and read the things it points at, changing nothing. Three verbs write:
`init` registers a checkout and never overwrites, `start` creates the branch and worktree a task
is worked on in and opens them in Herdr, and `finish` merges the task's PR and removes what
start created.

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

f10 start <id>[-suffix] [--plan | --local] [--after <id> | --base <branch>]   # a worktree, a Herdr workspace, a prompted agent
f10 finish <id>[-suffix] [--yes]   # merge the PR, pull main, archive the plan, close the workspace, remove the worktree
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
- `--after <id>` bases the branch on that task's existing branch, local or on origin, and
  records the dependency on the new branch in local git config (`branch.<name>.f10-after`, the
  task; `branch.<name>.f10-after-branch`, its branch), so it never leaves the machine. The
  prompt names the base task and the path of its plan as the contract to design against, not
  the checkout, with no fallback for its absence. With in-repo storage that path lies in the
  base task's worktree, and a note beneath the report says when no checkout reaches it or the
  plan is not written yet. A base still at the default branch's commit is noted as a task with
  no code yet. It fails before creating anything when the task has no branch
- `--base <branch>` bases the branch on a git ref instead: the local branch, else the one on
  origin (`origin/main` typed as such also resolves). It fails before creating anything when
  neither exists, and notes beneath the report when the base sits at the default branch's
  commit, since a base that changes nothing is otherwise invisible. Exclusive with `--after`
- a suffix glued to the id (`42-attempt2`, `42_v2`) salts the branch and the agent name, so a
  second worktree for one task can live beside the first
- a task that already has a branch gets its worktree reused, or created when the branch has
  none, and `--base` is noted as ignored since the branch keeps its base - `--after` likewise
  for the base, while its dependency is recorded all the same; a workspace Herdr already shows
  keeps its agent, and nothing is prompted twice
- `--local` refuses before creating anything when `project.md` declares no `local` pipeline,
  since ship never picks a pipeline unasked

A recorded dependency is read back by the ship and pr steps, not by this binary. Before the
first pipeline step, ship rebases onto the base branch when it moved, and asks once - wait, or
proceed on the plan's assumption - when it still equals the default branch. The pr step opens
the PR or MR against the base branch, so it is stacked and the host retargets it when the base
merges; a base not yet on origin stops that step rather than pushing another task's branch.

It runs inside a Herdr session only. Outside one it stops before touching anything and points
at https://herdr.dev.

## Finish

`f10 finish 42` is the inverse of start, in the order that keeps the local side honest. It
finds the task's branch the way start does (the exact name, else the one `<id>` / `<id>-*`
branch) and the worktree that has it checked out; without either it stops, since finish removes
only what start could have created.

1. **Refusals, before anything changes.** A dirty worktree, named file by file, with no
   `--force`: commit or discard first. A cwd inside the worktree being removed, or a
   `HERDR_WORKSPACE_ID` naming its workspace, unless `--yes` was passed: the message says the
   command would close its own workspace. Outside Herdr, as start does.
2. **The merge.** `gh pr view <branch>` / `glab mr view <branch>` by the branch. Open: merged
   through `gh pr merge` / `glab mr merge`, never with `--admin`; when the host refuses (red CI,
   a required review, conflicts) its reason is the error and nothing has changed. Already merged:
   confirmed. Closed without a merge: a refusal. A PR whose base is not the default branch is a
   refusal too: finish the task that base belongs to first, and the host retargets this PR.
3. **The merge method** comes from `project.md → Hosting & PR` when it carries `merge method:
   squash` (or `merge`, `rebase`, in any case). Absent, the repo's settings decide: on GitHub the
   one method allowed, else squash; on GitLab the project's squash option, since GitLab fixes the
   strategy per project.
4. **The remote branch is deleted** when origin still has it, through the host's API (glab's
   `--remove-source-branch` usually did it already). On GitHub, deleting the branch is what
   retargets a PR stacked on it to main. GitLab retargets when the base's MR is merged, so the
   same stacked flow works there by a different trigger; deleting the branch alone would not.
5. **Main is pulled** with `--ff-only` when the main checkout is on the default branch, else the
   default branch is fetched into without touching the checkout, and a note says so. Either way
   the merge commit must be in the local default branch, or the run stops here with nothing
   removed.
6. **The plan is archived**: with in-repo storage `<worktree>/.f10/plans/<ID>.md` moves to
   main's `.f10/plans/archive/<ID>.md` (`<ID>.<N>.md` when taken, older archived versions
   first); with out-of-tree storage it moves within the shared root. No plan is a note.
7. **The report**, then the Herdr workspace closes (the agent goes with it), then the worktree
   and its local branch are removed - through `wt remove` when worktrunk is on `PATH`, else
   `git worktree remove` and `git branch -D`. With `--yes` from inside the worktree, main's
   workspace is focused before the close so the user lands at home; the close ends the process,
   so a worktree still listed afterwards is removed by running `f10 finish <id>` again from main.

Run twice, finish changes nothing it already did: the PR reads as merged, the plan is already
archived, there is no workspace to close, and only what is left is removed.

Out of scope: closing the tracker issue (the PR body does that with "Closes #n"), local merges,
a task whose PR was never opened, and worktrees f10 did not start.

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
