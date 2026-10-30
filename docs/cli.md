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
f10 init --migrate       # move the facts a project.md declares in prose into its frontmatter (adding only what it lacks), and old keys into flows
f10 config [verify] [--json]   # the effective configuration, with the origin of every value; verify: the one verify line the loader prints
f10 agents [<step>...] [--kind KIND] [--agent PROFILE] [--json]   # the merged agents map with each value's origin, or each step's resolved agent profile and what runs on it
f10 flow [--json]        # the declared flows, each with what selects it, and drive_finish
f10 completion <shell>   # the completion script for bash, zsh, fish or pwsh; --flow completes the declared flows
f10 help [--all | --agent]   # the human commands; --all adds the plumbing the steps call; --agent prints one line per command for an agent's context
f10 doctor [--json]      # check the plugin, the binary and the environment; changes nothing

f10 task read [id]       # the task as markdown; a bundle (id+id) reads every member
f10 task current         # the id a bare skill call stands for: the branch's task, else this session's (exit 1: none)
f10 task ref <id>... [--md] [--fresh]   # `<id> (<title>)` per task, a markdown link with --md; from what f10 already read
f10 task report <id>...  # a report's facts block: one task's rows, else a row per task
f10 task stale [id] [--created YYYY-MM-DD]   # fetch's stale check: fresh, judged <verdict file>, needs judge or undated
f10 task open [id]       # in the browser, no id the task list (--link writes the url, --print the item or the list's first page, ranked by priority)
f10 task search <query>  # rows, not a picker
f10 task children [id] [--json | --plain] [--open]   # the task's open subtasks, in After: order
f10 task create [--status s] [--priority p] [--label l]... [--parent id] < task.md   # through the driver, else as a host issue; prints the new id
f10 task comment [id] < comment.md   # a comment on the task; prints its url when the tracker gives one
f10 task update [id] [--status s] [--priority p] [--label l]... [--assignee a] [--set k=v]...   # absolute values
f10 task retitle [id | --all] [--dry-run]   # write tracker.title onto the host's issue titles
f10 plan read [id]       # the plan saved for that task
f10 plan open [id]       # in your editor (--link writes the path)
f10 demo open [id]       # the demo report, in the browser (--link writes the path)
f10 pr open [n | id]     # the PR or MR: a number is one, a task id its open one, else its last; none, their list; gh or glab decided by the remote (--link, --print as for task)
f10 pr base [PR | id] [--origin]      # the base: the open PR's target, else the recorded stacked base, else the default branch
f10 pr diff [PR | id] [--name-only]   # the change against that base: here with uncommitted work, else the open PR's diff, else the branch
f10 pr view [PR | id] [--comments]    # branch, base, task and open PR (head sha, target, mine) with its description; says so when there is no PR
f10 pr create --title T --body-file F [--label L]... [--assignee A]... [--draft]   # open a PR for the branch here against pr base; prints its url, or an open one's
f10 pr edit [PR | id] [--title T] [--body-file F]   # rewrite the open PR's title or body; base, labels and assignees stay
f10 pr comment [PR | id] [--reply-to URL [--quote T]] < text    # comment, or reply in a review's thread; prints the url, never posts the same text twice
f10 pr status [PR | id] --name N --state S -m TEXT [--sha SHA]   # a commit status on the open PR's head; one set the same way is left alone
f10 pr catchup [--guard implement|pr|judge] [--no-fetch]   # what a catchup of the current branch brings in: base, how found, method and why, the conflict mark
f10 review pick [n] [--latest] [--json]               # the latest unhandled remote review, as markdown
f10 review wait [n] [--budget 10m] [--every 20s]      # poll it to done within a budget
f10 review ack [n] [--id <id>]                        # leave the declared handled marker on it, or on the review --id names
f10 review settle [n] --id <id> --revision <rev>      # leave the declared settled marker, unless it was rewritten
f10 review round [task-id] [--json]                   # the findings directory, its latest round and the next, or the decline (exit 3)
f10 trace trailers --step <step> [--task <id>] [--round n --fixed n --skipped n]   # the Task:, Step: and Review: lines a step's commit ends in; nothing with traces off
f10 trace state --step <step> [--round n] [--open n] [--asks n] [--task <id>] [--body-file <file>]   # rewrite the hidden state block in the branch's open PR body, or put it into a drafted body; nothing with traces off
f10 status [id] [--all] [--json | --plain]   # where the run is: a task's in its worktree (bare: the session's task, else the branch's, with that output's --json shape; in the main checkout, a `launched here` tree of what the session started or drove that is still live, nested by `After:` as a parent's children are, task rows under --json), else this session's, every live one, or a row per child of a parent
f10 standup [--since <time> | --week] [-C <checkout>]... [--group <name>]... [--all] [--json]   # your commits and PRs since your last standup, grouped by project and task
f10 standup mark --since <time> [--at <time>]      # record a standup as reported: the next one starts where it ended
f10 board [<window> | --since <window>] [--state] [--remote] [-C <checkout>]... [--group <name>]... [--all] [--detail] [--by person] [--json | --html] [--out <path>]   # what changed today (else the last working day); --state: where every task stands; one project or several
f10 stats [--since 30d] [<id>]   # active minutes per task by step group, with size and density; a median row; an id adds its steps

f10 start <id>[-suffix] | <id>+<id>... [--plan | --local | --flow <name> | --discuss] [--after <id> | --base <branch>] [--agent <profile> | --model <model>] [note...]   # a worktree, a Herdr workspace, a prompted agent
f10 forward <id>[-suffix] <text...>   # hand a command or a steering note to the agent working on the task
f10 drive <id | id+id>[:profile]... | <id> -- <id> [--flow <name> | --local] [--agent <profile> | --model <model>] [--parallel <n>] [--answer [<id>=]<text>] [--every 30s] [--foreground]   # a list through a flow, each task once its base has a commit; runs in the daemon
f10 daemon status | stop [--now] | logs [<drive>] [-f]   # the per-user process drives run in
f10 asks [list [--json] | answer <task> [N=VALUE ...]]   # the questions driven agents wait on, across projects; bare walks them in a terminal
f10 finish [<id>[-suffix]] [--yes] [--close <ids|all|none>] [--keep]   # no id: the branch's task; merge the PR (a branch never pushed: here), pull main, archive the plan, close the workspace, remove the worktree; a spike with no PR closes without a merge
f10 janitor [--dry-run]  # remove task branches whose PR merged or closed outside finish, locally and on origin; start and finish run it every 6 hours

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

## Agents

`f10 agents` prints the merged agents map, `~/.f10/agents.md` under the project's `agents:`, one key per line with its value and origin.
`f10 agents <step>...` prints each step's resolved profile: `review: claude:opus:high  (model: default (declared (user, ~/.f10/agents.md)), effort: review (declared (main, frontmatter)))  Agent tool: model opus, effort high  - the blind reviewer, a subagent`.
A step scoped by task kind (`plan.bug`) prints a line per kind unless `--kind` picks one.
`--agent <profile>` lays a run's profile over `default` as `f10 start --agent` does, and `--json` emits the same lines.
`f10-load-project.sh <step>...` prints this for the steps it loads, which is where a spawning step reads the Agent tool form.
A key or a profile the vocabulary refuses fails the command naming the file, as a `project.md` fact does.

