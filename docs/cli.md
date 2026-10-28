# The `f10` binary

A lookaround, and the commands that start, reach, drive and finish a task.
The lookaround verbs explain the loaded configuration and read the things it points at, changing nothing.
Five verbs act: `init` registers a checkout and never overwrites, `start` creates the branch and worktree a task is worked on in and opens them in Herdr, `forward` hands a command to the agent a task already has, `drive` runs a list of tasks through a flow that way, and `finish` merges the task's PR (or a branch never pushed, locally) and removes what start created.

The plugin requires the binary and ships it as `bin/f10`, a shim that runs the build matching `plugin.json`'s version.
The skills and the binary are one contract: the skill text describes the flags the binary takes.
The shim resolves the binary in this order, caching under `~/.claude/f10/bin/`:

1. **Cache:** this checkout's own build (`f10-<version>-<checkout key>`), else the release binary (`f10-<version>`).
2. **Download:** the `cli/v<version>` release asset for this OS and CPU (darwin or linux, amd64 or arm64), fetched with `curl` from this repo's public release with no auth, checked against the release's `checksums.txt`. A checksum mismatch fails; it never falls through to a build.
3. **Build:** the download failed and Go is installed, so `go build` of the plugin's `cli/` with the version stamped in.
4. **Neither:** one line on stderr saying what is missing and how to fix it (install `curl` or check the network, or install Go), exit 1.

A SessionStart hook runs the resolution in the background, so the first real call rarely waits, and shows the same line when neither `curl` nor Go is there.
It also puts the plugin's `bin/` first on the session's PATH (through `CLAUDE_ENV_FILE`), so an older `f10` in `~/go/bin` cannot shadow the shim.
Builds are keyed by the checkout they came from, so a worktree's build never replaces the one another checkout of the same version runs.
The hook rebuilds a checkout's build once its `cli/` holds newer sources.
A checkout running the release binary has no build of its own: after editing `cli/` there, run `just install` once, and the hook keeps that build current from then on.

```bash
f10 --version   # always the plugin's version through the shim
just install    # dev: rebuild this checkout's cli/ into the cache slot (bin/f10 --shim-rebuild) and $GOBIN (go install)
```

`--version` prints the version stamped at build time (`-X main.stampedVersion`), as the shim and the release workflow build it.
An unstamped build prints the module version for `go install ...@vX.Y.Z`, or the commit and its date for a plain checkout build.
Pushing a `cli/vX.Y.Z` tag runs `.github/workflows/release.yml`, which attaches the four binaries and `checksums.txt` to a GitHub Release; the release convention pushes that tag beside `f10--vX.Y.Z`.
A `flag provided but not defined` from `f10` means some other `f10` ran in place of the shim: `f10 doctor` names it.

## Commands

