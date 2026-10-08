# The `f10` binary

A lookaround, and the commands that start, reach, drive and finish a task. The lookaround verbs
explain the loaded configuration and read the things it points at, changing nothing. Five verbs
act: `init` registers a checkout and never overwrites, `start` creates the branch and worktree a
task is worked on in and opens them in Herdr, `forward` hands a command to the agent a task
already has, `drive` runs a list of tasks through a chain of skills that way, and `finish` merges
the task's PR and removes what start created.

```bash
go install github.com/amberpixels/f10/cli/cmd/f10@latest
f10 --version
```

The binary carries the plugin's version, read from the build: each release tags the commit
`f10--vX.Y.Z` for the plugin and `cli/vX.Y.Z` for the Go module under `cli/`, so `@latest`
resolves to the release and `--version` prints its number. A build from the checkout
(`just install`) prints the commit and its date instead, tagged or not, since Go stamps a
version from tags only for a module at the repository root; a dev binary never claims to be a
release.
The skills and the binary are one contract - the skill text describes the flags the binary takes
- and a `flag provided but not defined` from `f10` means the binary is behind the plugin:
reinstall it.

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
f10 review pick [n] [--json]                          # the latest unhandled remote review, as markdown
f10 review wait [n] [--budget 10m] [--every 20s]      # poll it to done within a budget
f10 review ack [n]                                    # leave the declared handled marker on it
f10 status [id] [--all] [--json]   # where the run is: this session's, a task's in its worktree, or every live one