**Your file**, `~/.f10/agents.md`, YAML frontmatter then free prose, holds the `agents:` map every project starts from (`docs/project-setup.md` → Agents).
`F10_USER_DIR` names another directory than `~/.f10` for it and the other user files, which the eval runner uses to keep a measured run off your own map.

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
- `--agent <profile>` starts the agent on that profile, `kind:model:effort` (`claude:opus:high`), `model:effort` or either alone, laid over the `default` the agents map resolves (your `~/.f10/agents.md`, then the project's `agents:`; `docs/project-setup.md` → Agents).
  The kind's flags go on the agent's command line (`--model`, `--effort`).
  The profile is recorded on the branch as `branch.<name>.f10-agent`, so a rerun on the branch starts its agent on it again unless a flag names another.
  The report gains an `agent` row when the profile is not the kind's default.
  `--model <model>` is the model-only form, exclusive with `--agent`, and reads a model only: a lone effort word is refused.
  The model is passed to claude as `--model`.
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
  A PR from a fork, `--local`, `--after` and `--base` are refused before anything is created.
  A **merged or closed** PR is picked up too, for a post-merge review.
  Its branch is usually gone from the host, so the worktree is created at the commit it landed as: the merge or squash commit, or the head of a PR closed unmerged, fetched through `refs/pull/<n>/head` or `refs/merge-requests/<n>/head`.
  The branch is local, named for the task as a task start names it, and tracks nothing: origin's branches are not looked at, so a stale copy of the PR's branch there is left alone, and a branch the task already has here is reused as it is, at its own tip.
  A branch this start made is handed back from the watcher at once (`branch.<name>.f10-watch=false`, **Watch** below), so the merge is never reported as news; `f10 watch on <id>` undoes that, and a reused branch keeps its watch.
  The agent is told the PR's state (merged by whom and when, or closed without a merge) before the diff, and the report says the branch is local only.
  `--plan` is refused: going on from a landed PR is a task of its own, started by its id.
  `/f10:finish --local` removes the worktree.
  A landed commit origin does not hold after the fetch (its base branch deleted since) fails the start, naming the sha
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
Before implement, where a base is recorded on the branch, ship's catchup guard brings the base in by the method `f10 pr catchup` prints, unless `catchup: off`: `merge`, `rebase`, or `onto` for a base that was squash-merged or rebased, which replays only the branch's own commits. A recorded base whose branch has no code yet gets one question instead, wait or proceed on the plan's assumption, even under `catchup: off`; a driven run waits.
A conflict there stops ship, marks the branch for the base that conflicted (`f10-landed`, or `f10-base-moved` for a recorded base), and hands off to `/f10:catchup`, which settles it with both sides kept.
Ship also catches up with the base, or the default branch when none is recorded, before every `pr` entry and a change judge, unless `catchup: off`; once the branch is on origin these are merges, so the push never forces.
The pr step opens the PR or MR against the base branch, so it is stacked and the base's finish retargets it to the default branch; a base not yet on origin stops that step rather than pushing another task's branch.
`f10 finish` on the base task clears the record from every dependent, since the base's code is in the default branch from then on.
It also records the base commit each dependent was built on, so the dependent's catchup drops the base's pre-squash commits (`f10 pr catchup`, `method: onto`).

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
The notes also carry one line on the change's lore (`docs/lore.md`): its intent, decisions and lessons counted, each named when missing, so a change that reached finish without a ship run shows it left none.
That checkout is a worktree start created, or the main checkout itself for a task shipped from main with no worktree.
A finish that stopped halfway is finished by a rerun: a task with no branch whose plan is in the archive reports `already finished` and exits 0, and a branch whose worktree is gone but whose PR merged is cleaned up without one (dependents released, default branch pulled, plan archived, only the branch deleted, no workspace closed).
A branch with no worktree and no merged PR is still refused.
There finish merges as usual, brings the default branch up to the merge, switches main to it, deletes the local branch, and leaves main's workspace open; the report says the checkout was main's and was switched, not removed.

1. **Refusals, before anything changes.**
   A task a drive holds (its lease, **Drive** below): `GH-12 is owned by drive 3 (pid 4242)`, with `f10 drive cancel 3` named.
   Finish takes the same lease for its own run, so two finishes of one task never run at once; drive's own finish runs under the lease drive already holds.
   A dirty worktree, named file by file, with no `--force`: commit or discard first.
   A cwd inside the worktree being removed, or a `HERDR_WORKSPACE_ID` naming its workspace, unless `--yes` was passed: the message says the command would close its own workspace.
   With the branch checked out in main, nothing closes, so this refusal does not apply.
   Outside Herdr, as start does.
2. **The merge.**
   `gh pr view <branch>` / `glab mr view <branch>` by the branch.
   Open: merged through `gh pr merge` / `glab mr merge`, never with `--admin`; when the host refuses (red CI, a required review, conflicts) its reason is the error and nothing has changed.
   Already merged: confirmed.
   Closed without a merge: a refusal.
   Right before an open request merges, or a branch never pushed lands (below), `f10 lore check <id>` runs on the task: under `lore_gate: warn`, the default, its reasons are a note under the report and the merge goes on; under `block` they are the error, with nothing changed.
   A passing check needs the intent, a judge verdict of proceed and no open blocker or major finding, so a flow with no `judge` entry stays on `warn`.
   A request already merged, `--local` and a spike's close merge nothing and are not checked; a lore root this clone lacks reads as `no entries`, which `block` refuses too.
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

**A spike.**
A task whose spike report (`<storage root>/spikes/<ID>.md`, or a legacy worktree's own `.f10/spikes/<ID>.md`) exists and whose branch has no PR, no copy on origin and no upstream is closed without a merge.
A plan without a `Kind: spike` line overrides the report: a report left from an earlier spike under the same id never closes a task planned again as something else.
Its worktree may hold the experiment uncommitted, so the dirty refusal of step 1 does not apply to it; in the main checkout, whose tree may hold anything, it still does: discard the experiment by the paths the report names first.
Nothing is merged, pulled or measured; dependents are released as in step 6; the plan is archived as in step 7 and the report beside it, as `plans/archive/<ID>.spike.md` (`<ID>.spike.<N>.md` when taken), which counts a spike planned with no plan file as finished too.
The report moves once the worktree is gone (a legacy worktree's own, before), so a rerun after a failed removal still closes the spike.
After the report the experiment is discarded (`git reset --hard`, `git clean -fd`, ignored files left), its paths listed in the notes, with a branch commit's recovery command when the branch had commits of its own; then the worktree, branch and workspace go as in step 8.
`--keep` keeps the experiment instead: in a linked worktree it is committed onto the branch (`--no-verify`) and the worktree is removed; on the main checkout, which is refused dirty, only the branch's own commits are kept. Either way the branch moves to `kept/<name>`, its name with the task ids taken out (in any case, dash or not), so no task lookup, the janitor or drive finds it and the task reads as finished.
`--keep` on a task with no spike report, or with a PR, is a refusal.
A spike branch with a PR finishes the normal way, clean tree required, and its report is archived with the plan.
Nothing closes the task on the tracker: the notes say so.

Run twice, finish changes nothing it already did: the PR reads as merged, the plan is already archived, there is no workspace to close, and only what is left is removed.

Out of scope: closing the tracker issue (the PR body does that with "Closes #n"), a pushed branch whose PR was never opened, a checkout with no origin, and worktrees f10 did not start.

## Janitor

`f10 janitor` clears the task branches that outlived their task: a PR merged in the host's UI or by `gh pr merge`, a worktree removed by `wt remove`.
`f10 finish` cleans up after itself; the janitor catches everything that went around it, without relying on a host setting such as GitHub's automatic head-branch deletion.
There is no schedule: `f10 start` after its report, `f10 finish` once its own cleanup is done and each watch pass under `watch: finish` (**Watch** below) run it when the last run is 6 hours old or more, `f10 janitor` runs it by hand, and `f10 janitor --dry-run` prints what it would remove and changes nothing.

It considers local branches and origin's (one `git ls-remote --heads origin`) tied to a task id by the rule finish reads a branch with: the `f10-task` link or recorded bundle, else the bundle the name spells, else the id it carries.
On origin that last rule counts only the shapes start creates (`<id>`, `<id>/<slug>`, `<id>-<suffix>`, `<id>+...`), so a colleague's `revert-<id>` is never read as a task branch.
Other branches and the default branch are never touched, and neither is a branch any checkout holds, main's included; those are dropped before the host is asked anything.
For each remaining branch the host lists its requests (`gh pr list --head` / the GitLab MR API by source branch), and the branch goes only when all of these hold:

- no request for it is open, and the newest one is merged, or closed without a merge, an abandoned task;
- its tip, locally and on origin, is the request's head sha, or has nothing beyond it that the default branch lacks by patch (`git cherry`, which skips merge commits), so a tip that merged main in, a follow-up that landed another way, or a tip behind a head a colleague pushed is clean, while a commit made after the last push keeps the branch, with the line naming it.
  A head the clone never fetched is fetched by sha first;
- no open request is based on it (`gh pr list --base` / `glab mr list --target-branch`), since deleting a stacked PR's base closes it on the host;
- for a closed request, no local branch was started `--after` it.

A branch with no request, or any branch when the checkout has no host CLI, goes only as finish would merge it as never pushed: no copy on origin, no upstream recorded, and its changes already in the local default branch.
A host that fails to answer for a branch keeps it, with a line.

Removal, per branch: a merged request's dependents are released as finish releases them; origin's copy is deleted through the host's API with its local tracking ref, but only for a request the signed-in user opened (someone else's keeps its origin branch, with a line, and one only on origin is not touched); then the local branch (`git branch -D`).
Once every branch of a task is gone, a bundle's riders included, its plan moves from `plans/<id>.md` to `plans/archive/` with finish's numbering, and its images are dropped.
A task with another branch left, such as a second attempt still in a worktree, keeps both, with a line; so does a plan written after the request merged or closed (for a branch never pushed, after its last commit), since the task was planned again.
A step failing keeps that branch, with a line, and the run goes on to the next.

The output is one line per removal and per branch kept for a reason worth knowing; a branch whose request is still open is kept without one.
`nothing to clear` when nothing was.
The last run is the mtime of `janitor.stamp` in the shared storage root (main's `.f10/`, or the out-of-tree root).
The stamp moves before the run, so starts in quick succession run it once; `--dry-run` leaves it alone.
Finish runs it before any workspace closes, since closing its own workspace ends the process; its lines go to stderr, as start's do.
`f10 drive` opens and finishes its tasks without the start and finish commands, so it never runs the janitor.

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

The answer path: `f10 status 42` reads the run the worktree's agent reports, its `ask` row included; `f10 asks answer 42 1=proceed 2="keep ours"` writes the answers through the asks inbox, which sends them as that agent's next prompt and closes the ask for every session.
`/f10:status 42` does both in one go, with the question put to you in between.

Out of scope: more than one task or more than one command per call, tailing the remote agent's output, and answering Claude Code permission prompts.

## Status

`f10 status 116` on a parent prints a tree: a heading with a count per stage, then a row per child, closed ones included, in `f10 task children` order, on box-drawing lines down each `After:` chain:

```
FT-116  2 done · 1 halted · 1 reviewing
├─ ✓ FT-117     done       finish  󰘭 #128 merged
│            trailers + traces switch
├─ · FT-118     reviewing  review  󰓂 #129
│  │         PR state block
│  └─ ✓ FT-120  done       finish  󰘭 #131 merged
│               look-around + f10 board
└─ ✗ FT-121     halted     ship
             sidebar icons
       halt  agent gh-121-1a2b3c in workspace w3 is idle while its run says ship running: implement
```

A row is a glyph for the stage (`✓` done or shipped, `✗` halted, `?` asking, `·` anything else), the id, the stage, the current or last step, and the PR with its state when it is not open, an open or merged PR's number after a Nerd Font icon (`F10_STATUS_GLYPHS` replaces the icons on a terminal without the font).
A task's run block gains an `agent` row, the profile its branch records (`opus:high`), when that is not the kind's default; `--json` carries it as `agent`.
The title goes on the line beneath, cut to fit; the ask, the halt reason and what unblocks it (`next`) follow as labelled lines.
A finished task alone prints as a one-row tree with no heading, every gate a labelled line.
Each row reads the child's live run in its worktree, else the ledger's last record of it, which outlives the worktree `f10 finish` removes; the PR comes from the host, by branch or by the url the ledger kept.
On a terminal the id and the PR are links (OSC 8).
`--plain` prints the same tree with no color and no urls, its gates as a `gates` line under the title; titles, halt and next lines are cut to `$COLUMNS`, else 100, and an ask is never cut.
A model that needs a url runs `f10 task open <id> --link` or `f10 pr open <n> --link`.
A skill relays it in a fenced block: relayed text shows an OSC 8 link's escape bytes, and a markdown table is reflowed at pane width.
It applies only to the tree; a run's block prints as without it.

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

- **The flow** is ship's entries as `f10 state pipeline` recorded them, else the entries after `plan` of the flow the branch records (`branch.<name>.f10-flow`), else the flow of the newest drive record holding the task that is neither cancelled nor done (a stopped drive's only while the task has no branch), else `default`, inside the chain `f10 drive` recorded on the branch (`branch.<name>.f10-chain`), which adds `finish`: a `judge` before `implement` is the **plan** judge, any later one the **change** judge, and `plan` and `resolve` are no rows of their own.
- **A plain step** is `done` before the step ship is on, that step takes the run's status (`running`, `blocked`, ...), the rest are `pending`; once ship is done, a step it never entered is `skipped`.
  A driven ship parked on its base (`f10 state wait`) reads `waiting on <id>` at that step, and the parent's row `waiting` with step `on <id>`.
- **Review** has a local and a remote half, each `pending`, `running` (its round, and `in ship`, `driven` or `manual`; `resolve running` while a resolve settles it, and in `review:local+ci` `round N in, waiting for the other half` once its own findings landed), `open` (`N open of M found`, or `open (not handled)` on the host), `settled` (`3 found (2 fixed, 1 skipped)`, `clean`), or `unknown`.
  A review, resolve or judge started outside ship counts from its `run_started` on the task's branch or checkout until its outcome lands, its session stops or starts another skill, or the state TTL passes; `ci` in its arguments makes it the remote half's, and inside ship the half reads `resolve running` once a round lands after ship entered the review.
  The remote half is in the flow when an entry names a remote review (`review:ci`, or both halves in one `review:local+ci`, which shows both halves on the one review row), or when Review declares a reviewer and the flow opens a PR; its host is asked under a 5 second timeout, past which it reads `unknown (timed out)` and the rest still prints.
- **Finish** in a chain `f10 drive` runs reads `pending` until ship is done, then `queued (drive)`, then `running (drive): <action>` while the driver's finish works (`merging #204`, `pulling main`, `removing worktree`), and `done`; a finish held for its base reads `queued (drive): waiting for <id>`.
  The driver records this on the branch as `branch.<name>.f10-finish`: `queued` or `running: <action>`.
  Whether the driver still runs is the task's lease (**Drive**, below), never the record: a record whose lease nobody holds is ignored, so a killed driver leaves the row `pending`.
  A parent's row puts the same text in its step column while the task is `shipped`, and `f10 board --state`, once the task's ship run is done, names it as the next move (`drive: merging #204`, `drive finishes it`) in place of `f10 finish`.
- **Judge** reads `pending`, `running`, or its outcome and reason; a judge or review half the flow does not declare shows once one ran, and otherwise the row reads `not in this flow`.
  The **task** judge, the one fetch runs on a task older than `stale_after` (and any verdict whose subject is the task), is in no flow: it leads the gates once it ran or while the plan phase is on it (`running`, `blocked`), and shows nowhere before that.
- **Outcomes** come from lore (`docs/lore.md`): findings by `source`, verdicts by `judged`, when lore is at least as new as the files and holds the latest round's findings. Otherwise, a missed lore write or a run from before lore, the files answer: each `reviews/<id>/<round>.md` counted by its verdict lines (`fix`, `skip`, and open for one still waiting), each block for the half its `source:` names, else the header's: remote when `reviewer:` names only a remote identity, so a file naming both (`local, claude[bot]`) counts once for each half; the newest `verdicts/<id>/<n>.md`, the task or plan judge's when its `subject:` is the task or a plan.

A parent's rows carry the same gates as one column after the step (a `gates` line under `--plain`), `󰚔 L 3/4 · R open · 󰊛 J proceed`: `L` and `R` the review halves (fixed of found, `N open`, `clean`, `ok`), `TJ`, `PJ` and `J` the task, plan and change judges, `-` a review half the flow does not run, `…` running, `?` unknown; a review or judge the flow leaves out is left out of the column.
The icons are Nerd Font `md-*` glyphs, the only range Claude Code's output keeps one cell wide: `󰚔` before the review halves, `󰊛` before each judge, and in the PR column `󰓂` before an open PR, `󰘭` before a merged one.
`F10_STATUS_GLYPHS` replaces all four, in that order (open PR, merged PR, review, judge), for a terminal without the font: `F10_STATUS_GLYPHS='PR MR rv jd'`.
`--json` holds them in a `gates` array, on a run and on a row.
`--json` prints the rows as an array: `id`, `title`, `url`, `depth`, `after`, `stage`, `step`, `pr`, `pr_state`, and `note`, `next`, `ask` where set.

A task with no children reports as before: its live run, else its plan and PR.
A task with no branch left (`f10 finish` removed it) prints its single `done` row when the ledger, the tracker or the plan archive says it finished, and `--json` a one-row array; otherwise it fails as before, with `no run to read`.
A task whose checkout has its branch is read from there first, so only a task with no run to read asks the host for children.

## Drive

`f10 drive 42 43` runs tasks through a flow from the main session.
Each task waits for its one base to have code, every task released that way runs at once, and the driver exits when every task is through, one needs a human, or nothing else can move.
The driver is this binary rather than a Claude session: ordering, prompting and polling are deterministic, and a session polling an agent pays a round trip and a slice of context per check.
The driver runs in the per-user [daemon](#daemon), not in the process that typed the command: that process prints the drive's log as it grows and exits with the drive's exit code, so closing or clearing the session that ran `/f10:drive` leaves the drive running.
Ctrl+C stops only the printing, naming `f10 daemon logs <n> -f` to follow it again; rerunning the same command follows the drive already running instead of starting a second one.
`--foreground` runs the driver in this process, as the daemon itself runs each drive; it is for tests and debugging, and on a platform without unix sockets it is how every drive runs.

**The record.**
Every drive writes `<storage root>/drives/<n>.yaml` and prints `drive <n>` first: the command line (without `--answer`, `--every`, `--parallel`, `--resume` and `--foreground`), the tasks with their `:profile`, the flow, the run-wide profile, `drive_finish`, the cap (`parallel`), the answers given and not yet taken, each answer taken (task, skill, text, time), and per task the skill in flight and its outcome (its report row and exit code).
Rerunning the same command line picks up the newest record started with it that is neither cancelled nor done (every task through with code 0); `f10 drive --resume <n>` picks up record `n` by number and takes only `--answer`, `--every` and `--parallel` beside it.
A rerun and a `--resume` keep the record's cap, unless they pass `--parallel`, which rewrites it.
A driver still running reads the new cap at its next poll.
An answer kept in the record, because the drive stopped before an agent took it, is given again on the resume, unless the resume gives that task another.
The branch's `f10-drive`, `f10-chain` and `f10-flow` keys stay what position and flow are read from; the record keeps what a branch cannot.

**The lease.**
Before anything opens, the driver takes an exclusive, non-blocking `flock` on `<storage root>/drives/locks/<id>.lock` for every task not already finished, its pid and drive number written into it, and one for itself (`drive-<n>.lock`).
The key is the task's lead id, so two attempts at one task share it.
A task whose lease another process holds ends at once with the row `owned by drive 3 (pid 4242)` (or `owned by f10 finish (pid …)`, `owned by the f10 watcher (pid …)`), and its dependents halt with it.
A task's lease is released when the task ends, so a halted task can be finished by hand while the drive runs on; the kernel releases every lease when the driver's process ends, however it ends.
A second drive on a record whose driver still runs is refused.
Readers (`f10 status`, `f10 board`) probe the lease with a shared lock for an instant; the driver retries a held lease for about 200ms to absorb that.
On a platform without `flock` nothing is exclusive.
Lock files are never deleted.

**List and cancel.**
`f10 drive list` prints the newest 20 drives (`--all` for every one), each with its time, `running (pid …)`, `stopped`, `done` or `cancelled`, its command line, and a row per task: its outcome, else `queued` or `running <skill>` while its driver runs, else `stopped at <skill>` or `not reached`.
`f10 drive cancel <n>` marks the record cancelled: a running driver stops at its next poll, after any finish it is in the middle of, ends every task still going with the row `cancelled` and exits **5**; nothing more is prompted, and worktrees, branches and agents stay as they are.
A cancelled record is never picked up again: the same command line starts a new one.

1. **The arguments.**
   Task ids first, or a range `42 -- 47` (every id between, inclusive, at most 50).
   The flow is `default`, or the one `--flow <name>` or `--local` names; an undeclared name fails with the `f10 flow` list.
   It gives the chain, which is never typed: `plan` when the flow leads with it, then `ship`, which runs every other entry, then `finish` unless the frontmatter's `drive_finish` is off.
   A task whose ship ended as a spike (its spike report written) is closed right after ship whatever `drive_finish` says, since the close merges nothing, and the chain recorded on its branch gains `finish`; a dependent then starts on the default branch.
   A chain skill typed after the ids is refused, naming `--flow` and `drive_finish`.
   `--flow`, `--local`, `--agent`, `--model`, `--parallel`, `--answer`, `--every`, `--resume` and `--foreground` go anywhere.
   The command parses its own arguments, so the range's `--` survives.
   `--agent` starts every agent on that profile, read as start reads it, and `--model` is its model-only form; `<id>:<profile>` starts one task's agent on its own, which wins (`42:sonnet:low 43 --agent opus`).
   The profile starts at the first colon with no slash after it, so a url keeps its own and `42:claude:opus:high` reads whole.
   A range takes the run-wide `--agent` only.
   Each profile is resolved once over the agents map, and any correction or `!!!` fallback is printed before the first task opens.
   The open line names a task's profile when it is not the kind's default, and a rerun keeps the one its branch records unless a flag names another.
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
   Its own commits are what its branch has beyond the default branch, `origin/<default>`, its own base's branch, and the commit it was created at (its oldest reflog entry), merges left out, so a lagging local default branch, a squash-merged grandparent or a catchup merge never releases it early.
   `f10 pr catchup` prints the same answer as its `code:` line, so ship's guard and the driver agree.
   Implement never commits, so a first commit means a commit or pr step ran with verify green; the driver reads it from local refs on each poll, so a branch never pushed counts.
   A dependent's own `finish` still waits until its base is through, so a dependent never merges before its base; while it waits its row reads `waiting for <id>`.
   Every task that is ready starts in the same pass, up to the cap.
   `--parallel <n>` caps the tasks running a skill at once, by default half the CPU cores, from 2 to 8.
   A task keeps its slot from one skill of its chain to the next.
   It frees the slot once it asks, waits for its base's first commit, or waits at finish for its base.
   Open agents are capped too, at twice the cap, because an idle session still holds its memory.
   The agent limit counts the tasks under way and the tasks asking, including agents an earlier run of the drive left open.
   A done or halted task's agent does not count, because the drive is finished with it.
   Counting it would also stall a drive with `drive_finish` off, whose done tasks keep their agents open, so no task past the limit would ever start.
   A task past either limit shows as `queued` in the report, `f10 drive list` and `f10 status`, and starts in the drive's order as slots free.
   A task whose agent an earlier run left open goes first, because it adds no session.
   A turn already running when the driver reaches the task is tracked and counted even past the cap, because the driver cannot stop it.
   The worktree, workspace and agent are opened as `f10 start` opens them, with the body's `After:` as the base: a base still on its branch stacks the new one on it, and a finished base leaves it on the default branch.
   A plan the main checkout saved for the task is read in place under start's rules (a worktree's own plan, from before storage moved to main's, wins).
   A checkout Herdr already shows, as an earlier `f10 start` leaves it, is adopted: its workspace, its agent, or a new agent in its idle shell.
   Run state recorded before the driver started that new agent was written by one that is gone, and halts nothing.
   From then on the task's workspace is the one opened, by id, whatever else shows the same checkout.
   Nothing is prompted at open.
   The chain runs from the task's position: the last skill the driver saw finish, recorded on the branch as `branch.<name>.f10-drive` in local git config, else what the task's run state proves (plan done, ship done).
   A plan the worktree reads when the driver starts its agent counts as plan done, so the task goes straight to ship rather than planning again.
   A task whose branch already records a flow (`branch.<name>.f10-flow`) keeps it, with a note when the run named another; a recorded flow `project.md` no longer declares gives way to the run's.
   `finish` runs in the driver, exactly as `f10 finish` would from the main checkout, and records what it is doing on the branch as `branch.<name>.f10-finish` for `f10 status` (above); the driver clears it on any other end, an interrupt included.
   A PR someone else opened is the driver's own ask at finish, `[local | merge]`, answered with `--answer` like any other.
   Every other skill is prompted to the agent, marked `--driven` (which takes every gap on its default and reuses a saved plan): a dependent task's plan and ship name the base task and its plan, ship names its flow unless it is `default`, and with `finish` in the chain ship's prompt says the driver finishes the task after it and removes the worktree, so the agent's report ends with that next step.
5. **Catchup when the base or the target moves, after finish, and before it.**
   On each poll, a running dependent whose branch lacks its base branch's head, read while the base agent is not working, is marked `branch.<name>.f10-base-moved` with that head; a dependent mid-catchup is not marked.
   Before a task's next skill, when its branch is on origin, the driver fetches once and marks the branch if its pull request's target moved past it, so an open PR never sits behind.
   The mark is `f10-landed` with the default branch's name when the PR targets it, `f10-base-moved` with the base's head for a base branch no task in the drive owns.
   A branch already marked, one its target is already in, or one never pushed is left alone, and a fetch that fails skips the poll.
   The catchup follows `catchup:`, or `catchup_stacked:` for a base branch.
   Once it ends clean the driver pushes a branch origin has, plain after a merge and `--force-with-lease` against origin's tip when it sent the catchup after a rebase or an `onto`, so the PR carries the integration before the next skill runs; a rejected push halts the task with the branch caught up locally.
   A branch origin moved on while the catchup ran is not pushed: the task halts, saying to bring `origin/<branch>` in and run `/f10:catchup` there, since the rebased branch lacks what was pushed meanwhile.
   A drive with no such record (resumed, or rerun after a halt) never force-pushes a caught-up branch, and halts instead.
   A catchup that brought in commits origin does not hold yet, a base merged from its local branch, is not pushed: the next `pr` entry or finish pushes it, so another task's unpushed work never rides into this PR.
   `finish` marks each branch that depended on the finished task `f10-landed`.
   Before a marked task's next skill, the driver sends it `/f10:catchup --driven`; the driver never interrupts a turn.
   Before a task's own `finish`, the driver fetches and tries origin's copy of the branch against `origin/<default>` in memory (`git merge-tree`), as the host will; a branch origin does not have is tried against the local default branch, where finish merges it.
   A conflict, as a sibling that merged first leaves one, marks the branch `f10-landed` with the default branch's name and sends the same catchup.
   Once it ends clean, the push above reaches origin and then finish merges.
   A branch that conflicts again after a catchup is judged by progress, not counted: the default branch moved since the catchup and other files conflict, it is caught up again; the catchup left the branch unchanged, the default branch has not moved, or the same files conflict twice in a row, the task halts naming the files.
   After a catchup the caught-up local branch is what is tried, since origin's copy is the tree before it.
   A ship stopped by its catchup guard, which marks the branch, gets the same catchup, then ship is prompted again at the entry it stopped on (`resume at <entry>`); its dependents wait, and a second guard conflict is judged by the same progress rule.
   The catchup's end is the `done catchup` signal its report writes into run state (`f10-state.sh done catchup`), dated after the prompt.
   An idle agent without it is a stall, halted after the same grace a ship or plan turn gets, never read as done.
   The chain resumes only on a clean end: the signal there, the mark cleared (catchup clears it once verify is green), no merge or rebase in progress, and a clean tree.
   A driven catchup never asks: it settles an ambiguous conflict by the task's intent and records each choice as a lore decision and a line under a Catchup heading in the PR body; a catchup that fails halts the task.
6. **Waiting.**
   It looks once a run file, prompt mark or open wait about one of its worktrees is written (a step boundary, a turn's end, a prompt taken), or its record is (a cancel), checked every second; other sessions' writes to the shared state dir do not wake it. It also looks and every `--every` (30s by default) regardless; each look reads two signals once for every task in flight: Herdr's workspace list, and the live runs, the newest one recorded against each worktree.
   A turn has begun once Herdr shows the agent working, the run reports after the prompt, or the agent's session left a prompt mark after it: the `UserPromptSubmit` hook writes `<session>.prompted` beside the run file, naming the checkout and the time, so a turn that began and ended between two looks still counts as taken once the mark is five seconds old.
   Herdr's `done` lasts from the previous turn until someone looks, so it proves nothing.
   No sign within two minutes halts the task, and so does a workspace that still shows no agent status by then.
   Once the agent is idle again, the run says how the turn ended:
   - **blocked with an ask** - the question is printed with its options and kept in the asks inbox (**Asks** below), naming this drive.
     The driver keeps driving every other task: a guard stop still gets its catchup, a base still releases its dependents.
     Once nothing else can move (every other task ended, asking, or held on its base), it prints every task's ask and the command that answers them all, and exits **4**;
   - **blocked waiting on its base** (`f10 state wait`, a driven ship that found its base without code) - the task parks, with no ask and no halt, and its row reads `waiting on <id>`.
     Once the base is released and its branch head has moved past the one the ship found, ship is sent again with `resume at implement`; the same head never sends it twice.
     A wait on a base this drive does not run, or one already through, halts the task with the run's note;
   - **blocked without one** (a judge stop), **failed** or **partial** - the task halts with the run's note and next, and so does every task that waits on it: one not started yet never starts, and one already running halts at its next chain boundary, its work left on its branch.
     Every other task runs on, and the driver exits **5** once nothing can move;
   - **still running** under plan or ship, or a catchup with no `done catchup` signal since its prompt, a minute after the agent went idle - the agent stopped without reporting, usually a permission dialog in its pane; the task halts naming the workspace.
     Found at prompt time on an agent the driver did not start, the same halt also says how to leave a cancelled run: quit that agent and rerun, and a new one is started in its place;
   - anything else - the skill is done, recorded on the branch, and the next one is sent.

   **Every prompt checks first.**
   An agent working is attached to, not prompted.
   A `plan` or `ship` its run already shows `done` (or `prior`) is counted done and recorded with no prompt, as when a driver stopped mid-ship and the agent finished the step before the rerun saw it.
7. **The report.**
   Progress lines as it goes, an end included: a halt or an ask is printed the moment the driver sees it, not only beneath the report.
   Then one row per task in graph order: `already finished`, `finished`, `done: <chain>`, `waiting on <id>` (parked for its base's code), `waiting for <id>` (its finish waits on its base), `asked at <skill>`, `halted at <skill>`, `halted: base <id> halted`, `owned by drive <n> (pid …)`, `cancelled`, `not reached`.
   Exit 0 when every task went through its chain, 4 when any task asks, else 5 when any halted.
   Exit 6 is a drive stopped for a daemon upgrade; only the daemon and the command following it see it.

Rerunning the same command, or `f10 drive --resume <n>`, is the resume: the record is picked up, and each task's position is read from its own branch and run state.
With `--answer "1. … 2. …"`, the first task stopped on a question gets the text as its agent's next prompt, plain, the way `f10 forward` sends an answer, and the driver polls that turn as it would any other.
Several asking tasks take one answer each, keyed by id: `--answer "42=1. proceed" --answer "45=1. no"`, each sent to its own task's agent.
An answer sent closes the task's ask in the inbox. An answer the inbox already holds for a task goes first: one typed in the agent's pane is followed as the turn it started, with nothing sent; any other is sent as an `--answer` would be, and an `--answer` for the same task goes unused with a note.
A drive whose task was answered after it last looked, while it still held its lease, resumes itself once it has stopped: `f10 asks resume` waits for its process to end, then has the daemon run `--resume <n>`.
`f10 drive cancel <n>` closes the drive's open asks.
An agent found mid-turn is attached to rather than prompted again.
The turn is read by what it reports: one that turns out to be a catchup is followed like a catchup the drive sent, so a clean one resumes ship at the entry its stop names.
A ship the catchup guard stopped before `implement`, `pr` or a `judge` (its next naming `/f10:catchup`), or a driven catchup left `failed`, is prompted again on a rerun once a catchup settled the branch, by the same checks as point 5 (no mark, no merge or rebase in progress, a clean tree), with `resume at <entry>` so ship starts where it stopped; a branch still marked gets the catchup first.
A task halted for another reason stays halted until its run changes: settle it in that workspace, or send steering through `--answer`.

`/f10:drive` runs the command in the background, answers an exit 4 through `/f10:asks` (the questions in one questionnaire, the answers written to the inbox, the drive resumed), and prints the final report.

Out of scope: more than one base per task, and ordering tasks by anything but their recorded dependencies.

## Daemon

`f10 daemon` is one process per user that runs drives, so a drive outlives the session that typed it, and watches the projects tasks were started in (**Watch** below).
Nothing registers it as a service: `f10 drive` and `f10 start` start it when none listens, and it exits after ten minutes with no drive running, no project watched and no call.

- **Where.** `$XDG_RUNTIME_DIR/f10`, else `~/.f10/run` (`F10_RUN_DIR` overrides), kept `0700` and refused when another user owns it: the socket `daemon.sock`, the daemon's own `daemon.log`, a log per drive run under `logs/` (with its `.exit` file and an `index`, finished ones pruned after seven days), the locks under `drives/locks/`, `drained.json` between an upgrade's two daemons, and `watch.json` (`0600`), the watched projects with the environment their passes run with, tokens such as `GH_TOKEN` included, for as long as each project is watched, with a `logs/watch-<hash>.log` per project.
- **Starting one.** A client dials the socket; with nothing listening it takes the `start` lock, dials again (another client may have started one), else starts `f10 daemon` detached in its own session and waits up to 5s for it to listen. The daemon holds its own `daemon` lock for life and exits at once if another holds it, so two clients at once get one daemon.
- **A drive.** The client sends the command line, its directory, its storage root and an allowlisted copy of its environment: `PATH`, `HOME`, `USER`, `LOGNAME`, `SHELL`, `TMPDIR`, `LANG`, `LC_*`, `TERM`, `SSH_AUTH_SOCK`, `XDG_*`, `GH_*`, `GITHUB_TOKEN`, `GLAB_*`, `GITLAB_TOKEN`, `HERDR_*`, `F10_*`, `CLAUDE_*`, and the keys the frontmatter's `drive_env` lists. The daemon runs `f10 drive --foreground <command line>` in that directory with exactly that environment, its output to the drive's log and `F10_DRIVE_FILE` naming a file the drive writes its record number to once the record is open, and writes `<log>.exit` when it ends: the exit code, the drive's number, and for exit 6 why it drained (`upgrade` or `stopped`). A request for a drive already running in the same storage root (the same command line, or `--resume` of its number) with no `--answer` follows that drive's log instead.
- **Upgrades.** A client sends its version first. Only a client of a strictly newer release (`0.46.0` over `0.45.2`) replaces the daemon; an older client, or a development build, uses the daemon running and says which version it runs, and `f10 daemon stop` is how to replace one by hand. A client following a drive never starts a daemon while another drains: the replacing client starts the next one, and a follower starts one only when none has listened for 5s after the old one is gone. A replacing client asks the old daemon to drain: each drive stops at its next tick, after any finish it is in the middle of, ends no task, releases its leases and exits 6; the old daemon writes those drives (storage root, directory, number and environment) to `drained.json`, `0600`, and exits. The client then starts its own binary's daemon, which runs each drained drive again as `--resume <n>`, and every client that was following one follows it into the new daemon, whatever its version. A drive that has not stopped after ten minutes is stopped outright and not resumed; rerunning its command resumes it.
- **A crash or a reboot** leaves running drives stopped, their records intact: rerunning the drive's command resumes it.

`f10 daemon status` prints the pid, version and start time, then each drive: its project, number, pid, log, and each task's standing from its record; then each watched project: its last pass, that pass's exit, and its log.
`f10 daemon stop` drains the daemon the same way but keeps no drained list: each drive stops at its next tick, a client following one ends with `f10 drive --resume <n> goes on`, and nothing resumes until someone runs that. `--now` stops each drive at once, and cuts a drain in progress short: the daemon keeps answering `status` and `stop` until its drives are gone.
`f10 daemon logs` prints the daemon's log, `f10 daemon logs <n>` drive `n`'s newest log in this project, `-f` following it until the drive exits.

## Asks

A driven agent's ask (`f10-state.sh ask`, `modes/driven.md`), and a drive's own finish question, wait in one per-user inbox until the first answer from any way in.

- **Where.** `~/.claude/f10/asks/` (`F10_ASKS_DIR` overrides), beside the run state and outside every project: one YAML file per open ask, `<root hash>-<TASK>.yaml`, naming its task, url, storage root, main checkout, worktree, drive (none for an agent no drive watches), skill, the ask's text and its parsed questions. The first answer moves it to `answered/` with the answer, the way in (`pane`, `asks`, `skill`, `drive`, `cancelled`) and the time, through a rename only one caller wins; a later one is told who answered and when. At each session start, answered asks older than a week are pruned, and so is an open ask whose worktree is gone.
- **Raised.** `f10-state.sh ask` writes the ask as the agent stops; the drive that sees it adds its number. The same text is one ask, so the user is told once.
- **Told.** The status line counts open asks, ` f10 · 2 asks`, with or without a run (`docs/statusline.md`). A `UserPromptSubmit` and `SessionStart` hook (`f10 asks hook`) adds each open ask to a session's context once, leaving out the asking agent's own session. And `notify` runs per new ask: `~/.f10/asks.md` frontmatter, `notify: ntfy publish f10 "{task} asks: {question}" {url}`, run through `sh -c` for up to 10s, with `{task}`, `{question}` (the first question) and `{url}` standing for the shell variables `${F10_ASK_TASK}`, `${F10_ASK_QUESTION}` and `${F10_ASK_URL}`, so an agent's text is never run as shell; quote them as any variable; `notify: off` runs nothing, and absent it is a desktop notification (`osascript` on macOS, `notify-send` on Linux when installed).
- **Answered.** Typed in the agent's own pane, the prompt is the answer: the agent goes on, and its drive, if stopped, is resumed to follow the turn. `f10 asks answer <task> 1=b 2=8080` answers questions by option letter, number or text; free text only for a question with no options, and every question not named takes its default (one with no default must be named). `<project>:<task>` picks between two projects' asks. Bare `f10 asks` in a terminal walks the open asks, Enter taking each default and `s` skipping an ask; elsewhere it is `f10 asks list`. `/f10:asks` asks them in one questionnaire and runs `f10 asks answer --by skill`.
- **Delivered.** An answer to a drive's task goes to its drive: a stopped drive is resumed through the daemon (`--resume <n>` from its main checkout, with the answering process's allowlisted environment), and a drive still running takes it as it stops. An answer to an agent no drive watches is sent to the agent, as `f10 forward` would; one that cannot be sent is printed to type in its pane.

`f10 asks list` prints the open asks, oldest first, then those answered in the last day; `--json` prints `{open: [...], answered: [...]}`.

## Watch

The daemon watches every project `f10 start` or `f10 drive` ran in, for the tasks no drive owns: a task started by hand whose PR merged on the host, an agent left idle, a run that stopped with nobody looking.
`watch: finish` is the default: a pass also finishes a task whose PR merged; `watch: report` only reports, and `watch: off` watches nothing; `watch_catchup: on` lets a pass also keep an open PR up to date with its target (**behind** and **catchup** below).
Owning is the task's lease (**Drive**): a task a drive or a finish holds is never looked at, and the watcher's own finish takes the lease first, so a drive started later is refused with `owned by the f10 watcher`.

- **Registering.** After its report, `f10 start` hands the daemon the project's storage root, its main checkout and the same allowlisted environment a drive gets; `f10 drive` does the same before it follows its drive. It never replaces or waits on a running daemon: a daemon of another version is used as it is, and one that is stopping is skipped with one line, so the next `f10 start` or `f10 drive` hands the project over. A failure is one `watch: warning:` line, never a failed command. The project stays watched across daemon restarts and upgrades (`watch.json` in the run dir, which keeps the newest registration's environment, tokens included), and leaves the list once a pass finds no task worktree left (under `watch: finish`, no local task branch either, since its janitor still has work), once its main checkout is gone, or after ten passes in a row fail. A registration that arrives while a pass runs gets one more pass after it, so a task started meanwhile is seen. `watch: off` in the frontmatter registers nothing.
- **A pass.** Every 3 minutes, and at once on registering, the daemon runs `f10 watch --pass` in the main checkout with that environment, its output appended to the project's watch log. For each task worktree whose lease nobody holds and that `f10 watch off` did not hand back, it reads the PR (`gh pr list --head`, one call per branch), the newest run reported from the worktree and the agent's status, and says at most one thing, in this order:
  - **merged**: the PR merged on the host. Under `watch: finish`, with the branch's tip equal to the PR's head and the agent not working, the pass takes the task's lease, reads the agent's status and the branch's tip again, and finishes the task as `f10 finish` would (the worktree removed, the workspace closed, the plan archived, main pulled), with a finish that refuses anything but a merged PR, so the watcher never merges. A refusal (a dirty worktree, someone else's PR, a bundle's riders) is reported with `f10 finish <id>` to run by hand. A branch with commits the PR lacks is only reported.
  - **closed**: the PR closed without a merge; reported, never cleaned up.
  - **stalled**: the agent idle while its run says plan or ship is running and the run has not changed for 10 minutes: the test a drive halts on, with a longer grace, measured from the run's last write since a pass keeps no memory between runs.
  - **stopped**: the run ended `blocked` (with its question), `failed`, `partial`, or ship `done`.
  - **catchup** and **behind**, under `watch_catchup: on` only. When any undriven task has an open PR and an idle agent, or a catchup in flight, the pass fetches origin once and reads what each such branch targets as the catchup step does (the branch it was started after, else the default branch), cross-checked against the PR's base. A target that moved past a branch whose tip is origin's, with an idle agent and a clean tree, is marked (`f10-landed` or `f10-base-moved`, the drive's marks; one already there, left by a finish, a ship guard or a failed catchup, is set again) and the agent is prompted with a driven catchup under the task's lease. A later pass reads the end from git (the mark gone, no merge or rebase open, a clean tree) and pushes the branch under the lease, plain after a merge and `--force-with-lease` after a rebase, against the origin tip the catchup started from: a commit pushed to the PR meanwhile is never overwritten, and such a catchup is told once as not pushed. One that did not end clean is told with `/f10:catchup` to run in the worktree, the branch left as it is, and is sent again only once the target or the branch moves. **behind** is a PR the watcher will not catch up: no agent in its workspace, a PR retargeted to a branch the task was not started on, or a branch whose tip is not origin's. A working agent or a dirty tree waits for a later pass; a task a drive holds, or one handed back, is never touched.

  A pass lists only this checkout's branches, so it asks the network nothing before the host, except the one fetch `watch_catchup: on` needs. An agent whose status cannot be read (no listing from Herdr) is never finished. A host that does not answer for a task's PR leaves the task out of the pass, and the pass says so in one line naming the error and the tasks left out, every pass until the host answers; `f10 watch` shows it under `host`.
  Each fact is told once: `watch.yaml` in the storage root keeps the last report per task and every fact told for it (the PR's state, the run's update time), so a fact is never repeated, even after another, and a newer one is told. Under `watch: finish` the janitor then runs if it is due; under `report` it never runs from a pass, since it deletes branches.
  A pass exits 7 when nothing is left to watch, which drops the project from the daemon's list.
- **Telling.** A pass reports through its own output, one line per report, into the project's watch log; `f10 watch` prints the last report per task.
- **Handing a task back.** `f10 watch off <id>` sets `branch.<branch>.f10-watch` to `false`: no pass reports on the task, finishes it or catches it up. A mark git cannot read (a lock, a broken config) counts as handed back for that pass, with a line naming the branch and the error. `f10 watch on <id>` removes it. `f10 start` on a merged or closed PR sets the same mark on the branch it creates, since the PR landed before the branch existed. Typing in the task's own pane needs nothing: a working agent is never stalled.

## PR catchup

`f10 pr catchup` decides what a catchup of the current branch brings in and how, so the catchup step, ship's guard and the dry run read one answer; settling conflicts stays the step's.
It fetches origin first (`--no-fetch` reads the refs at hand) and prints `key: value` lines, an empty value left out:

| key | meaning |
|---|---|
| `branch` | the current branch |
| `base`, `sha` | the ref to integrate and its sha; with `f10-base-moved` set (not before `pr` or `judge`), the commit the mark names, printed as a sha |
| `upstream` | with `method: onto` only: the base's old tip, from `branch.<name>.f10-base-tip` |
| `found` | `f10-landed`, `f10-after-branch`, `f10-after-branch gone` (its task finished: unset both after keys) or `default branch` |
| `how` | how the base was found, in a sentence |
| `method` | `merge`, `rebase`, `onto` (the base's history was rewritten: `git rebase --onto <base> <upstream>`), `none` (the base is already in the branch) or `skip` (a guard with nothing to do) |
| `why` | the frontmatter key that set the method (`catchup:`, `catchup_stacked:`), or that it is the default |
| `mark` | the key and value a conflict sets (`f10-landed <default>`, `f10-base-moved <sha>`) |
| `code` | for an `f10-after-branch` base: `yes`, or `none` when it has no commit of its own, merges not counted |

`--guard <entry>` decides as ship's guard before `implement`, `pr` or `judge`: before `pr` and `judge` every base is read from origin, a branch already on origin is merged so its push never forces, and `catchup: off`, the default branch itself, no recorded base before implement or a base not on origin yet before `pr` print `method: skip`.
`f10-base-moved` overrides the local-first order of an `f10-after-branch` base because the drive's and the watcher's push gates check that sha against origin, so a newer local tip would carry commits neither checked; a marked sha the base branch no longer holds fails, naming the key to unset.
A base is rewritten when the branch holds the recorded `f10-base-tip` and the base does not, as after a squash merge or a rebase of the base.
`onto` then replays only the branch's own commits, whatever `catchup:` or `catchup_stacked:` says; the guard still merges a branch on origin.
`f10 start --after` records the tip, `f10 finish` records it again for each dependent before deleting the base branch (a spike closed without merging clears it instead), and the catchup step refreshes it once green.
`f10 pr base` prints the same `found: f10-after-branch gone` line for a finished stacked base.
No `origin/HEAD` fails with its remedy, as `pr base` and the drive do.

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

`f10 review round` is the one local verb: it reads the storage root's findings files, needs no remote review declared, and writes nothing.
The directory is `reviews/<task id>` (a bundle's lead), else `reviews/<branch>` with `/` as `-` for work no task names.
It prints `dir:`, `latest:` (the highest round's file, absent with none), and the next round's `round:` and `path:`.
Two cases decline, exit 3, with a `decline:` line and the findings behind it instead of the next round: `1.md` holding a finding without a verdict, and `2.md` present, the last round, naming each finding whose latest block is an `ask` or still waits for a verdict.
`--json` emits the same.

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
A state row's stage says where the task is (`asks-waiting`, `blocked`, `ready`, `in-review`, `pr-open`, `implementing`, `planning`, `planned`, `merged-unfinished` (merged, its worktree still on disk), `next`, `done`) and `stage_from` where the evidence came from: `local` for this machine's state, `trace` for the traces the project left on the host, `tracker` for a `next` row, `guessed` for the host's own facts alone.
On a repository nothing ever traced, read with `--remote`, every in-progress row is `guessed`.
The next action is a command where one exists (`f10 finish FT-71`, `/f10:resolve FT-71`), else a sentence.
Rows draw under `in progress`, `next` and `done`; a task waiting only on its unmerged base, with nothing of its own started, stays under `next`, after the tasks that can start now.
Inside a session, `/f10:board` runs the command with the same flags and prints its output; with `--state` it offers the rows whose next action is a command: the in-progress ones one per line, the `next` group as one line for its first task (`f10 start <id> and <N> more`), running none.

## Stats

`f10 stats` is how long each part of a delivery took, per task of the current project, newest first, then a median row.

- **plan**: active minutes. A `/f10:plan` run counts from its start to the plan it saves; inside `/f10:ship`, the plan step counts from entering it to entering the next step.
- **implement**: active minutes of the `implement` step, summed over its stretches (a driven run blocked on an ask and resumed is two).
- **review**: the review loop, review, resolve and judge together. A `review` entry runs both review and resolve under one claim: review ends when the round's findings file is first written, and the rest of the entry is resolve, a second round and a resolve resumed after an ask included. Under `review:local+ci` the file is first written by the local half, so the wait for the CI half counts as resolve. Every `judge` flow entry counts here, the one before implement as well; the judge fetch runs on a stale task counts as plan.
- **deliver**: every other step the flow names: `pr`, `cleanup`, `commit`, `push`, `deploy`, `demo` and project-defined steps.
- **glue**: the time a plan or ship run spends outside every step: loading, the fetch of a run that reuses a plan, the gaps between steps, and the work after the last step. A run's stretch ends at the first turn end after its last row (the saved plan, or `ship done`, `failed`, `partial` or `blocked`), else at the next run; a driven run resumed on an answer opens a new stretch. A stretch that never ends counts nothing.
- **size**: lines added plus deleted, with the markdown share, as `f10 finish` measured it.
- **density**: implement minutes per 100 changed lines.

`f10 stats <id>` adds that task's steps under the table, one row per step in flow order (plan, judge, implement, cleanup, pr, review, resolve, then other steps alphabetically, glue last) and a total.

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
`children` prints a tree to a terminal and JSON to a pipe or with `--json`; `--plain` prints the tree into a pipe too, with no urls (they stay in `--json`), and `--open` leaves out the finished rows as well (merged, or the plan archived), a dependent of a finished base moving up to its base's level.

## Stale

`f10 task stale 71` answers the fetch step's check under `stale_after`, one line on stdout:

| line | meaning |
|---|---|
| `fresh` | `stale_after` is not declared, or the task is younger than it; the tracker is not asked when it is not declared |
| `judged <path>` | older, but a verdict file with subject `task <id>` reads `proceed` and was written within `stale_after`; the newest one is named |
| `needs judge` | older, and no such verdict |
| `undated` | no created date was found |

`--created <YYYY-MM-DD>` passes the date the task's read showed, which spares a tracker call; without it the date comes from the host's issue, and on any other tracker the answer is `undated`.
Verdict files are read from the storage root and, from a worktree that keeps its own `.f10`, from main's too.
A bundle is checked on its lead.

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

`f10 task create` creates a task through the driver's `create` verb ([driver contract](driver-contract.md#creating-a-task)), else, on a tracker that is the host's issues, as a GitHub or GitLab issue.
It reads the task as markdown on stdin, `# <title>` on the first line, and prints the new id in the project's id format.
The body's `After: <id>` line reaches the driver as a link to the base task, `After: [<id>: <title>](<url>)`, titled as `f10 task ref` would; a base it cannot title stays bare.
`--status`, `--priority` (one of `urgent`, `high`, `medium`, `low`), `--label` (repeatable) and `--parent` go to the driver as flags, with the body's `After:` line as `--after`; an unset property passes no flag.
On the host, `--status` takes only open, `--priority` becomes the label `tracker.priority` maps to it, and `--parent` links the new issue as a GitHub sub-issue; a link that fails is a warning, and the id still prints. GitLab nests tasks, not issues, under an issue, so there `--parent` always warns and the parent's body lists its subtasks.
It exits **3** when neither creates tasks here (no driver, or one declining `create`, on a tracker that is not the host's): the capture step then uses the project's Create adapter.
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

## Report

`f10 task report` prints the facts block a skill's report opens with (`conventions/report.md`), from the same titles and urls as `task ref`.
One id prints its `task`, `title` and `url` rows.
Several ids print a row each, in the order given: the id as the label, `↗ <url>` as the value.
A task with no url known prints bare, and the exit is 0.
A parent and its subtasks report through `f10 task report <parent> <subtask ids>` for the urls and `f10 status <parent> --plain`, whose tree carries the titles.

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