```bash
f10 init                 # register this checkout: write project.md from detection
f10 init --migrate       # move the facts an older project.md declares in prose into its frontmatter, and old keys into flows
f10 config [--json]      # the effective configuration, with the origin of every value
f10 flow [--json]        # the declared flows, each with what selects it, and drive_finish
f10 completion <shell>   # the completion script for bash, zsh, fish or pwsh; --flow completes the declared flows
f10 doctor [--json]      # check the plugin, the binary and the environment; changes nothing

f10 task read [id]       # the task as markdown; a bundle (id+id) reads every member
f10 task ref <id>... [--md] [--fresh]   # `<id> (<title>)` per task, a markdown link with --md; from what f10 already read
f10 task open [id]       # in the browser, no id the task list (--link writes the url, --print the item or the list's first page, ranked by priority)
f10 task search <query>  # rows, not a picker
f10 task children [id] [--json]   # the task's open subtasks, in After: order
f10 task create [--status s] [--priority p] [--label l]... [--parent id] < task.md   # through the driver; prints the new id
f10 task comment [id] < comment.md   # a comment on the task; prints its url when the tracker gives one
f10 task update [id] [--status s] [--priority p] [--label l]... [--assignee a] [--set k=v]...   # absolute values
f10 task retitle [id | --all] [--dry-run]   # write tracker.title onto the host's issue titles
f10 plan read [id]       # the plan saved for that task
f10 plan open [id]       # in your editor (--link writes the path)
f10 demo open [id]       # the demo report, in the browser (--link writes the path)
f10 pr open [n]          # the PR or MR, no number their list; gh or glab decided by the remote (--link, --print as for task)
f10 review pick [n] [--latest] [--json]               # the latest unhandled remote review, as markdown
f10 review wait [n] [--budget 10m] [--every 20s]      # poll it to done within a budget
f10 review ack [n] [--id <id>]                        # leave the declared handled marker on it, or on the review --id names
f10 review settle [n] --id <id> --revision <rev>      # leave the declared settled marker, unless it was rewritten
f10 trace trailers --step <step> [--task <id>] [--round n --fixed n --skipped n]   # the Task:, Step: and Review: lines a step's commit ends in; nothing with traces off
f10 trace state --step <step> [--round n] [--open n] [--asks n] [--task <id>] [--body-file <file>]   # rewrite the hidden state block in the branch's open PR body, or put it into a drafted body; nothing with traces off
f10 status [id] [--all] [--json | --markdown]   # where the run is: a task's in its worktree (bare: the session's task, else the branch's, with that output's --json shape), else this session's, every live one, or a row per child of a parent
f10 standup [--since <time> | --week] [-C <checkout>]... [--group <name>]... [--all] [--json]   # your commits and PRs since your last standup, grouped by project and task
f10 standup mark --since <time> [--at <time>]      # record a standup as reported: the next one starts where it ended
f10 board [<window> | --since <window>] [--state] [--remote] [-C <checkout>]... [--group <name>]... [--all] [--detail] [--by person] [--json | --html] [--out <path>]   # what changed today (else the last working day); --state: where every task stands; one project or several
f10 stats [--since 30d] [<id>]   # active plan and implement minutes per task, with size and density; a median row

f10 start <id>[-suffix] | <id>+<id>... [--plan | --local | --flow <name> | --discuss] [--after <id> | --base <branch>] [--model <model>] [note...]   # a worktree, a Herdr workspace, a prompted agent
f10 forward <id>[-suffix] <text...>   # hand a command, or an answer, to the agent working on the task
f10 drive <id | id+id>[:model]... | <id> -- <id> [--flow <name> | --local] [--model <model>] [--answer [<id>=]<text>] [--every 15s]   # a list through a flow, each task once its base has a commit
f10 finish <id>[-suffix] [--yes] [--close <ids|all|none>]   # merge the PR (a branch never pushed: here), pull main, archive the plan, close the workspace, remove the worktree
f10 janitor [--dry-run]  # remove task branches whose PR merged or closed outside finish, locally and on origin; start runs it every 6 hours

f10 lore add <kind> --change <id> [--anchor path:line[-end][@commit][#symbol]]... [--supersedes <id>] [kind flags] [-m <text> | -F <file> | stdin]   # write one lore entry, print its id
f10 lore show <change> [--json]     # the change's effective entries, the intent first
f10 lore why <path:line> [--json]   # the change behind a line: intent, latest verdict, entries on the line
f10 lore lessons <path> [--json]    # the lessons to read before editing a file
f10 lore check <change>             # exit 1 naming why the change does not pass its gate
f10 lore validate [<change>]        # lint entries against the schema
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

- the prompt is `/f10:plan <id> && /f10:ship <id>` with every gap on its default, closed by `--driven`, the marker that tells the agent it is prompted from outside and routes every question through run state ([driven mode](../modes/driven.md)); `--plan` prompts the plan alone; `--flow <name>` ships through that declared flow, `/f10:ship <name>: <id>`, and `--local` is `--flow local`, so nothing is pushed; a flow with no leading `plan` sends ship alone; `--discuss` prompts only the fetch: the agent summarises the task and waits for you in its own pane, so the prompt is plain text with no `--driven` marker.
  Words after the id are a note the agent acts on after the summary and before it waits (`f10 start 42 --discuss "/f10:judge it first"`); a note goes only with `--discuss` or a PR pickup, and any other mode refuses one, since a driven run takes its instructions from the task.
  The three are exclusive, and `--flow` or `--local` with `--plan` or `--discuss` is refused, since both stop before ship
- the flow is recorded on the branch as `branch.<name>.f10-flow` in local git config, for `f10 status` and a rerun of `f10 drive`; with `flow_by_area` declared and no flow named, nothing is recorded and the prompt is `/f10:ship <id>` alone, since ship picks the flow from the task's area
- a plan the main checkout saved for the task (`.f10/plans/<id>.md`, written by `/f10:plan` after a discussion there) stays in place: every worktree's storage root is main's `.f10/` (or the out-of-tree root), so the worktree's agent reads it there.
  The agent ships it: the prompt is `/f10:ship <id>`, or `/f10:ship <name>: <id>` with `--flow` or `--local`; `--plan` still plans, and the plan step archives the saved one; `--discuss` names the plan's path.
  The report gains a `plan` row.
  A worktree from before storage moved to main's ships its own plan when main has none for the task (start rerun on it).
  `f10 drive` reads main's plan the same way, when it starts the task's agent.
- the agent is started with an `--add-dir` for each task-file dir of the storage root (`plans`, `reviews`, `verdicts`, `spikes`, `lore`, `images`, created if missing) whenever that root lies outside the worktree, so its writes there ask no permission; `instructions/` and `driver/` are left out, so a worktree's agent cannot change what every checkout loads.
- `--after <id>` bases the branch on that task's existing branch, local or on origin, and records the dependency on the new branch in local git config (`branch.<name>.f10-after`, the task; `branch.<name>.f10-after-branch`, its branch), so it never leaves the machine.
  The prompt names the base task and the path of its plan as the contract to design against, not the checkout, with no fallback for its absence.
  The path is in the shared storage root (in a base worktree from before storage moved to main's, that worktree's own), and a note beneath the report says when no checkout reaches it or the plan is not written yet.
  A base still at the default branch's commit is noted as a task with no code yet.
  It fails before creating anything when the task has no branch
- with neither `--after` nor `--base`, the task body decides: a line `After: <id>` (alone on its line, the last of the body by convention) is read as `--after <id>` when that task has a branch, and noted beneath the report.
  When the base has no branch and is finished (its issue closed, or its plan in main's `plans/archive/`), the branch is based on the default branch with a note; when it is not finished, start refuses before creating anything.
  A body that cannot be read is a note, never a refusal
- a parent task with unfinished subtasks (`f10 task children`) is refused before anything is created, since its agent would ship the whole umbrella on one branch: start exits **6** naming the open subtasks, and `/f10:start` asks whether to drive them (`/f10:drive <parent>`) or start one of them.
  The check is the one drive makes.
  It is skipped for `--discuss`, which talks an umbrella task through, for a PR pickup, and for a task that already has a branch (start rerun on it); a parent whose subtasks are all finished, and a tracker with no parent link, start as before
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
- every lookup of a task's branch (start, forward, status, finish, drive, `--after`) reads the `f10-task` link and the `f10-tasks` riders first and falls back to the name patterns (`<id>`, `<id>/...`, `<id>-...`, `<id>+...`), then, for local branches only, to the id past a prefix (`feat/<id>-...`), as `f10 task read` reads a branch; a task read with no id in a picked-up worktree finds its task through the same link.
  With no local branch, start also finds the one task branch on origin (`refs/remotes/origin/`) and creates the local branch tracking it rather than a second branch from main.
  It reads what the last fetch saw; for a branch pushed since, run `git fetch` or pass the PR link
- a task that already has a branch gets its worktree reused, or created when the branch has none, and `--base` is noted as ignored since the branch keeps its base - `--after` likewise for the base, while its dependency is recorded all the same; a workspace Herdr already shows is adopted rather than opened again, keeps its agent, and nothing is prompted twice.
  Where several workspaces show the checkout, the one with an agent is adopted, else the first, and each other one is named in a note with the command that closes it; none is closed.
  One with no agent in its root pane (a start that failed earlier) gets one started and prompted when that pane is an idle shell; a pane running anything else is left alone
- a fresh pane whose shell is still running its rc files refuses the agent as busy; start retries every 200ms for up to 5s before failing with Herdr's error, and prints a line to stderr at least every second while it waits
- `--flow` and `--local` refuse before creating anything when `project.md` declares no such flow, printing the list `f10 flow` prints, since ship never picks a flow unasked

**Bundles.**
`f10 start ABC-1+ABC-2` ships several tasks as one branch, one worktree, one agent and one PR.
The first id is the **lead** and keeps everything keyed on a task (plan file, findings, verdicts, agent name, run state, ledger); the others are **riders**.
The branch is `ABC-1+ABC-2/<slug>`, the slug from the lead's title, and start records `branch.<name>.f10-task` (the lead) and `branch.<name>.f10-tasks` (the riders, space-separated) once it exists.
Every other command takes any member's id and acts on the bundle: `f10 finish ABC-2` finds the ABC-1 branch.
A bundle branch fetched from origin has no local record; start reads its riders from the branch name and records them.
Before anything is created, start refuses a parent among the members, a rider already on another branch (naming it), a branch that exists with other riders (riders are fixed at start) and a suffix.
The bundle's base is the one `After:` its members name outside it; a line naming another member is dropped, and two different bases are refused.
`f10 drive` takes a bundle as one entry; a task `After:` a rider waits for the whole bundle.
Drive's finish asks no riders question, since a resume carries only the latest answer: it leaves every rider open, and its report names them for `gh issue close` or the tracker.

A recorded dependency is read back by the ship, pr and catchup steps, not by this binary.
Before the first pipeline step, unless `catchup: off`, ship brings the branch up to date with the base when it moved - always a merge, since a rebase onto a branch that later rebases or squash-merges replays its old commits - and asks once - wait, or proceed on the plan's assumption - when it still equals the default branch.
A conflict there stops ship and hands off to `/f10:catchup`, which settles it with both sides kept.
Ship also catches up with the base, or the default branch when none is recorded, before every `pr` entry and a change judge, unless `catchup: off`.
The pr step opens the PR or MR against the base branch, so it is stacked and the base's finish retargets it to the default branch; a base not yet on origin stops that step rather than pushing another task's branch.
`f10 finish` on the base task clears the record from every dependent, since the base's code is in the default branch from then on.

It runs inside a Herdr session only.
Outside one it stops before touching anything and points at https://herdr.dev.

After its report, start runs the [janitor](#janitor) when its last run is 6 hours old or more; its lines follow on stderr, prefixed `janitor:`, and a janitor failure is one warning line that never fails the start.

## Finish

`f10 finish 42` is the inverse of start, in the order that keeps the local side honest.
It finds the task's branch the way start does (the branch linked by `f10-task` or `f10-tasks`, else the exact name, else the one `<id>` / `<id>-*` / `<id>+*` branch, else the one local branch carrying the id past a prefix, like `feat/<id>-slug`) and the checkout that has it checked out; without either it stops, since finish removes only a branch it can tie to the task.
For a bundle, finish needs `--close`: the riders the PR delivers, comma-separated, `all` or `none`.
Without it, finish exits **7** before changing anything, and the finish skill asks.
Before an open PR merges, each rider left open has its closing keyword in the PR body turned into `Part of #n`; after a merge that already happened, each is reopened instead.
A tracker that is not the host closes nothing on merge, so the notes name the riders to close there.
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
   A PR whose base is not the default branch is a refusal too: finish the task that base belongs to first, and its finish retargets this PR.
   **Someone else's PR.**
   An open PR whose author is not the signed-in host user (`gh api user`) is not merged unasked: with neither flag finish exits **3** having changed nothing, and `/f10:finish` asks whether to merge it or clean up locally.
   `--merge` merges it as above.
   `--local` cleans up here and leaves the PR open, for a review handed back: no merge, no remote branch deleted, no pull, no dependents released; the plan is archived and the worktree, local branch and workspace go as in step 8.
   It refuses first when the local branch has commits `origin/<branch>` lacks, since deleting the branch would lose them.
   A closed PR is cleaned up only with `--local`.
   **A branch never pushed.**
   No PR, no `origin/<branch>` and no upstream recorded for the branch: the branch merges into the local default branch instead, by the merge method below, built in memory (`git merge-tree`, `git commit-tree`) so no checkout is touched.
   The merge is built on the local default branch, or on `origin/<default>` when the local one only lags it, so origin's commits never fold into the merge; the two diverged is a refusal.
   A squash's subject is the task and its title, its body the branch's commit subjects and, with traces on, the `Task:` trailers; a merge writes a two-parent commit; a rebase lands only a fast-forward.
   The default branch then moves: `git merge --ff-only` when the main checkout is on it, else `git update-ref`.
   A conflict, or a rebase behind the default branch, is refused with nothing changed and `/f10:catchup` named; a branch already in the default branch lands nothing.
   Nothing is pushed, pulled or deleted on origin, and the task stays open on the tracker, since no PR closes it: the notes say so.
   A pushed branch with no PR is a refusal: open one, or delete origin's copy to merge here.
   So is a branch with an upstream origin lacks (a request the host merged may no longer be listed by branch), and `--local`, which merges nothing.
