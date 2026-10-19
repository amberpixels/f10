# The `f10` binary

A lookaround, and the commands that start, reach, drive and finish a task.
The lookaround verbs explain the loaded configuration and read the things it points at, changing nothing.
Five verbs act: `init` registers a checkout and never overwrites, `start` creates the branch and worktree a task is worked on in and opens them in Herdr, `forward` hands a command to the agent a task already has, `drive` runs a list of tasks through a chain of skills that way, and `finish` merges the task's PR and removes what start created.

The plugin requires the binary and ships it as `bin/f10`, a shim that runs the build matching `plugin.json`'s version.
The skills and the binary are one contract: the skill text describes the flags the binary takes.
The shim resolves the binary in this order, and caches it as `~/.claude/f10/bin/f10-<version>`:

1. **Cache:** that file exists, so it runs.
2. **Download:** `gh release download cli/v<version>` from this repo, the asset for this OS and CPU (darwin or linux, amd64 or arm64), checked against the release's `checksums.txt`. `gh` handles auth for the private repo. A checksum mismatch fails; it never falls through to a build.
3. **Build:** the download failed and Go is installed, so `go build` of the plugin's `cli/` with the version stamped in.
4. **Neither:** one line on stderr saying what is missing and how to fix it (`gh auth login`, or install Go), exit 1.

A SessionStart hook runs the resolution in the background, so the first real call rarely waits, and shows the same line when neither `gh` nor Go is there.
It also puts the plugin's `bin/` first on the session's PATH (through `CLAUDE_ENV_FILE`), so an older `f10` in `~/go/bin` cannot shadow the shim.
A checkout build is marked in the cache, and the hook rebuilds it once `cli/` holds newer sources.

```bash
f10 --version   # always the plugin's version through the shim
just install    # dev: rebuild this checkout's cli/ into the cache slot (bin/f10 --shim-rebuild)
```

`--version` prints the version stamped at build time (`-X main.stampedVersion`), as the shim and the release workflow build it.
An unstamped build prints the module version for `go install ...@vX.Y.Z`, or the commit and its date for a plain checkout build.
Pushing a `cli/vX.Y.Z` tag runs `.github/workflows/release.yml`, which attaches the four binaries and `checksums.txt` to a GitHub Release; the release convention pushes that tag beside `f10--vX.Y.Z`.
A `flag provided but not defined` from `f10` means some other `f10` ran in place of the shim: `f10 doctor` names it.

## Commands

```bash
f10 init                 # register this checkout: write project.md from detection
f10 init --migrate       # move the facts an older project.md declares in prose into its frontmatter
f10 config [--json]      # the effective configuration, with the origin of every value
f10 doctor [--json]      # check the plugin, the binary and the environment; changes nothing

f10 task read [id]       # the task as markdown
f10 task open [id]       # in the browser (--print writes the url instead)
f10 task search <query>  # rows, not a picker
f10 task children [id] [--json]   # the task's open subtasks, in After: order
f10 plan read [id]       # the plan saved for that task
f10 plan open [id]       # in your editor
f10 demo open [id]       # the demo report, in the browser
f10 pr open              # this branch's PR or MR, gh or glab decided by the remote
f10 review pick [n] [--json]                          # the latest unhandled remote review, as markdown
f10 review wait [n] [--budget 10m] [--every 20s]      # poll it to done within a budget
f10 review ack [n]                                    # leave the declared handled marker on it
f10 status [id] [--all] [--json]   # where the run is: this session's, a task's in its worktree, or every live one
f10 standup [--since <time> | --week] [-C <checkout>]... [--group <name>]... [--all] [--json]   # your commits and PRs since your last standup, grouped by project and task
f10 standup mark --since <time> [--at <time>]      # record a standup as reported: the next one starts where it ended

f10 start <id>[-suffix] [--plan | --local | --discuss] [--after <id> | --base <branch>] [--model <model>]   # a worktree, a Herdr workspace, a prompted agent
f10 forward <id>[-suffix] <text...>   # hand a command, or an answer, to the agent working on the task
f10 drive <id>[:model]... | <id> -- <id> [skill...] [--model <model>] [--answer [<id>=]<text>] [--every 15s]   # a list through a chain of skills, each task once its base is finished
f10 finish <id>[-suffix] [--yes]   # merge the PR, pull main, archive the plan, close the workspace, remove the worktree
```

A verb means one thing under every noun.
`read` writes content, `open` follows an address, `search` finds by description.
The matrix stays sparse where a verb has no meaning for a noun.

## Doctor

`f10 doctor` runs every check below and prints one row per check: `ok`, `problem` or `not needed here`, with the evidence.
Under each problem an `→` line names the remedy, a command to run or a file to edit.
It changes nothing, so it is always safe to run; `-C` points it at another checkout.