f10 start <id>[-suffix] [--plan | --local] [--after <id> | --base <branch>]   # a worktree, a Herdr workspace, a prompted agent
f10 forward <id>[-suffix] <text...>   # hand a command, or an answer, to the agent working on the task
f10 drive <id>... | <id> -- <id> [skill...] [--answer [<id>=]<text>] [--every 15s]   # a list through a chain of skills, each task once its base is finished
f10 finish <id>[-suffix] [--yes]   # merge the PR, pull main, archive the plan, close the workspace, remove the worktree
```

A verb means one thing under every noun. `read` writes content, `open` follows an address,
`search` finds by description. The matrix stays sparse where a verb has no meaning for a noun.

## Start

`f10 start 42` is the three hand-offs of starting a task as one command. It derives the branch
name (the driver's `branch` verb, else `<ID>/<slug>` with the slug cut from the task's title, else
the bare id with a note when the host returns no title), creates the worktree beside the main
checkout - through `wt switch` when [worktrunk](https://github.com/max-sixty/worktrunk) is on
`PATH`, so the project's hooks keep firing, else with `git worktree add` at the same sibling
path - opens it as a Herdr workspace, starts a claude agent in its root pane and prompts it.
The command returns as soon as the prompt is submitted; the work happens in the new workspace.

- the prompt is `/f10:plan <id> && /f10:ship <id>` with every gap on its default, closed by
  `--driven`, the marker that tells the agent it is prompted from outside and routes every
  question through run state ([driven mode](../modes/driven.md)); `--plan` prompts the plan
  alone; `--local` ships through the project's `local` pipeline, so nothing is pushed
- `--after <id>` bases the branch on that task's existing branch, local or on origin, and
  records the dependency on the new branch in local git config (`branch.<name>.f10-after`, the
  task; `branch.<name>.f10-after-branch`, its branch), so it never leaves the machine. The
  prompt names the base task and the path of its plan as the contract to design against, not
  the checkout, with no fallback for its absence. With in-repo storage that path lies in the
  base task's worktree, and a note beneath the report says when no checkout reaches it or the
  plan is not written yet. A base still at the default branch's commit is noted as a task with
  no code yet. It fails before creating anything when the task has no branch
- with neither `--after` nor `--base`, the task body decides: a line `After: <id>` (alone on its
  line, the last of the body by convention) is read as `--after <id>` when that task has a
  branch, and noted beneath the report. When the base has no branch and is finished (its issue
  closed, or its plan in main's `plans/archive/`), the branch is based on the default branch with
  a note; when it is not finished, start refuses before creating anything. A body that cannot be
  read is a note, never a refusal
- `--base <branch>` bases the branch on a git ref instead: the local branch, else the one on
  origin (`origin/main` typed as such is also accepted). It fails before creating anything when
  neither exists, and notes beneath the report when the base sits at the default branch's
  commit, since a base that changes nothing is otherwise invisible. Exclusive with `--after`
- the default branch is `<ID>/<slug>`: the title fetched through `gh` or `glab`, lowercased,
  non-alphanumerics collapsed to single hyphens, cut at 40 characters on a hyphen
  (`F10-8/default-task-ids-from-the-project-name`). The id stays in front, so the branch is
  still searched by its task. A project with its own shape encodes it in the driver's `branch`
  verb; no title, or a title with no ASCII letters in it, leaves the bare id and says so beneath
  the report
- a suffix glued to the id (`42-attempt2`, `42_v2`) salts the branch and the agent name, so a
  second worktree for one task can live beside the first
- the agent is named `<id>` lowercased, with the suffix, then six hex characters hashed from the
  worktree path (`gh-12-3e78d4`): Herdr's agent names are one namespace across every project,
  so the same id in two repositories never collides
- a task that already has a branch gets its worktree reused, or created when the branch has
  none, and `--base` is noted as ignored since the branch keeps its base - `--after` likewise
  for the base, while its dependency is recorded all the same; a workspace Herdr already shows
  is adopted rather than opened again, keeps its agent, and nothing is prompted twice. Where
  several workspaces show the checkout, the one with an agent is adopted, else the first, and
  each other one is named in a note with the command that closes it; none is closed. One with no agent in its root pane (a start
  that failed earlier) gets one started and prompted when that pane is an idle shell; a pane
  running anything else is left alone
- a fresh pane whose shell is still running its rc files refuses the agent as busy; start
  retries every 200ms for up to 5s before failing with Herdr's error, and prints a line to
  stderr at least every second while it waits
- `--local` refuses before creating anything when `project.md` declares no `local` pipeline,
  since ship never picks a pipeline unasked

A recorded dependency is read back by the ship, pr and catchup steps, not by this binary.
Before the first pipeline step, ship brings the branch up to date with the base when it moved -
a rebase, or a merge where `project.md` declares `catchup: merge` - and asks once - wait, or
proceed on the plan's assumption - when it still equals the default branch. A conflict there
stops ship and hands off to `/f10:catchup`, which settles it with both sides kept. The pr step opens
the PR or MR against the base branch, so it is stacked and the host retargets it when the base
merges; a base not yet on origin stops that step rather than pushing another task's branch.
`f10 finish` on the base task clears the record from every dependent, since the base's code is
in the default branch from then on.

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
   one method allowed, else squash with a note beneath the report saying the default was taken
   and how to declare one; on GitLab the project's squash option, since GitLab fixes the strategy
   per project.
4. **The remote branch is deleted** when origin still has it, through the host's API (glab's
   `--remove-source-branch` usually did it already). On GitHub, deleting the branch is what
   retargets a PR stacked on it to main. GitLab retargets when the base's MR is merged, so the
   same stacked flow works there by a different trigger; deleting the branch alone would not.
5. **Main is pulled** with `--ff-only` when the main checkout is on the default branch, else the
   default branch is fetched into without touching the checkout, and a note says so. Either way
   the merge commit must be in the local default branch, or the run stops here with nothing
   removed.
6. **Dependents are released.** A branch another task started with `--after` on this one has
   `branch.<name>.f10-after` and `.f10-after-branch` in local config. Both are cleared, since the
   base is in the default branch now and the dependent's pr targets it from here. The dependent
   branched before that code existed, so it is marked `branch.<name>.f10-landed` with the base
   task's id. Its next ship, or `/f10:catchup`, brings the default branch in before anything else
   runs and clears the mark. A note names each dependent.
7. **The plan is archived**: with in-repo storage `<worktree>/.f10/plans/<ID>.md` moves to
   main's `.f10/plans/archive/<ID>.md` (`<ID>.<N>.md` when taken, older archived versions
   first); with out-of-tree storage it moves within the shared root. No plan is a note. A plan
   that cannot be moved is an error and nothing is removed, since with in-repo storage the
   worktree holds the only copy.
8. **The report**, then the worktree and its local branch are removed - through `wt remove`
   when worktrunk is on `PATH`, else `git worktree remove` and `git branch -D` - then the Herdr
   workspace closes (the agent goes with it). With `--yes` from inside the worktree, main's
   workspace is focused before the close so the user lands at home; the close ends the process,
   which is why it comes last.

Run twice, finish changes nothing it already did: the PR reads as merged, the plan is already
archived, there is no workspace to close, and only what is left is removed.

Out of scope: closing the tracker issue (the PR body does that with "Closes #n"), local merges,
a task whose PR was never opened, and worktrees f10 did not start.

## Forward

`f10 forward 42 "/f10:ship 42"` hands a command to the agent already working on a task. The plan
and ship skills run it first whenever their argument is a task id, because a task started with
`f10 start` has its branch, its plan and its agent in a worktree of its own, and running the
command anywhere else would run it on the wrong branch without the plan.

1. **The lookup.** The task's branch, found the way start and finish find it (the exact name,
   else the one `<id>` / `<id>-*` branch); the worktree that has it checked out; the Herdr
   workspace showing that worktree; the agent under the name start gave it (below), or under
   `<id>` lowercased with the suffix alone, the name an agent started by an older f10 carries. A
   task started with a suffix is forwarded to with the same suffix.
2. **Three outcomes.** Exit 0: the command was submitted, and the report has three rows - `task`,
   `workspace`, `sent`. The command returns at submission, since Herdr's prompt call does, and the
   outcome lands in that tab; nothing here waits or tails. Exit 3: the task is in this checkout
   (its branch is checked out here, or nowhere, or it has no branch) and the command runs
   locally; a one-line reason says which. Anything else is a failure: outside Herdr, with
   start's own refusal; no workspace shows the worktree; the agent is idle and not reporting;
   Herdr refused the prompt.
3. **The marker.** Text that begins with `/f10:` and does not already carry `--driven` gets it
   appended, so the agent knows from its first turn that it is driven ([driven
   mode](../modes/driven.md)). Plain text, an answer or a steering note, is sent as it is.
4. **Idle and not reporting.** When Herdr lists the workspace's agent as `idle` while a live run
   recorded against the worktree still says `running`, the agent is waiting on something f10
   cannot see - a Claude Code permission dialog in its pane, a question asked outside driven
   mode. Forward names the agent, the workspace and the step, and sends nothing; a prompt
   submitted behind that dialog would wait with it. An idle agent on a `blocked` run is the
   driven state this verb exists for, and is sent to.

The answer path: `f10 status 42` reads the run the worktree's agent reports, its `ask` row
included; `f10 forward 42 "1. reuse 2. proceed"` sends the answers back as that agent's next
prompt. `/f10:status 42` does both in one go, with the question put to you in between.

Out of scope: more than one task or more than one command per call, tailing the remote agent's
output, and answering Claude Code permission prompts.

## Drive

`f10 drive 42 43 judge ship` runs tasks through a chain of skills from the main session. Each
task waits for its one base, every task whose base is finished runs at once, and the driver
exits when every task is through, one needs a human, or nothing else can move. The driver is this
binary rather than a Claude session: ordering, prompting and polling are deterministic, and a
session polling an agent pays a round trip and a slice of context per check.

1. **The arguments.** Task ids first, or a range `42 -- 47` (every id
   between, inclusive, at most 50). Then, optionally, the chain: any of `plan`, `judge`, `ship`,
   `review`, `resolve`, `finish`, each once, `finish` last; arrows and commas between them are
   ignored. No chain named: `project.md → Drive chain`, else
   `plan → judge → ship → review → resolve → finish`. `--answer` and `--every` go anywhere. The
   command parses its own arguments, so the range's `--` survives.
2. **Validation, before anything is created or prompted.** Outside Herdr it refuses as start
   does. Every task is looked up on the tracker. In a range, a pull request (GitHub numbers both
   alike) or an unknown number is skipped with a note; listed by hand, either is an error. A task
   with no branch whose issue is closed, or whose plan sits in main's `plans/archive/`, is
   finished and skipped.
3. **The graph.** Each task has at most one base: the `branch.<name>.f10-after` its branch
   recorded once started, else the `After:` line in its body. Where the two disagree, the
   branch wins, with a note. The map is rebuilt on every run, and nothing about it is stored. A
   base outside the list that is not finished is refused, and so are tasks that wait on each
   other in a circle, both before anything is created. Within the list the graph sets the
   order: a base runs first wherever it is listed, and among tasks ready at once the one listed
   first goes first.
4. **Per task.** A task starts once its base is through: finished, or its whole chain run, where
   the chain has no `finish`. Every task that is ready starts in the same pass, since each agent
   is its own session. The worktree, workspace and agent are opened as `f10 start` opens them,
   with the body's `After:` as the base: a base still on its branch stacks the new one on it,
   and a finished base leaves it on the default branch. A checkout Herdr already shows, as an
   earlier `f10 start` leaves it, is adopted: its workspace, its agent, or a new agent in its idle
   shell. Run state recorded before the driver started that new agent was written by one that is
   gone, and halts nothing. From then on the task's workspace is the one opened, by id, whatever
   else shows the same checkout. Nothing is prompted at open. The chain runs from the task's
   position: the last skill the driver saw finish, recorded on the branch as
   `branch.<name>.f10-drive` in local git config, else what the task's run state proves (plan
   done, ship done). `finish` runs in the driver, exactly as `f10 finish` would from the main
   checkout. Every other skill is prompted to the agent, marked `--driven`: plan and ship take
   every gap on its default as start's prompt does, ship reuses the saved plan, a dependent task's
   plan and ship carry the dependency's contract, and review gets `ci` where `project.md` declares
   a remote review.
5. **Catchup after finish.** `finish` marks each branch that depended on the finished task
   `f10-landed`. Before a marked task's next skill, the driver sends it `/f10:catchup --driven`.
   The chain resumes only on a clean end: the mark cleared (catchup clears it once verify is
   green), no merge or rebase in progress, and a clean tree. A conflict the catchup cannot
   settle is an ask; anything else halts the task.
6. **Waiting.** Every `--every` (15s by default) it reads two signals once for every task in
   flight: Herdr's workspace list, and the live runs, the newest one recorded against each
   worktree. A turn has begun once Herdr
   shows the agent working or the run reports after the prompt; Herdr's `done` lasts from the
   previous turn until someone looks, so it proves nothing. No sign within two minutes halts the
   task, and so does a workspace that still shows no agent status by then. Once the agent is idle again, the run says how the turn ended:
   - **blocked with an ask** - the question is printed with its options. At the end of that
     poll the driver prints every task's ask and the command that answers them all, and exits
     **4**. The other agents keep working in their own panes;
   - **blocked without one** (a judge stop, a wait on a base with no code), **failed** or
     **partial** - the task halts with the run's note and next, and so does every task that
     waits on it. Every other task runs on, and the driver exits **5** once nothing can move;
   - **still running** under plan or ship, a minute after the agent went idle - the agent
     stopped without reporting, usually a permission dialog in its pane; the task halts naming
     the workspace. Found at prompt time on an agent the driver did not start, the same halt
     also says how to leave a cancelled run: quit that agent and rerun, and a new one is
     started in its place;
   - anything else - the skill is done, recorded on the branch, and the next one is sent.
7. **The report.** Progress lines as it goes, an end included: a halt or an ask is printed the
   moment the driver sees it, not only beneath the report. Then one row per task in graph order:
   `already finished`, `finished`, `done: <chain>`, `running <skill>` (still working when the
   driver exited on an ask), `asked at <skill>`, `halted at <skill>`,
   `halted: base <id> halted`, `not reached`. Exit 0 when every task went through its chain, 4
   when any task asks, else 5 when any halted.

Rerunning the same command is the resume: each task's position is read from its own branch and
run state. With `--answer "1. … 2. …"`, the first task stopped on a question gets the text as its
agent's next prompt, plain, the way `f10 forward` sends an answer, and the driver polls that turn
as it would any other. Several asking tasks take one answer each, keyed by id:
`--answer "42=1. proceed" --answer "45=1. no"`, each sent to its own task's agent. An agent found mid-turn is attached to
rather than prompted again. A task halted for another reason stays halted until its run changes:
settle it in that workspace, or send steering through `--answer`.

`/f10:drive` runs the command in the background, puts an exit-4 question to you in one
questionnaire, reruns the command with the answers, and prints the final report.

Out of scope: more than one base per task, and ordering tasks by anything but their recorded
dependencies.

## Review

`f10 review` reads the remote review a project declares under `project.md → Review` - four
facts: who posts it, where it arrives on the PR or MR, what triggers it, what signals done and
marks it handled ([project setup](project-setup.md#review)) - off the current branch's request,
or the one `[n]` names. Nothing declared, or a fact outside the vocabulary, is a refusal naming
the line to fix; the trigger is never performed, and `pick` says `the trigger is manual (<how>)`
when it finds nothing on a manual project.

- `pick` writes the newest review by the reviewer that nobody handled: a title, a meta line (url,
  the sha it reviewed, posted at, done, handled), the body verbatim. `--json` emits the struct.
  An issue comment stores no sha, so its sha is the request's last commit before it was posted;
  an inline review carries its own, a check run its head.
- `wait` polls `pick` every `--every` until the review is done, and gives up at `--budget`
  (10 minutes by default) with the last reason quoted. It returns within the budget whatever the
  host does, so one agent call covers it.
- `ack` leaves the declared handled marker: a reaction, a marker appended to the text, the first
  checkbox ticked, or the inline review's threads resolved. It is the one verb here that writes
  to the host. A review already handled is confirmed, not marked twice; a check run has no
  marker and ack says so. A reaction counts as handled only when it is newer than the review's
  last edit, so a sticky comment re-edited by a re-review reads as unhandled again.

Both CLIs are spoken: gh's issue comments, pull reviews and check runs; glab's notes,
discussions and pipeline jobs.

## References

A reference is a bare number, a `#123`, a prefixed id in any case, or a url whose last path
segment is one of those (`.../issues/42`, `.../browse/ABC-1042`). With no id, a reference
is looked up in one cascade: the id you passed, else the id in the current branch name, else the
task this session recorded. The branch lookup needs the project's task id format, declared in
`project.md` or derived from the project's name.

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

Task ids take the prefix `project.md` declares, else one derived from the main checkout's name:
a short name with a digit as is (`f10` → `F10`, `r3` → `R3`), initials across hyphens,
underscores or dots (`git-undo` → `GU`, `notion-sdk-go` → `NSG`), the first three characters
of one word (`herdr` → `HER`). The prefix names plan files and is how a branch is searched for
its task, so it must not move once used: `f10 init` writes it into `project.md`, and `f10
config` shows which it is and where it came from.