3. **The merge method** comes from `project.md → Hosting & PR` when it carries `merge method: squash` (or `merge`, `rebase`, in any case).
   Absent, the repo's settings decide: on GitHub the one method allowed, else squash with a note beneath the report saying the default was taken and how to declare one; on GitLab the project's squash option, since GitLab fixes the strategy per project.
4. **The remote branch is deleted** when origin still has it, through the host's API (glab's `--remove-source-branch` usually did it already).
   Its local copy, `refs/remotes/origin/<branch>`, goes too: left behind, it reads as a branch, and drive or start would reopen the finished task.
   Before the delete, every open PR or MR based on the branch is retargeted to the merged one's base (`gh pr edit <n> --base` / `glab mr update <iid> --target-branch`), a note each: a branch deleted through the API closes the PRs still based on it.
   A failed retarget stops finish with the branch left on origin, so the stacked PR stays open; a rerun finds the request merged and retries.
   A branch the merge already removed (glab's `--remove-source-branch`, GitHub's automatic delete) leaves nothing to retarget: the host did it.
5. **Main is pulled** with `--ff-only` when the main checkout is on the default branch, else the default branch is fetched into without touching the checkout, and a note says so.
   A main checkout on the task's branch has the default branch fetched into without a note, and stays on the task's branch until after the report, so a failure before then leaves a rerun the same checkout to find.
   Either way the merge commit must be in the local default branch, or the run stops here with nothing removed.
6. **Dependents are released.**
   A branch another task started with `--after` on this one has `branch.<name>.f10-after` and `.f10-after-branch` in local config.
   Both are cleared, since the base is in the default branch now and the dependent's pr targets it from here.
   The dependent branched before that code existed, so it is marked `branch.<name>.f10-landed` with the base task's id.
   `/f10:catchup`, or its next ship unless `catchup: off`, brings the default branch in before anything else runs and clears the mark.
   A note names each dependent.