| check | what it reads | a problem's remedy |
|---|---|---|
| `version` | the plugin's `plugin.json` version against `f10 --version`; an unstamped dev build is not compared | run the plugin's `bin/f10` in place of the `f10` earlier on PATH, or `claude plugin update f10@<marketplace>` when the plugin is the one behind |
| `scripts` | every file in the plugin's `bin/`, the `f10` shim included: executable, and inside a Claude Code session resolved from that `bin/` on PATH | `claude plugin update`, or restart the session; for a shadowed `f10`, remove the copy PATH reaches first |
| `hooks` | the plugin enabled in `settings.json` (a plugin loaded on the session's PATH counts as enabled) and its `hooks/hooks.json`; "registered but needs a restart" when the install changed after this session started (the `<session>.started` marker the SessionStart hook writes), or, outside a session, after the last session start anywhere | `claude plugin enable`, or restart Claude Code |
| `statusline` | `statusLine` in `settings.json` runs `~/.claude/f10/statusline`, and the link resolves; scoped to the status-line badge | add the segment from [the status line](statusline.md), or start a session to relink it |
| `herdr` | `herdr` on PATH; scoped to `start`, `forward`, `drive` and `finish` | install Herdr |
| `host` | the `gh` or `glab` the remote routes to, installed and `auth status` green for the host; `not needed here` without a remote | `<cli> auth login --hostname <host>` |
| `project` | `project.md` frontmatter: no unknown keys or values; `not needed here` outside a project | edit the named `project.md`; `f10 config` shows every fact |

Inside a session the plugin judged is the one on the session's PATH (a directory marketplace or `--plugin-dir` included); outside one, the install `installed_plugins.json` records.

It exits 1 when any check that every command needs has a problem.
A scoped problem (`herdr`, `statusline`) is printed but affects only what it names, and `not needed here` never fails.

`--json` emits an array, one object per check:

| field | meaning |
|---|---|
| `name` | the check, as in the table |
| `status` | `ok`, `problem` or `not_needed` |
| `scope` | what a problem affects, commands or the status-line badge; `[]` means every command |
| `detail` | the evidence, one line |
| `remedy` | the command or file edit; empty unless `status` is `problem` |

## Start

`f10 start 42` is the three hand-offs of starting a task as one command.
It derives the branch name (the driver's `branch` verb, else `<ID>/<slug>` with the slug cut from the task's title, else the bare id with a note when the host returns no title), creates the worktree beside the main checkout - through `wt switch` when [worktrunk](https://github.com/max-sixty/worktrunk) is on `PATH`, so the project's hooks keep firing, else with `git worktree add` at the same sibling path - opens it as a Herdr workspace, starts a claude agent in its root pane and prompts it.
The command returns as soon as the prompt is submitted; the work happens in the new workspace.

- the prompt is `/f10:plan <id> && /f10:ship <id>` with every gap on its default, closed by `--driven`, the marker that tells the agent it is prompted from outside and routes every question through run state ([driven mode](../modes/driven.md)); `--plan` prompts the plan alone; `--local` ships through the project's `local` pipeline, so nothing is pushed; `--discuss` prompts only the fetch: the agent summarises the task and waits for you in its own pane, so the prompt is plain text with no `--driven` marker.
  The three are exclusive
- a plan the main checkout saved for the task (`.f10/plans/<id>.md`, written by `/f10:plan` after a discussion there) is moved, not copied, into the new worktree's `.f10/plans/`, so one plan exists and a later `/f10:ship <id>` in main finds none.
  The agent ships it: the prompt is `/f10:ship <id>`, or `/f10:ship local: <id>` with `--local`; `--plan` still plans, and the plan step archives the moved one; `--discuss` names the plan's path.
  The report gains a `plan` row and a note beneath saying it was moved from main.
  A worktree that already holds its own plan (start rerun on an existing worktree) keeps it and ships it, and main's is left in place with a note.
  Out-of-tree storage shares one plans dir, so nothing moves and only the prompt changes.
  The plan moves only when start starts the agent: a workspace whose agent is already running gets no prompt, so main's plan stays in place with a note.
  A move that fails stops start before the agent is started, and an agent that fails to start after the move names where the plan went.
  `f10 drive` adopts main's plan the same way, when it starts the task's agent.
- `--after <id>` bases the branch on that task's existing branch, local or on origin, and records the dependency on the new branch in local git config (`branch.<name>.f10-after`, the task; `branch.<name>.f10-after-branch`, its branch), so it never leaves the machine.
  The prompt names the base task and the path of its plan as the contract to design against, not the checkout, with no fallback for its absence.
  With in-repo storage that path lies in the base task's worktree, and a note beneath the report says when no checkout reaches it or the plan is not written yet.
  A base still at the default branch's commit is noted as a task with no code yet.
  It fails before creating anything when the task has no branch
- with neither `--after` nor `--base`, the task body decides: a line `After: <id>` (alone on its line, the last of the body by convention) is read as `--after <id>` when that task has a branch, and noted beneath the report.
  When the base has no branch and is finished (its issue closed, or its plan in main's `plans/archive/`), the branch is based on the default branch with a note; when it is not finished, start refuses before creating anything.
  A body that cannot be read is a note, never a refusal
- `--base <branch>` bases the branch on a git ref instead: the local branch, else the one on origin (`origin/main` typed as such is also accepted).
  It fails before creating anything when neither exists, and notes beneath the report when the base sits at the default branch's commit, since a base that changes nothing is otherwise invisible.
  Exclusive with `--after`
- `--model <model>` starts the agent on that model, passed to claude as `--model`.
  A family (`opus`, `fable`) passes as the alias; a family with a version is rebuilt into the full id (`opus5-5`, `Opus 5.5` and `opus-5.5` all become `claude-opus-5-5`); a full `claude-` id passes as typed.
  A family misspelt by up to two letters is corrected (`fabel` to `fable`) with a note.
  A name that is no family falls back to claude's own default (the `model` in `~/.claude/settings.json`) and the first line beneath the report is a `!!!` warning naming it.
  Only families are checked, so a real family with a version that does not exist reaches claude, which refuses it at the agent's first prompt.
  A workspace whose agent is already running keeps it, and the model is noted as ignored
- the default branch is `<ID>/<slug>`: the title fetched through `gh` or `glab`, lowercased, non-alphanumerics collapsed to single hyphens, cut at 40 characters on a hyphen (`F10-8/default-task-ids-from-the-project-name`).
  The id stays in front, so the branch is still searched by its task.
  A project with its own shape encodes it in the driver's `branch` verb; no title, or a title with no ASCII letters in it, leaves the bare id and says so beneath the report
- a suffix glued to the id (`42-attempt2`, `42_v2`) salts the branch and the agent name, so a second worktree for one task can live beside the first
- the agent is named `<id>` lowercased, with the suffix, then six hex characters hashed from the worktree path (`gh-12-3e78d4`): Herdr's agent names are one namespace across every project, so the same id in two repositories never collides
- `f10 start <PR url | #n>` picks up a PR someone else opened: the task is in the tracker, the branch and the PR are on the remote, and nothing is local.
  A url with `/pull/<n>` or `/merge_requests/<n>`, or GitLab's `!<n>`, names a PR; on GitHub, whose issues and PRs share one counter, a bare `#<n>` is asked of the host as a PR first, and one the host knows as no PR is read as a task id, as before; on GitLab `#<n>` stays a task id.
  Any other host error on that lookup is the error.
  Start reads the PR through `gh` or `glab`, runs `git fetch origin`, creates the local branch tracking origin's (`git branch --track`), and puts the worktree on it.
  A local branch already there is fast-forwarded to origin's when it is behind and checked out nowhere; behind in a worktree, or diverged, is a note.
  The task id comes from `--task <id>`, else a closing keyword in the PR body (`Closes #12`, or an issue url of this repo) where the tracker is the host's issues, else an id in the project's format in the body, the title or the branch name.
  None found: start exits **3** having changed nothing, and `/f10:start` asks for the id and reruns with `--task`.
  The branch is linked to its task in local config, `branch.<name>.f10-task=<id>`, beside `f10-after`.
  The agent is prompted plain text, with no `--driven` marker: it reads the task, `gh pr view <n> --comments` and `gh pr diff <n>`, summarises the task, the PR's state and its diff, and waits.
  `--plan` prompts the plan instead, with the PR's diff as the starting point; nothing ships a picked-up PR unread.
  The report gains a `pr` row.
  A PR from a fork, a PR no longer open, `--local`, `--after` and `--base` are refused before anything is created
- every lookup of a task's branch (start, forward, status, finish, drive, `--after`) reads the `f10-task` link first and falls back to the name patterns (`<id>`, `<id>/...`, `<id>-...`); a task read with no id in a picked-up worktree finds its task through the same link.
  With no local branch, start also finds the one task branch on origin (`refs/remotes/origin/`) and creates the local branch tracking it rather than a second branch from main.
  It reads what the last fetch saw; for a branch pushed since, run `git fetch` or pass the PR link
- a task that already has a branch gets its worktree reused, or created when the branch has none, and `--base` is noted as ignored since the branch keeps its base - `--after` likewise for the base, while its dependency is recorded all the same; a workspace Herdr already shows is adopted rather than opened again, keeps its agent, and nothing is prompted twice.
  Where several workspaces show the checkout, the one with an agent is adopted, else the first, and each other one is named in a note with the command that closes it; none is closed.
  One with no agent in its root pane (a start that failed earlier) gets one started and prompted when that pane is an idle shell; a pane running anything else is left alone
- a fresh pane whose shell is still running its rc files refuses the agent as busy; start retries every 200ms for up to 5s before failing with Herdr's error, and prints a line to stderr at least every second while it waits
- `--local` refuses before creating anything when `project.md` declares no `local` pipeline, since ship never picks a pipeline unasked

A recorded dependency is read back by the ship, pr and catchup steps, not by this binary.
Before the first pipeline step, ship brings the branch up to date with the base when it moved - a rebase, or a merge where `project.md` declares `catchup: merge` - and asks once - wait, or proceed on the plan's assumption - when it still equals the default branch.
A conflict there stops ship and hands off to `/f10:catchup`, which settles it with both sides kept.
The pr step opens the PR or MR against the base branch, so it is stacked and the host retargets it when the base merges; a base not yet on origin stops that step rather than pushing another task's branch.
`f10 finish` on the base task clears the record from every dependent, since the base's code is in the default branch from then on.

It runs inside a Herdr session only.
Outside one it stops before touching anything and points at https://herdr.dev.

## Finish

`f10 finish 42` is the inverse of start, in the order that keeps the local side honest.
It finds the task's branch the way start does (the branch linked by `f10-task`, else the exact name, else the one `<id>` / `<id>-*` branch) and the checkout that has it checked out; without either it stops, since finish removes only what start could have created.
That checkout is a worktree start created, or the main checkout itself for a task shipped from main with no worktree.
There finish merges as usual, brings the default branch up to the merge, switches main to it, deletes the local branch, and leaves main's workspace open; the report says the checkout was main's and was switched, not removed.

1. **Refusals, before anything changes.**
   A dirty worktree, named file by file, with no `--force`: commit or discard first.
   A cwd inside the worktree being removed, or a `HERDR_WORKSPACE_ID` naming its workspace, unless `--yes` was passed: the message says the command would close its own workspace.
   With the branch checked out in main, nothing closes, so this refusal does not apply.
   Outside Herdr, as start does.
2. **The merge.**
   `gh pr view <branch>` / `glab mr view <branch>` by the branch.
   Open: merged through `gh pr merge` / `glab mr merge`, never with `--admin`; when the host refuses (red CI, a required review, conflicts) its reason is the error and nothing has changed.
   Already merged: confirmed.
   Closed without a merge: a refusal.
   A PR whose base is not the default branch is a refusal too: finish the task that base belongs to first, and the host retargets this PR.
   **Someone else's PR.**
   An open PR whose author is not the signed-in host user (`gh api user`) is not merged unasked: with neither flag finish exits **3** having changed nothing, and `/f10:finish` asks whether to merge it or clean up locally.
   `--merge` merges it as above.
   `--local` cleans up here and leaves the PR open, for a review handed back: no merge, no remote branch deleted, no pull, no dependents released; the plan is archived and the worktree, local branch and workspace go as in step 8.
   It refuses first when the local branch has commits `origin/<branch>` lacks, since deleting the branch would lose them.
   A closed PR is cleaned up only with `--local`.
3. **The merge method** comes from `project.md → Hosting & PR` when it carries `merge method: squash` (or `merge`, `rebase`, in any case).
   Absent, the repo's settings decide: on GitHub the one method allowed, else squash with a note beneath the report saying the default was taken and how to declare one; on GitLab the project's squash option, since GitLab fixes the strategy per project.
4. **The remote branch is deleted** when origin still has it, through the host's API (glab's `--remove-source-branch` usually did it already).
   Its local copy, `refs/remotes/origin/<branch>`, goes too: left behind, it reads as a branch, and drive or start would reopen the finished task.
   On GitHub, deleting the branch is what retargets a PR stacked on it to main.
   GitLab retargets when the base's MR is merged, so the same stacked flow works there by a different trigger; deleting the branch alone would not.
5. **Main is pulled** with `--ff-only` when the main checkout is on the default branch, else the default branch is fetched into without touching the checkout, and a note says so.
   A main checkout on the task's branch has the default branch fetched into without a note, and stays on the task's branch until after the report, so a failure before then leaves a rerun the same checkout to find.
   Either way the merge commit must be in the local default branch, or the run stops here with nothing removed.
6. **Dependents are released.**
   A branch another task started with `--after` on this one has `branch.<name>.f10-after` and `.f10-after-branch` in local config.
   Both are cleared, since the base is in the default branch now and the dependent's pr targets it from here.
   The dependent branched before that code existed, so it is marked `branch.<name>.f10-landed` with the base task's id.
   Its next ship, or `/f10:catchup`, brings the default branch in before anything else runs and clears the mark.
   A note names each dependent.
7. **The plan is archived**: with in-repo storage `<worktree>/.f10/plans/<ID>.md` moves to main's `.f10/plans/archive/<ID>.md` (`<ID>.<N>.md` when taken, older archived versions first); with out-of-tree storage it moves within the shared root.
   With the branch checked out in main, the plan moves within main's `.f10/plans/`, after main's existing archive.
   No plan is a note.
   A plan that cannot be moved is an error and nothing is removed, since with in-repo storage the worktree holds the only copy.
8. **The report**, then the worktree and its local branch are removed - through `wt remove` when worktrunk is on `PATH`, else `git worktree remove` and `git branch -D` - then the Herdr workspace closes (the agent goes with it).
   With `--yes` from inside the worktree, main's workspace is focused before the close so the user lands at home; the close ends the process, which is why it comes last.
   With the branch checked out in main, main is switched to the default branch (`git switch`) and only the local branch is deleted (`git branch -D`, worktrunk or not), and no workspace closes.

Run twice, finish changes nothing it already did: the PR reads as merged, the plan is already archived, there is no workspace to close, and only what is left is removed.

Out of scope: closing the tracker issue (the PR body does that with "Closes #n"), local merges, a task whose PR was never opened, and worktrees f10 did not start.

## Forward

`f10 forward 42 "/f10:ship 42"` hands a command to the agent already working on a task.
The plan and ship skills run it first whenever their argument is a task id, because a task started with `f10 start` has its branch, its plan and its agent in a worktree of its own, and running the command anywhere else would run it on the wrong branch without the plan.

1. **The lookup.**
   The task's branch, found the way start and finish find it (the exact name, else the one `<id>` / `<id>-*` branch); the worktree that has it checked out; the Herdr workspace showing that worktree; the agent under the name start gave it (below), or under `<id>` lowercased with the suffix alone, the name an agent started by an older f10 carries.
   A task started with a suffix is forwarded to with the same suffix.
2. **Three outcomes.**
   Exit 0: the command was submitted, and the report has three rows - `task`, `workspace`, `sent`.
   The command returns at submission, since Herdr's prompt call does, and the outcome lands in that tab; nothing here waits or tails.
   Exit 3: the task is in this checkout (its branch is checked out here, or nowhere, or it has no branch) and the command runs locally; a one-line reason says which.
   Anything else is a failure: outside Herdr, with start's own refusal; no workspace shows the worktree; the agent is idle and not reporting; Herdr refused the prompt.
3. **The marker.**
   Text that begins with `/f10:` and does not already carry `--driven` gets it appended, so the agent knows from its first turn that it is driven ([driven mode](../modes/driven.md)).
   Plain text, an answer or a steering note, is sent as it is.
4. **Idle and not reporting.**
   When Herdr lists the workspace's agent as `idle` while a live run recorded against the worktree still says `running`, the agent is waiting on something f10 cannot see - a Claude Code permission dialog in its pane, a question asked outside driven mode.
   Forward names the agent, the workspace and the step, and sends nothing; a prompt submitted behind that dialog would wait with it.
   An idle agent on a `blocked` run is the driven state this verb exists for, and is sent to.

The answer path: `f10 status 42` reads the run the worktree's agent reports, its `ask` row included; `f10 forward 42 "1. proceed 2. keep ours"` sends the answers back as that agent's next prompt.
`/f10:status 42` does both in one go, with the question put to you in between.

Out of scope: more than one task or more than one command per call, tailing the remote agent's output, and answering Claude Code permission prompts.

## Drive

`f10 drive 42 43 judge ship` runs tasks through a chain of skills from the main session.
Each task waits for its one base, every task whose base is finished runs at once, and the driver exits when every task is through, one needs a human, or nothing else can move.
The driver is this binary rather than a Claude session: ordering, prompting and polling are deterministic, and a session polling an agent pays a round trip and a slice of context per check.

1. **The arguments.**
   Task ids first, or a range `42 -- 47` (every id between, inclusive, at most 50).
   Then, optionally, the chain: any of `plan`, `judge`, `ship`, `review`, `resolve`, `finish`, each once, `finish` last; arrows and commas between them are ignored.
   No chain named: `project.md → Drive chain`, else `plan → judge → ship → review → resolve → finish`.
   `--model`, `--answer` and `--every` go anywhere.
   The command parses its own arguments, so the range's `--` survives.
   `--model` starts every agent on that model, read as start reads it; `<id>:<model>` starts one task's agent on its own, which wins (`42:sonnet 43 --model opus`).
   Only a colon with no slash after it is read this way, so a url keeps its own.
   A range takes the run-wide `--model` only.
   Each model is read once, and any correction or `!!!` fallback is printed before the first task opens.
2. **Validation, before anything is created or prompted.**
   Outside Herdr it refuses as start does.
   Every task is looked up on the tracker.
   In a range, a pull request (GitHub numbers both alike) or an unknown number is skipped with a note; listed by hand, either is an error.
   A task with no branch whose issue is closed, or whose plan sits in main's `plans/archive/`, is finished and skipped.
   A listed task with no branch that has unfinished subtasks (`f10 task children`) is refused: the subtasks are what runs, and `/f10:drive <parent>` is where they are chosen.
3. **The graph.**
   Each task has at most one base: the `branch.<name>.f10-after` its branch recorded once started, else the `After:` line in its body.
   Where the two disagree, the branch wins, with a note.
   The map is rebuilt on every run, and nothing about it is stored.
   A base outside the list that is not finished is refused, and so are tasks that wait on each other in a circle, both before anything is created.
   Within the list the graph sets the order: a base runs first wherever it is listed, and among tasks ready at once the one listed first goes first.
4. **Per task.**
   A task starts once its base is through: finished, or its whole chain run, where the chain has no `finish`.
   Every task that is ready starts in the same pass, since each agent is its own session.
   The worktree, workspace and agent are opened as `f10 start` opens them, with the body's `After:` as the base: a base still on its branch stacks the new one on it, and a finished base leaves it on the default branch.
   A plan the main checkout saved for the task moves into the worktree under start's rules (a worktree's own plan wins, out-of-tree storage moves nothing, a failed move stops before the agent starts).
   A checkout Herdr already shows, as an earlier `f10 start` leaves it, is adopted: its workspace, its agent, or a new agent in its idle shell.
   Run state recorded before the driver started that new agent was written by one that is gone, and halts nothing.
   From then on the task's workspace is the one opened, by id, whatever else shows the same checkout.
   Nothing is prompted at open.
   The chain runs from the task's position: the last skill the driver saw finish, recorded on the branch as `branch.<name>.f10-drive` in local git config, else what the task's run state proves (plan done, ship done).
   A plan the worktree reads when the driver starts its agent counts as plan done, so the default chain resumes at `judge` rather than planning again.
   `finish` runs in the driver, exactly as `f10 finish` would from the main checkout.
   A PR someone else opened is the driver's own ask at finish, `[local | merge]`, answered with `--answer` like any other.
   Every other skill is prompted to the agent, marked `--driven` (which takes every gap on its default and reuses a saved plan): a dependent task's plan and ship name the base task and its plan, and review gets `ci` where `project.md` declares a remote review.
5. **Catchup after finish, and before it.**
   `finish` marks each branch that depended on the finished task `f10-landed`.
   Before a marked task's next skill, the driver sends it `/f10:catchup --driven`.
   Before a task's own `finish`, the driver fetches and tries origin's copy of the branch against `origin/<default>` in memory (`git merge-tree`), as the host will.
   A conflict, as a sibling that merged first leaves one, marks the branch `f10-landed` with the default branch's name and sends the same catchup.
   Once it ends clean, the driver pushes the caught-up branch (`--force-with-lease` against the sha it read, when the catchup rebased) and then merges.
   A branch that still conflicts after one catchup halts the task.
   The chain resumes only on a clean end: the mark cleared (catchup clears it once verify is green), no merge or rebase in progress, and a clean tree.
   A conflict the catchup cannot settle is an ask; anything else halts the task.
6. **Waiting.**
   Every `--every` (15s by default) it reads two signals once for every task in flight: Herdr's workspace list, and the live runs, the newest one recorded against each worktree.
   A turn has begun once Herdr shows the agent working or the run reports after the prompt; Herdr's `done` lasts from the previous turn until someone looks, so it proves nothing.
   No sign within two minutes halts the task, and so does a workspace that still shows no agent status by then.
   Once the agent is idle again, the run says how the turn ended:
   - **blocked with an ask** - the question is printed with its options.
     At the end of that poll the driver prints every task's ask and the command that answers them all, and exits **4**.
     The other agents keep working in their own panes;
   - **blocked without one** (a judge stop, a wait on a base with no code), **failed** or **partial** - the task halts with the run's note and next, and so does every task that waits on it.
     Every other task runs on, and the driver exits **5** once nothing can move;
   - **still running** under plan or ship, a minute after the agent went idle - the agent stopped without reporting, usually a permission dialog in its pane; the task halts naming the workspace.
     Found at prompt time on an agent the driver did not start, the same halt also says how to leave a cancelled run: quit that agent and rerun, and a new one is started in its place;
   - anything else - the skill is done, recorded on the branch, and the next one is sent.
7. **The report.**
   Progress lines as it goes, an end included: a halt or an ask is printed the moment the driver sees it, not only beneath the report.
   Then one row per task in graph order: `already finished`, `finished`, `done: <chain>`, `running <skill>` (still working when the driver exited on an ask), `asked at <skill>`, `halted at <skill>`, `halted: base <id> halted`, `not reached`.
   Exit 0 when every task went through its chain, 4 when any task asks, else 5 when any halted.

Rerunning the same command is the resume: each task's position is read from its own branch and run state.
With `--answer "1. … 2. …"`, the first task stopped on a question gets the text as its agent's next prompt, plain, the way `f10 forward` sends an answer, and the driver polls that turn as it would any other.
Several asking tasks take one answer each, keyed by id: `--answer "42=1. proceed" --answer "45=1. no"`, each sent to its own task's agent.
An agent found mid-turn is attached to rather than prompted again.
A task halted for another reason stays halted until its run changes: settle it in that workspace, or send steering through `--answer`.

`/f10:drive` runs the command in the background, puts an exit-4 question to you in one questionnaire, reruns the command with the answers, and prints the final report.

Out of scope: more than one base per task, and ordering tasks by anything but their recorded dependencies.

## Review

`f10 review` reads the remote review a project declares under `project.md → Review` - four facts: who posts it, where it arrives on the PR or MR, what triggers it, what signals done and marks it handled ([project setup](project-setup.md#review)) - off the current branch's request, or the one `[n]` names.
Nothing declared, or a fact outside the vocabulary, is a refusal naming the line to fix; the trigger is never performed, and `pick` says `the trigger is manual (<how>)` when it finds nothing on a manual project.

- `pick` writes the newest review by the reviewer that nobody handled: a title, a meta line (url, the sha it reviewed, posted at, done, handled), the body verbatim.
  `--json` emits the struct.
  An issue comment stores no sha, so its sha is the request's last commit before it was posted; an inline review carries its own, a check run its head.
- `wait` polls `pick` every `--every` until the review is done, and gives up at `--budget` (10 minutes by default) with the last reason quoted.
  It returns within the budget whatever the host does, so one agent call covers it.
- `ack` leaves the declared handled marker: a reaction, a marker appended to the text, the first checkbox ticked, or the inline review's threads resolved.
  It is the one verb here that writes to the host.
  A review already handled is confirmed, not marked twice; a check run has no marker and ack says so.
  A reaction counts as handled only when it is newer than the review's last edit, so a sticky comment re-edited by a re-review reads as unhandled again.

Both CLIs are spoken: gh's issue comments, pull reviews and check runs; glab's notes, discussions and pipeline jobs.

## Standup

`f10 standup` reads what you did over a period: your commits from git, your PRs or MRs, and other people's you reviewed or merged.
It groups them by project, then task, and gives each task where it ended the period.
It changes nothing and does not fetch: local and remote-tracking branches are read as they are, while PR states come fresh from the host.

**The scope** is this checkout, or the union of:

- `-C <path | name>`, repeatable; a path always works, a name once `F10_ROOTS` is set.
- `--group <name>`, repeatable: a group from `~/.f10/standup.md`.
- `--all`: every checkout under `F10_ROOTS`; with it unset, an error.

Each checkout is read with its own `project.md`: tracker, id format, host.
Checkouts of one repository (a main checkout and its worktrees) share their history, so the repository is read once, from the first of them given.
A checkout that fails (a broken `project.md`, a host CLI that errors) fails the run, named.

**Projects.** A host's issues (GitHub Issues, GitLab work items) belong to their repository, so each such repository is its own project, named after it: `GH-12` in two repositories is two issues.
Any other tracker (a driver, or a declared kind other than the host's issues) is shared: repositories with the same tracker kind and id prefix form one project, named by the prefix, and the same task id in several of them is one task (`ACME-1: svc-a#12, svc-b#40`).
Projects are listed in the order their first repository was given.

**The period** runs since the last standup you reported, in any checkout, so two scopes run the same morning agree.
The report lists the days that had activity (`days`), so Friday to Monday, a working Saturday or a holiday Monday need no calendar.
Activity is a commit by its author date, a PR of yours opened or merged, a force-push of yours, and a review or merge of someone else's PR.

- A standup reported earlier today gives its own start again: a second report the same day covers the same span, not an empty one.
- With no standup stored, the period starts at local midnight of the last working day before today with activity, looked for over the last 14 days.
  With none there, it starts at the previous working day.
- `--since` takes an RFC 3339 time or a `YYYY-MM-DD` date (local midnight) instead.
- `--week` starts at local midnight of last week's Monday and runs to now: the last-week summary for a Monday.

Only `f10 standup mark --since <time> [--at <time>]` moves the stored standup.
`--since` is the `since` the reported standup showed; `--at` is when it was read (default now), and the next standup starts there, so work done between reading the report and marking it is not lost.
The `/f10:standup` skill calls it once it has printed a report over the default period; run it by hand when you report some other way.
Until something marks a standup, every run starts at the last working day.
`f10 standup`, with or without `--json`, never marks, so a script reading the data does not shift the next report's period.

**Your files**, in `~/.f10/`, per person rather than per repo:

- `standup.md`: your settings, YAML frontmatter then free prose.
  `workdays` lists the working weekdays as `mon` to `sun`, default `[mon, tue, wed, thu, fri]`; only a run with no standup stored reads it.
  `groups` maps a name to the checkouts `--group <name>` reads: absolute paths or globs, `~/` expanded.
  A glob keeps the checkouts it matches and skips anything else; a path that is not a checkout is an error.
  Only a run with `--group` reads `groups`.
  An unknown key is an error.
  The prose is for `/f10:standup`, and is where your wording and Deliver adapter are named, and an Assigned adapter where yours should win over a project's.
  A `## <name>` heading naming one of your `groups` starts that group's section, which runs to the next group heading or the end of the file; keep person-wide prose above the first one.
  A section applies on top of the person-wide prose when that group alone is the scope: no other `--group`, no `-C`, no `--all`.

  ```yaml
  ---
  workdays: [mon, tue, wed, thu, fri]
  groups:
    work: [~/code/acme/*]
    oss: [~/code/github.com/amberpixels/r3, ~/code/github.com/amberpixels/k1]
  ---
  ```

  The prose below that frontmatter, with a section for `work`:

  ```markdown
  Keep it short: one line per task, no commit counts.

  ## work

  Write for my lead, outcomes first.
  Deliver: a Gmail draft to lead@acme.example.
  ```

  `--group work` drafts a mail in the lead's wording; `--group oss`, with no section, prints the report in the person-wide wording.
- `standup.json`: the last reported standup, `{"at", "since"}`, written by `mark`.
  It is f10's state, not for editing; delete it to start over from the last working day.

**You** are your git `user.email` and your host login (`gh api user` / `glab api user`), never a name.
A commit is yours when its mailmapped author email equals yours, in any case.
It is in the period when its author date is: a rebase or a `git merge main` replay moves the committer date, not the author date.
A checkout with no host CLI reports commits only.
A host CLI that fails (not logged in, no network) fails the command; it never falls back to commits only.

**Grouping.**

- A commit belongs to the PR whose commit list or merge/squash commit names it.
  A commit on the default branch belongs to the merged PR listing a commit with its author date and subject, which is what a rebase merge keeps.
  Otherwise it belongs to an open PR whose head is the branch it was reached from.
- A PR's task id comes from its head branch, then its title; a commit's from its branch, then its subject, read with the project's id format.
  No id puts the PR or commit in `other`.
- A PR whose base is another of your PRs' head chains under it, listed root first.
  The base `f10 start --after` recorded wins over the host's, which the host retargets to the default branch once the base merges.
  A chain crossing tasks gives the dependent task `after`.
- Your own PR is reported when you opened or merged it in the period, or it carries a commit of yours from it.
- Other people's PRs go to `reviewed` when you reviewed them or merged them in the period.
  On GitHub a review is any review you submitted.
  On GitLab it is an approval or a comment on an MR that lists you as a reviewer: an MR's notes are read only for those, since reading every MR updated in the period costs a call each.

**States**, least advanced first: `started` (open, no changes yet), `in progress` (a draft), `in review` (open and ready), `shipped` (merged), plus `closed` for a PR closed unmerged.
A task takes the least advanced state among its open PRs and its PR-less commits on a branch other than the default (`in progress`).
With nothing open it is `shipped` when a PR merged or a commit landed on the default branch, else `closed`.
A task merged across repositories combines their states by the same rule, each repository read against its own default branch.

**Work and upkeep.**
Every commit and force-push in the period is `work` or `upkeep`, decided from git and the host, never from a commit message.
Upkeep is:

- `base-merge`: a merge whose second parent is on the first-parent line of the default branch, or of the base `f10 start --after` recorded.
  A merge made on the default branch, pushed or not, or of any other branch, is work.
- `rebase`: a force-push to your PR whose old and new commits, each taken against the PR's base, pair up with the same patches (`git range-diff`, every pair `=`).
  Without the old commits in the local object store it is work: standup never fetches them.
- `files`: a commit touching only lockfiles (`go.sum`, `package-lock.json`, `yarn.lock`, `Gemfile.lock`, `Cargo.lock` and the like), generated files (`*.pb.go`, `*_gen.go`, `*.gen.*`, `zz_generated*`, anything marked `linguist-generated`, Go files with the `Code generated ... DO NOT EDIT.` header) or snapshots (`__snapshots__/`, `*.snap`, `*.golden`), plus the project's `standup.upkeep` globs.
  A manifest such as `go.mod` or `package.json` is work.
- `ci-fix`: a commit touching only test files, at most 30 lines added plus deleted, whose parent's CI was red and finished before the commit's author date.
  It needs a host; with none, or with a parent the host never saw (an unpushed commit), it never applies.

Anything else is work: hiding a real change costs more than listing a small one.
A task is `upkeep` when its period holds at least one upkeep item and no work item; opening or merging a PR in the period is work.
The text report folds upkeep tasks into one `kept up to date` line.
Your own PR is also reported when its only activity in the period is a force-push of yours: a rebase keeps author dates, so it leaves no commit in the period.

**JSON**, schema 2.
The same history gives the same bytes in the same zone: no run timestamp, every time in UTC (`days` are local dates), every list in a fixed order.
A breaking change bumps `schema`.

```json
{
  "schema": 2,
  "since": "2026-10-14T00:00:00Z",
  "period": "last_standup",
  "days": ["2026-10-14", "2026-10-15"],
  "projects": [
  {
  "name": "svc-a",
  "tracker": {"kind": "GitHub Issues", "prefix": "GH"},
  "repos": [
    {"name": "svc-a", "checkout": "/path/to/checkout", "host": "gh",
     "user": {"email": "you@example.com", "login": "you"}}
  ],
  "tasks": [
    {
      "id": "GH-12",
      "title": "the root PR's title, empty with no PR",
      "state": "in review",
      "class": "work",
      "after": "GH-11",
      "days": [{"date": "2026-10-14", "class": "work"}, {"date": "2026-10-15", "class": "upkeep"}],
      "prs": [
        {
          "repo": "svc-a", "number": 40, "title": "...", "url": "...", "state": "in review", "draft": false,
          "head": "GH-12/x", "base": "GH-11/y", "base_pr": 38,
          "created_at": "...", "merged_at": "...",
          "commits": [{"sha": "...", "subject": "...", "at": "...", "branch": "GH-12/x", "parents": ["..."],
                       "class": "upkeep", "upkeep": "base-merge", "repo": "svc-a"}],
          "force_pushes": [{"at": "...", "before": "...", "after": "...", "class": "upkeep", "upkeep": "rebase"}]
        }
      ],
      "commits": []
    }
  ],
  "other": {"prs": [], "commits": []},
  "reviewed": [
    {"repo": "svc-a", "number": 41, "title": "...", "url": "...", "state": "shipped", "author": "them",
     "actions": [{"kind": "reviewed", "at": "..."}, {"kind": "merged", "at": "..."}],
     "days": ["2026-10-15"]}
  ]
  }
  ]
}
```

| field | meaning |
|---|---|
| `period` | how `since` was chosen: `last_standup`, `last_working_day`, `since` (the flag) or `week` |
| `days` | the local dates (`YYYY-MM-DD`) with any of your activity in the period, across every repository, ascending |
| `projects` | one per tracker, in the order their first repository was given |
| `projects[].name` | the repository's name, or a shared tracker's id prefix |
| `projects[].tracker` | its `kind` and id `prefix` as `project.md` gives them |
| `repos` | the repositories read for the project, in the order given |
| `repos[].host` | `gh`, `glab`, or `null` with no host CLI (then `user.login` is absent and that repository adds nothing to `reviewed`) |
| `repos[].user` | your git email and host login there; either can differ per repository |
| `repo` | on a PR, a commit or a reviewed PR: the repository it is in; a PR number names a PR only within it |
| `tasks` | by id prefix, then number; a task id seen in several repositories of a project is one task |
| `tasks[].state` | combined across repositories, each read against its own default branch |
| `tasks[].after` | the task its root PR is stacked on; absent when none |
| `tasks[].days` | the local dates with the task's activity, ascending, each with the class of that day's activity alone: a task worked on one day and only caught up with its base the next is `work`, then `upkeep`; a task merged across repositories has the days of all of them |
| `reviewed[].days` | the local dates of your actions on that PR in the period, ascending |
| `tasks[].prs` | per repository, in the order given; within one, chain order: a root, then the PRs on it; roots and siblings by number |
| `tasks[].commits`, `other.commits` | commits on no PR of yours, by author date, then sha |
| `other.prs`, `reviewed` | per repository, in the order given; within one, by number |
| `prs[].base_pr` | the PR whose head this one's base is; absent at a chain root |
| `prs[].merged_at` | absent unless merged |
| `prs[].commits` | your commits on it in the period, by author date, then sha |
| `commits[].at` | the author date |
| `commits[].branch` | the branch it was reached from, remote prefix dropped; the default branch (origin/HEAD, else the host's) for anything it reaches |
| `commits[].parents` | parent shas; two or more is a merge commit |
| `class` | on a task, a commit or a force-push: `work` or `upkeep` |
| `upkeep` | on an upkeep commit or force-push: `base-merge`, `rebase`, `files` or `ci-fix`; absent on work |
| `prs[].force_pushes` | your force-pushes to it in the period, by time, then after-sha |
| `reviewed[].actions` | by time, then kind |

## References

A reference is a bare number, a `#123`, a prefixed id in any case, or a url whose last path segment is one of those (`.../issues/42`, `.../browse/ABC-1042`).
With no id, a reference is looked up in one cascade: the id you passed, else the id in the current branch name, else the task this session recorded.
The branch lookup needs the project's task id format, declared in `project.md` or derived from the project's name.

`-C` answers for another checkout.
It names one checkout; only `f10 standup` takes it more than once.
A path always works.
A bare name works once `F10_ROOTS` says where to look:

```bash
f10 -C ../r3 task read 12
export F10_ROOTS=~/code/github.com/*/*
f10 -C r3 task read 12
```

## Output

`read` picks its presentation from where it writes: a pager when `$PAGER` is set and stdout is a terminal, raw markdown into any pipe.
`f10 task read | glow` works because a pipe is not a terminal.
`search` prints a table to a terminal and JSON to a pipe.
`children` prints a tree to a terminal and JSON to a pipe or with `--json`.

## Children

`f10 task children 71` lists a parent's open subtasks, the links capture makes: GitHub sub-issues through `gh`, GitLab work-item children through `glab api graphql`, or a driver's `children` verb.
Closed subtasks are dropped.
Rows come in tree order: a subtask whose `After:` base is a sibling follows it one level deeper, and siblings at one level go by number.
A base outside the siblings, or a cycle among them, puts the subtask at the top level.
A subtask that is itself a parent is listed, not expanded.

| field | meaning |
|---|---|
| `id` | the subtask, in the project's id format |
| `title`, `url`, `state` | as the tracker holds them |
| `labels` | label names; `[]` for none |
| `after` | the id its body's `After:` line names, absent when none |
| `finished` | no branch, and its plan archived, as drive decides it |
| `depth` | 0 directly under the parent, one more per `After:` step |

A task with no subtasks prints `[]`.
Exit 3 means the tracker has no parent link: a driver that declines the verb on a tracker no host reaches, or a host without sub-issues or work items.

## Trackers

The host's issues need nothing declared: the git remote says GitHub or GitLab, and `gh` or `glab` does the rest.
Anything else goes through an executable at `.f10/driver`, per [the driver contract](driver-contract.md).
A driver that declines a verb falls back to the host's issues.

Task ids take the prefix `project.md` declares, else one derived from the main checkout's name: a short name with a digit as is (`f10` → `F10`, `r3` → `R3`), initials across hyphens, underscores or dots (`git-undo` → `GU`, `notion-sdk-go` → `NSG`), the first three characters of one word (`herdr` → `HER`).
The prefix names plan files and is how a branch is searched for its task, so it must not move once used: `f10 init` writes it into `project.md`, and `f10 config` shows which it is and where it came from.