7. **The plan is archived**: `<storage root>/plans/<ID>.md` moves to `<storage root>/plans/archive/<ID>.md` (`<ID>.<N>.md` when taken), within main's `.f10/` or the out-of-tree root.
   A worktree from before storage moved to main's still holds its own `.f10/plans/<ID>.md`: it moves to main's archive, its older archived versions first.
   No plan is a note.
   A plan that cannot be moved is an error and nothing is removed, since such a worktree holds the only copy.
8. **The report**, then the worktree and its local branch are removed - through `wt remove` when worktrunk is on `PATH`, else `git worktree remove` and `git branch -D` - then the Herdr workspace closes (the agent goes with it).
   With `--yes` from inside the worktree, main's workspace is focused before the close so the user lands at home; the close ends the process, which is why it comes last.
   With the branch checked out in main, main is switched to the default branch (`git switch`) and only the local branch is deleted (`git branch -D`, worktrunk or not), and no workspace closes.

Before the merge lands, finish also measures the branch for `f10 stats`: lines added plus deleted against the base it forked from (its `--after` base branch, else `origin/<default>`, else the local default branch for a branch never pushed), lockfiles, generated code and snapshots left out, with the markdown share.
Measured after landing, a local merge would read zero.
The size goes to the ledger once the merge landed; a branch with nothing to measure records none.

Run twice, finish changes nothing it already did: the PR reads as merged, the plan is already archived, there is no workspace to close, and only what is left is removed.

Out of scope: closing the tracker issue (the PR body does that with "Closes #n"), a pushed branch whose PR was never opened, a checkout with no origin, and worktrees f10 did not start.

## Janitor

`f10 janitor` clears the task branches that outlived their task: a PR merged in the host's UI or by `gh pr merge`, a worktree removed by `wt remove`.
`f10 finish` cleans up after itself; the janitor catches everything that went around it, without relying on a host setting such as GitHub's automatic head-branch deletion.
`f10 start` runs it after its report at most once every 6 hours, `f10 janitor` runs it by hand, and `f10 janitor --dry-run` prints what it would remove and changes nothing.

It considers local branches and origin's (one `git ls-remote --heads origin`) tied to a task id by the rule finish reads a branch with: the `f10-task` link, else the bundle the name spells, else the id it carries.
Other branches and the default branch are never touched, and neither is a branch any checkout holds, main's included; those are dropped before the host is asked anything.
For each remaining branch the host lists its requests (`gh pr list --head` / the GitLab MR API by source branch), and the branch goes only when all of these hold:

- no request for it is open, and the newest one is merged, or closed without a merge, an abandoned task;
- its tip, locally and on origin, is the request's head sha, so no commit the request lacks is lost;
- no open request is based on it (`gh pr list --base` / `glab mr list --target-branch`), since deleting a stacked PR's base closes it on the host;
- for a closed request, no local branch was started `--after` it.

A branch with no request, or any branch when the checkout has no host CLI, goes only as finish would merge it as never pushed: no copy on origin, no upstream recorded, and its changes already in the local default branch.
A host that fails to answer for a branch keeps it, with a line.

Removal, per branch: a merged request's dependents are released as finish releases them; origin's copy is deleted through the host's API with its local tracking ref, but only for a request the signed-in user opened (someone else's keeps its origin branch, with a line, and one only on origin is not touched); then the local branch (`git branch -D`).
Once every branch of a task is gone, its plan moves from `plans/<id>.md` to `plans/archive/` with finish's numbering, and its images are dropped; a task with another branch left, such as a second attempt still in a worktree, keeps both, with a line.
A step failing keeps that branch, with a line, and the run goes on to the next.

The output is one line per removal and per branch kept for a reason worth knowing; a branch whose request is still open is kept without one.
`nothing to clear` when nothing was.
The last run is the mtime of `janitor.stamp` in the shared storage root (main's `.f10/`, or the out-of-tree root).
The stamp moves before the run, so `f10 drive` starting several tasks at once runs it once; `--dry-run` leaves it alone.

## Forward

`f10 forward 42 "/f10:ship 42"` hands a command to the agent already working on a task.
The plan and ship skills run it first whenever their argument is a task id, because a task started with `f10 start` has its branch, its plan and its agent in a worktree of its own, and running the command anywhere else would run it on the wrong branch without the plan.

1. **The lookup.**
   The task's branch, found the way start and finish find it (the exact name, else the one `<id>` / `<id>-*` branch, else the one local branch carrying the id past a prefix); the worktree that has it checked out; the Herdr workspace showing that worktree; the agent under the name start gave it (below), or under `<id>` lowercased with the suffix alone, the name an agent started by an older f10 carries.
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

## Status

`f10 status 116` on a parent prints one row per child, closed ones included, in `f10 task children` order and indented the same way:

```
FT-117    done       trailers + traces switch  finish        #128 merged
FT-118    reviewing  PR state block            review        #129
  FT-120  waiting    look-around + f10 board   after FT-118
FT-121    halted     sidebar icons             ship
          halt: agent gh-121-1a2b3c in workspace w3 is idle while its run says ship running: implement
```

The columns are the id, the stage, the title cut to fit the terminal, the current or last step, and the PR with its state when it is not open.
Each row reads the child's live run in its worktree, else the ledger's last record of it, which outlives the worktree `f10 finish` removes; the PR comes from the host, by branch or by the url the ledger kept.
On a terminal the id and the PR are links (OSC 8).
`--markdown` writes the same rows as a markdown table, ids and PRs linked, for a skill to print in chat where a fenced block shows no links; it applies only to rows, and a run's block prints as without it.

| stage | when | color |
|---|---|---|
| `done` | finished (finish ran, the issue closed, the plan archived) or the PR merged | green |
| `shipped` | the ship pipeline ended, the PR not merged yet | faint green |
| `asking` | a driven run stopped on a question; the `ask:` line under it holds the question | red |
| `halted` | a phase failed, partial or blocked, or drive halted the task itself (an idle agent, a refused finish) after its run's last word; `halt:` and `next:` lines under it | red |
| `implementing` | ship running any step but the three below | yellow |
| `reviewing`, `resolving` | ship running review, resolve | orange |
| `judging`, `planning` | ship running judge; capture or plan under way | blue |
| `planned` | the plan is saved, ship not begun | faint blue |
| `waiting` | no run yet, and its `After:` base is not done | faint |
| `not started` | nothing recorded | faint |

Color is off when stdout is not a terminal or `NO_COLOR` is set.

`f10 status <id>` lists the task's **gates** under the `ship` row, while the run is going and after it finished, and the one `done` row of a task with no branch left prints them as lines under it:

```
ship            running: review:local
  implement     done
  cleanup       done
  pr            done
  review        local: round 1 running (in ship) · remote: pending
  judge         change: pending
  pr            pending
```

- **The flow** is ship's entries as `f10 state pipeline` recorded them, else the entries after `plan` of the flow the branch records (`branch.<name>.f10-flow`, else `default`), inside the chain `f10 drive` recorded on the branch (`branch.<name>.f10-chain`), which adds `finish`: a `judge` before `implement` is the **plan** judge, any later one the **change** judge, and `plan` and `resolve` are no rows of their own.
- **A plain step** is `done` before the step ship is on, that step takes the run's status (`running`, `blocked`, ...), the rest are `pending`; once ship is done, a step it never entered is `skipped`.
- **Review** has a local and a remote half, each `pending`, `running` (its round, and `in ship`, `driven` or `manual`; `resolve running` while a resolve settles it, and in `review:local+ci` `round N in, waiting for the other half` once its own findings landed), `open` (`N open of M found`, or `open (not handled)` on the host), `settled` (`3 found (2 fixed, 1 skipped)`, `clean`), or `unknown`.
  A review, resolve or judge started outside ship counts from its `run_started` on the task's branch or checkout until its outcome lands, its session stops or starts another skill, or the state TTL passes; `ci` in its arguments makes it the remote half's, and inside ship the half reads `resolve running` once a round lands after ship entered the review.
  The remote half is in the flow when an entry names a remote review (`review:ci`, or both halves in one `review:local+ci`, which shows both halves on the one review row), or when Review declares a reviewer and the flow opens a PR; its host is asked under a 5 second timeout, past which it reads `unknown (timed out)` and the rest still prints.
- **Judge** reads `pending`, `running`, or its outcome and reason; a judge or review half the flow does not declare shows once one ran, and otherwise the row reads `not in this flow`.
  The **task** judge, the one fetch runs on a task older than `stale_after` (and any verdict whose subject is the task), is in no flow: it leads the gates once it ran or while the plan phase is on it (`running`, `blocked`), and shows nowhere before that.
- **Outcomes** come from lore (`docs/lore.md`): findings by `source`, verdicts by `judged`, when lore is at least as new as the files and holds the latest round's findings. Otherwise, a missed lore write or a run from before lore, the files answer: each `reviews/<id>/<round>.md` counted by its verdict lines (`fix`, `skip`, and open for one still waiting), each block for the half its `source:` names, else the header's: remote when `reviewer:` names only a remote identity, so a file naming both (`local, claude[bot]`) counts once for each half; the newest `verdicts/<id>/<n>.md`, the task or plan judge's when its `subject:` is the task or a plan.

A parent's rows carry the same gates as one column after the step, `L 3/4 · R open · J proceed`: `L` and `R` the review halves (fixed of found, `N open`, `clean`, `ok`), `TJ`, `PJ` and `J` the task, plan and change judges, `-` not in the flow, `…` running, `?` unknown; `--markdown` adds it as a `gates` column.
`--json` holds them in a `gates` array, on a run and on a row.
`--json` prints the rows as an array: `id`, `title`, `url`, `depth`, `after`, `stage`, `step`, `pr`, `pr_state`, and `note`, `next`, `ask` where set.

A task with no children reports as before: its live run, else its plan and PR.
A task with no branch left (`f10 finish` removed it) prints its single `done` row when the ledger, the tracker or the plan archive says it finished, and `--json` a one-row array; otherwise it fails as before, with `no run to read`.
A task whose checkout has its branch is read from there first, so only a task with no run to read asks the host for children.

## Drive

`f10 drive 42 43` runs tasks through a flow from the main session.
Each task waits for its one base to have a commit, every task released that way runs at once, and the driver exits when every task is through, one needs a human, or nothing else can move.
The driver is this binary rather than a Claude session: ordering, prompting and polling are deterministic, and a session polling an agent pays a round trip and a slice of context per check.

1. **The arguments.**
   Task ids first, or a range `42 -- 47` (every id between, inclusive, at most 50).
   The flow is `default`, or the one `--flow <name>` or `--local` names; an undeclared name fails with the `f10 flow` list.
   It gives the chain, which is never typed: `plan` when the flow leads with it, then `ship`, which runs every other entry, then `finish` when the frontmatter's `drive_finish` is on.
   A chain skill typed after the ids is refused, naming `--flow` and `drive_finish`.
   `--flow`, `--local`, `--model`, `--answer` and `--every` go anywhere.
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
   It is the same check `f10 start` makes.
3. **The graph.**
   Each task has at most one base: the `branch.<name>.f10-after` its branch recorded once started, else the `After:` line in its body.
   Where the two disagree, the branch wins, with a note.
   The map is rebuilt on every run, and nothing about it is stored.
   A base outside the list that is not finished is refused, and so are tasks that wait on each other in a circle, both before anything is created.
   Within the list the graph sets the order: a base runs first wherever it is listed, and among tasks ready at once the one listed first goes first.
4. **Per task.**
   A task starts once its base releases it: the base's branch has a commit of its own, or the base is through (finished, or its whole chain run).
   Its own commits are what its branch has beyond the default branch, `origin/<default>`, its own base's branch, and the commit it was created at (its oldest reflog entry), so a lagging local default branch or a squash-merged grandparent never releases it early.
   Implement never commits, so a first commit means a commit or pr step ran with verify green; the driver reads it from local refs on each poll, so a branch never pushed counts.
   A dependent's own `finish` still waits until its base is through, so a dependent never merges before its base; while it waits its row reads `waiting for <id>`.
   Every task that is ready starts in the same pass, since each agent is its own session.
   The worktree, workspace and agent are opened as `f10 start` opens them, with the body's `After:` as the base: a base still on its branch stacks the new one on it, and a finished base leaves it on the default branch.
   A plan the main checkout saved for the task is read in place under start's rules (a worktree's own plan, from before storage moved to main's, wins).
   A checkout Herdr already shows, as an earlier `f10 start` leaves it, is adopted: its workspace, its agent, or a new agent in its idle shell.
   Run state recorded before the driver started that new agent was written by one that is gone, and halts nothing.
   From then on the task's workspace is the one opened, by id, whatever else shows the same checkout.
   Nothing is prompted at open.
   The chain runs from the task's position: the last skill the driver saw finish, recorded on the branch as `branch.<name>.f10-drive` in local git config, else what the task's run state proves (plan done, ship done).
   A plan the worktree reads when the driver starts its agent counts as plan done, so the task goes straight to ship rather than planning again.
   A task whose branch already records a flow (`branch.<name>.f10-flow`) keeps it, with a note when the run named another; a recorded flow `project.md` no longer declares gives way to the run's.
   `finish` runs in the driver, exactly as `f10 finish` would from the main checkout.
   A PR someone else opened is the driver's own ask at finish, `[local | merge]`, answered with `--answer` like any other.
   Every other skill is prompted to the agent, marked `--driven` (which takes every gap on its default and reuses a saved plan): a dependent task's plan and ship name the base task and its plan, and ship names its flow unless it is `default`.
5. **Catchup when the base moves, after finish, and before it.**
   On each poll, a running dependent whose branch lacks its base branch's head, read while the base agent is not working, is marked `branch.<name>.f10-base-moved` with that head; a dependent mid-catchup is not marked.
   The catchup merges the local base branch, whatever `catchup:` says, so no push is needed.
   `finish` marks each branch that depended on the finished task `f10-landed`.
   Before a marked task's next skill, the driver sends it `/f10:catchup --driven`; the driver never interrupts a turn.
   Before a task's own `finish`, the driver fetches and tries origin's copy of the branch against `origin/<default>` in memory (`git merge-tree`), as the host will; a branch origin does not have is tried against the local default branch, where finish merges it.
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
   - **blocked without one** (a judge stop, a wait on a base with no code), **failed** or **partial** - the task halts with the run's note and next, and so does every task that waits on it: one not started yet never starts, and one already running halts at its next chain boundary, its work left on its branch.
     Every other task runs on, and the driver exits **5** once nothing can move;
   - **still running** under plan or ship, a minute after the agent went idle - the agent stopped without reporting, usually a permission dialog in its pane; the task halts naming the workspace.
     Found at prompt time on an agent the driver did not start, the same halt also says how to leave a cancelled run: quit that agent and rerun, and a new one is started in its place;
   - anything else - the skill is done, recorded on the branch, and the next one is sent.
7. **The report.**
   Progress lines as it goes, an end included: a halt or an ask is printed the moment the driver sees it, not only beneath the report.
   Then one row per task in graph order: `already finished`, `finished`, `done: <chain>`, `running <skill>` (still working when the driver exited on an ask), `waiting for <id>` (its finish waits on its base), `asked at <skill>`, `halted at <skill>`, `halted: base <id> halted`, `not reached`.
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
Nothing declared, or a fact outside the vocabulary, is a refusal naming the line to fix; `pick` says `the trigger is manual (<how>)` when it finds nothing on a manual project, and whether `trigger` can perform it.

- `pick` writes the newest review by the reviewer that nobody handled: a title, a meta line (url, the sha it reviewed, posted at, done, handled, settled, its revision), the body verbatim.
  `--json` emits the struct.
  `--latest` takes the newest review whatever its state, for `/f10:resolve ci` to tell a settled one from one to read in.
  A settled review counts as handled, so plain `pick` never returns it.
  The revision fingerprints the reviewer's text without f10's own marks: a sticky comment rewritten by a re-review keeps its id and sha, but not its revision.
  An issue comment stores no sha, so its sha is the request's last commit before it was posted; an inline review carries its own, a check run its head.
  `stale` marks a review whose sha is not the request's head: commits landed after it (an issue comment is judged at its last edit, since a re-review rewrites the sticky comment).
  `triggerable` says `trigger` can perform the declared manual trigger.
- `wait` polls `pick` every `--every` until the review is done, and gives up at `--budget` (10 minutes by default) with the last reason quoted.
  `--since <RFC3339>` counts only a review posted or updated after that time, so an older review left unhandled is not taken for the one a trigger asked for.
  It returns within the budget whatever the host does, so one agent call covers it.
- `ack` leaves the declared handled marker: a reaction, a marker appended to the text, the first checkbox ticked, or the inline review's threads resolved.
  It, `settle` and `trigger` are the verbs here that write to the host.
  A review already handled is confirmed, not marked twice; a check run has no marker and ack says so.
  A reaction counts as handled only when it is newer than the review's last edit, so a sticky comment re-edited by a re-review reads as unhandled again.
- `trigger` performs the declared manual trigger: a how reading `comment \`<text>\`` posts the text as a comment on the request, `request a review from \`<login>\`` requests that review; the login must sit in a code span, so a word guessed from prose never reaches a real account.
  It prints the host's time for the comment (the local clock for a request), what it did and the url; the time is what `wait --since` takes.
  An automatic-only trigger, or a how in neither shape, is a refusal naming the Trigger line, and nothing is posted.
- `settle` leaves the declared settled marker on the review `--id` names: a reaction or a marker.
  It refuses when the review's revision is no longer `--revision`: a re-review rewrote it, and its findings are unread.
  Already settled is confirmed; a check run has no marker; no `settled` declared is a refusal naming the fact.

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
  The base `f10 start --after` recorded wins over the host's, which the base's finish retargets to the default branch.
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

## Board

`f10 board` has two views.
The default is **the feed**: what changed in a calendar window, one row per request or task in one of five groups, `started`, `updated`, `reviewed`, `finished` and `new tasks`, and one line counting the open requests with no commits, reviews or status changes in the window.
`--state` is where every task of the project stands: done lately, in progress at which stage and who owes the next move, and what comes next.

**The window** is the argument (`f10 board yesterday`) or `--since`, never both: `today`, `yesterday`, `last-workday`, `this-week`, `last-week`, a `YYYY-MM-DD` date (that day), or an RFC 3339 time (from then until now).
[years](https://github.com/amberpixels/years) reads each one; weeks start on Monday, and working days are the `workdays` in `~/.f10/standup.md` (Monday to Friday by default), read only by the default and `last-workday`.
With no window the feed shows **today**, or the last working day while today has no activity yet, so a Monday-morning run shows Friday; the header names the span and says when it fell back.
Over a scope every project shares the span.
With `--state` the window sets where `done` rows start, seven days back without one.

On GitHub the feed asks the host once per repository: one GraphQL search for the requests updated since the window's start, each with its newest 100 timeline events since then (commits with their parent count, force pushes, reviews, ready for review, review requests, merges, closes), paged as needed, and the count of open requests nobody touched.
On GitLab it lists the merge requests updated since and the open ones nobody touched, then reads the commits, notes and versions of the touched ones alone, eight at a time.
The earlier requests of the tasks a request started in the window are read to tell a follow-up from a first request: one more GraphQL call on GitHub, searching titles and head branches; one list per task on GitLab, whose search reads titles only.
`docs/board.md` holds the groups' rules.

Each view reads three sources, each failing on its own: the host (the feed's activity; for `--state`, open requests and the ones merged in the window, their commits, reviews, checks and mergeable state), the tracker (open tasks through the driver's `list`, else the host's issues, up to 100), and this machine (for `--state`: live runs, worktrees, branches, plans, findings, verdicts, the ledger, Herdr's workspaces; the feed skips it).
A failed source is printed first with its error, and every other row still renders.
A checkout with no origin remote is **local only**: no host, no tracker but a driver, and the line says `local only` instead of two failures; its feed is its branches committed in the window.
It writes nothing to the host.

**The scope** is this checkout, or the union of `-C <path | name>` (repeatable), `--group <name>` (repeatable, a group in `~/.f10/board.md`) and `--all` (every checkout under `F10_ROOTS`, with it unset an error, plus the `repos` the file watches), as `f10 standup` takes them.
Each project is collected on its own, a few at a time: one that cannot be read (a `project.md` that does not parse, a host CLI that errors) shows every source failed with the error, and every other project renders.
Checkouts of one repository are read once, from the first given; a repository named outright that a checkout already covers is dropped.

- `--all` or `--group` prints one line per project. The feed's line counts its rows per group and the quiet requests, under one header naming the shared span; `--state`'s is `<n> in flight` (rows under `in progress`), `<m> waiting on you` (those whose next action is a command: an ask to answer, a review to resolve, a merge to finish, a plan to ship), `oldest <key> <age>` (the in-flight row with the earliest `last_activity`), then any failed source; `nothing in flight` where that is so, and `failed` with the error for a project nothing could read.
- `--detail`, or a scope of more than one `-C` with no `--all` or `--group`, prints every project's rows under a heading with its name; one `-C` prints the board as this checkout's.
- `--by person` prints rows grouped by `who.person` across projects, a heading per person, the unresolved last under `nobody`, each row naming its project.
- `--json` over several projects (`--all`, `--group`, more than one `-C`) prints a JSON array of Board documents, one per project in scope order, each the version 4 document `docs/board.md` specifies; one project, this checkout or one `-C`, prints one document. A server run every few minutes reads each board's `sources[].took_ms` for the cost of a run.

**Your file**, `~/.f10/board.md`, YAML frontmatter then free prose, an unknown key an error:

- `groups` maps a name to what `--group <name>` reads: absolute paths or globs (`~/` expanded) as `standup.md` has them, and repositories with no checkout written as `host/owner/repo` (`github.com/acme/widgets`).
  Each file owns its `groups`; the board never reads `standup.md`'s.
- `repos` lists repositories watched with no checkout, read with `--all`: a string `github.com/acme/widgets`, or `{repo: ..., prefix: WID}` where the name derives the wrong task id prefix.
  `github.com` is read with `gh`, `gitlab.com` with `glab`; another host says which with a `gh:` or `glab:` prefix (`glab:git.example.com/acme/widgets`), and a GitLab host off gitlab.com needs `GITLAB_HOST` for its API calls.
  Such a repository renders from the host and its issues alone, its local source skipped, as `--remote` reads.
- `people` maps one person to the identities the sources know them by: `{name: ann, tracker: Ann Lee, host: annl, emails: [ann@acme.example]}`, every key but `name` optional.
  A row's `who.person` is the tracker assignee's person, else the request author's, else the first committer's; without a map, or with none matching, it is that identity as the source spelled it, so a tracker name and a host login stay two names until the map joins them.
  Where the tracker is the host's issues the assignee is a login, which `host` claims as well, so `tracker` is for a tracker of its own.

  ```yaml
  ---
  groups:
    work: [~/code/acme/*, github.com/acme/infra]
  repos: [github.com/acme/widgets]
  people:
    - {name: ann, tracker: Ann Lee, host: annl, emails: [ann@acme.example]}
  ---
  ```

- With `--state` the host is asked once for the open requests and once for the merged ones, then once for every branch when a request merged in the window, once per open request for the declared remote reviewer's review, and once for the commits of a request nothing else names a task for.
- `--remote` reads the host and the tracker only: what a viewer without the checkout sees.
- `--json` prints the Board JSON, the contract every renderer reads; `docs/board.md` is its spec, the stages and their rules included, and `docs/board.schema.json` its schema.
- `--html` prints one static page: the Board JSON embedded, drawn by a renderer inlined in the page, so it opens from disk with no server and no network and can be attached or hosted as is.
  The page draws one project: over several (`--all`, `--group`, more than one `-C`) it is refused.
  The page shows the feed's span, groups and quiet line, or every row with its stage, `stage_from` and next action, and the failed sources; it draws the same rows from the same JSON as the terminal and decides no group or stage.
- `--out <path>` writes `--json` or `--html` to a file instead of stdout.

A feed row says what happened (`opened`, `3 commits, merged main`, `approved by bob`, `merged`, `abandoned`), who owes the next move where a review left one, and where the group came from: `host` for a merge, a close or an opening, `trace` for a commit's `Step:` trailer (`docs/traces.md`), `tracker` for a new task, `guessed` for the rest of the host's facts.
A task with several requests is one row, led by the latest opened, its summary naming how the others relate (`opened, follow-up to #80`, `merged (replaced #175)`, `merged in 2 PRs`) and its request column listing the parts (`#176 + #180`).
A state row's stage says where the task is (`asks-waiting`, `blocked`, `ready`, `in-review`, `pr-open`, `implementing`, `planned`, `merged-unfinished` (merged, its worktree still on disk), `next`, `done`) and `stage_from` where the evidence came from: `local` for this machine's state, `trace` for the traces the project left on the host, `tracker` for a `next` row, `guessed` for the host's own facts alone.
On a repository nothing ever traced, read with `--remote`, every in-progress row is `guessed`.
The next action is a command where one exists (`f10 finish FT-71`, `/f10:resolve FT-71`), else a sentence.
Rows draw under `in progress`, `next` and `done`; a task waiting only on its unmerged base, with nothing of its own started, stays under `next`, after the tasks that can start now.
Inside a session, `/f10:board` runs the command with the same flags and prints its output; with `--state` it offers the rows whose next action is a command: the in-progress ones one per line, the `next` group as one line for its first task (`f10 start <id> and <N> more`), running none.

## Stats

`f10 stats` is how long planning and implementing took, per task of the current project, newest first, then a median row.

- **plan**: active minutes. A `/f10:plan` run counts from its start to the plan it saves; inside `/f10:ship`, the plan step counts from entering it to entering the next step.
- **implement**: active minutes of the `implement` step, summed over its stretches (a driven run blocked on an ask and resumed is two).
- **size**: lines added plus deleted, with the markdown share, as `f10 finish` measured it.
- **density**: implement minutes per 100 changed lines.

Active means the window minus every wait on the user inside it: a turn ended and not yet answered, an `AskUserQuestion` open, a permission dialog open.
The hook records each as `wait_started` and `wait_ended` in the ledger.
Tool run time (tests, lint) stays in, except for a tool that needed a permission dialog: no hook marks the approval, so its whole run counts as waiting.
Step boundaries are the `f10-state.sh set ship running <entry>` calls; a skipped one counts the next step's time as the step before it.
A value never recorded prints `-`, never `0`, and no wall duration is printed.
Counting starts with the release that records waits: a session with no wait in the ledger, and a window a wait with no recorded end runs into, count nothing.
`--since` takes days or weeks (`7d`, `2w`) and keeps the tasks active in that span; a task id shows that task whatever its age.

## Lore

`f10 lore` keeps a change's intent, decisions, findings, verdicts and lessons as entry files and answers why a line is the way it is.
[docs/lore.md](lore.md) is the spec: the schema per kind, the rules, every verb and an example change.
The root is `.f10/lore` in the main checkout, shared by its worktrees (`~/.f10/<project>/lore` with out-of-tree storage), or `.lore/` at the checkout root with `lore: committed` in `project.md`; `--root <dir>` overrides both.

## References

A reference is a bare number, a `#123`, a prefixed id in any case, or a url whose last path segment is one of those (`.../issues/42`, `.../browse/ABC-1042`).
With no id, a reference is looked up in one cascade: the id you passed, else the id in the current branch name, else the task this session recorded.
The branch lookup needs the project's task id format, declared in `project.md` or derived from the project's name.

`-C` answers for another checkout.
It names one checkout; only `f10 standup` and `f10 board` take it more than once.
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
Into a pipe, `read` also downloads every image the task links (markdown and HTML image syntax, and bare image urls, in the body and the comments) into `<storage root>/images/<ID>/`, replaced on each read, and ends with a line naming each local path; an image that fails is named with its url and the reason.
At most 20 images per read, never from a loopback or link-local address.
In a session, `/f10:read <id>` runs it, opens every image it downloaded, and stops without planning or starting anything.
On GitHub, requests to GitHub's own hosts carry the `gh` token, so a private repo's attachments download.
`f10 finish` removes the task's images from the shared root and from main's `.f10/`; a worktree's go with the worktree.
`--no-images` keeps the links as they are, for a read that only wants the title.
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

## Create

`f10 task create` creates a task through the driver's `create` verb ([driver contract](driver-contract.md#creating-a-task)).
It reads the task as markdown on stdin, `# <title>` on the first line, and prints the new id in the project's id format.
The body's `After: <id>` line reaches the driver as a link to the base task, `After: [<id>: <title>](<url>)`, titled as `f10 task ref` would; a base it cannot title stays bare.
`--status`, `--priority` (one of `urgent`, `high`, `medium`, `low`), `--label` (repeatable) and `--parent` go to the driver as flags, with the body's `After:` line as `--after`; an unset property passes no flag.
It exits **3** when no driver creates tasks here (none, or one declining `create`): the capture step then uses the project's Create adapter.
A failed create is never retried, and its error says the task may exist already.
With `tracker.title` declared on the host's issues, the new issue's title is then written through it; a failure there is a warning naming `f10 task retitle`, since the task exists.

## Ref

`f10 task ref FT-109 FT-132` prints one line per task, `FT-109 (<title>)`; with `--md`, `[FT-109: <title>](<url>)`, brackets in the title escaped.
This is the form text that leaves the machine uses to name another task.
Titles and urls come from the ledger (`~/.f10/f10.db`), which `task read`, `task create`, `task search`, `task open --print`, `task children`, `board` and `status` fill as they read the tracker.
A task not there costs one tracker call (the host's issue, or the driver's `read` and `url`), whose answer is kept.
`--fresh` asks the tracker for every task and rewrites what is kept.
A title renamed on the tracker stays stale until f10 reads that task again.
An id the tracker does not know, or a tracker that fails, prints bare with a note on stderr, and the exit is 0.

## Comment and update

`f10 task comment` posts markdown on stdin as a comment on the task, through the driver's `comment` verb, else the host's issues (`gh issue comment`, `glab issue note`), and prints the comment's url when one comes back.
`f10 task update` sets the task's `--status`, `--priority`, `--label` (repeatable, adds), `--assignee` (replaces) and `--set key=value` (a property the driver knows), through the driver's `update` verb, else the host's issues, where a status is `open` or `closed`, a priority is the label `tracker.priority` maps it to, and `--set` is refused ([driver contract](driver-contract.md#commenting-on-and-updating-a-task)).
Both exit **3** on a tracker that is not the host's when no driver takes the verb, and a ship run then reports its shipment locally only.
A failed comment is never retried, and its error says it may have posted.
On the host's issues with `tracker.title` declared, an update also puts the prefix back on a title renamed by hand.

## Retitle

`f10 task retitle [id]` writes `tracker.title` ([title template](project-setup.md#title-template)) onto the task's title; `--all` does it for every issue, open and closed, printing one line per change, `<id>  <old>  ->  <new>`.
A title already written through the template is left alone, so a second run changes nothing; `--dry-run` prints the changes and writes nothing.
With no template declared it says so and writes nothing; it exits **3** on a tracker that is not the host's, since a driver has no title verb.
`--all` stops at the first failed edit, naming how many it changed before.
Reads strip the prefix back off: `task read`, `search`, `children`, the board, status and a default branch name show the title once.

## Trackers

The host's issues need nothing declared: the git remote says GitHub or GitLab, and `gh` or `glab` does the rest.
Anything else goes through an executable at `.f10/driver`, per [the driver contract](driver-contract.md).
A driver that declines a verb falls back to the host's issues.

Task ids take the prefix `project.md` declares, else one derived from the main checkout's name: a short name with a digit as is (`f10` → `F10`, `r3` → `R3`), initials across hyphens, underscores or dots (`git-undo` → `GU`, `notion-sdk-go` → `NSG`), the first three characters of one word (`herdr` → `HER`).
The prefix names plan files and is how a branch is searched for its task, so it must not move once used: `f10 init` writes it into `project.md`, and `f10 config` shows which it is and where it came from.
