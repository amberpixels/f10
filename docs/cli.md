# The `f10` binary

A lookaround, and the commands that start, reach and finish tasks.
The lookaround verbs explain the loaded configuration and read the things it points at, changing nothing.
Six verbs act.
`init` registers a checkout and never overwrites.
`start` takes one task or a list, creates the branch and worktree each task is worked on in, opens them in Herdr and runs the task through its flow.
`forward` hands a command to the agent a task already has.
`stop` stops a drive.
`finish` merges the task's PR (or a branch never pushed, locally) and removes what start created.

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
f10 config [verify] [--json]   # the effective configuration, with the origin of every value; verify: the one verify line the loader prints
f10 agents [<step>...] [--kind KIND] [--agent PROFILE] [--pace NAME] [--json]   # the merged agents map with each value's origin, or each step's resolved agent profile and what runs on it; the run's pace lays its effort dials on top
f10 flow [<name>] [--pace NAME] [--json]   # the declared flows under the run's pace and the project's ceiling, each with what selects it; with a name, that flow an entry per line with its dials and a labelled entry as written (`pr[pr-polish]`), an optional CI review (`ci?`) settled with the reason, the list /f10:ship runs; a dial the ceiling capped names the ceiling and what the pace had
f10 completion <shell>   # the completion script for bash, zsh, fish or pwsh; --flow completes the declared flows
f10 help [--all | --agent]   # the human commands; --all adds the plumbing the steps call; --agent prints one line per command for an agent's context
f10 doctor [--json]      # check the plugin, the binary and the environment; changes nothing

f10 task read [id]       # the task as markdown; a bundle (id+id) reads every member
f10 task current         # the id a bare skill call stands for: the branch's task, else this session's (exit 1: none)
f10 task ref <id>... [--md] [--fresh]   # `<id> (<title>)` per task, a markdown link with --md, `[closed]` after a closed one; from what f10 already read
f10 task report <id>... [--children]  # a report's facts block: one task's rows, else a row per task, a parent's subtasks under it
f10 task stale [id] [--created YYYY-MM-DD]   # the stale step's check: fresh, judged <verdict file>, needs judge or undated
f10 task regression [id] --repro R --confidence C [--commit SHA --lines PATH:A-B ...]   # record a bug task's origin task and how review let it through
f10 task regression [id] --claimed ORIGIN [--text PHRASE]   # record the origin a task's own text names
f10 task open [id]       # in the browser, no id the task list (--link writes the url, --print the item or the list's first page, ranked by priority)
f10 task search <query>  # rows, not a picker
f10 task children [id] [--json | --plain] [--open]   # the task's open subtasks, in After: order
f10 task create [--status s] [--priority p] [--label l]... [--parent id] [--set k=v]... < task.md   # through the driver, else as a host issue; prints the new id
f10 task comment [id] [--closed] < comment.md   # a comment on the task; prints its url when the tracker gives one; a closed task needs --closed
f10 task update [id] [--status s] [--priority p] [--label l]... [--assignee a] [--parent id] [--after id | none] [--set k=v]...   # absolute values
f10 task retitle [id | --all] [--dry-run]   # write tracker.title onto the host's issue titles
f10 plan read [id]       # the plan saved for that task
f10 plan open [id]       # in your editor (--link writes the path, a file:// link on a terminal)
f10 demo open [id]       # the demo report, in the browser (--link writes the path)
f10 pr open [n | id]     # the PR or MR: a number is one, a task id its open one, else its last; none, their list; gh or glab decided by the remote (--link, --print as for task)
f10 pr base [PR | id] [--origin]      # the base: the open PR's target, else the recorded stacked base, else the default branch
f10 pr diff [PR | id] [--name-only]   # the change against that base: here with uncommitted work, else the open PR's diff, else the branch
f10 pr checks [PR | id] [--json]      # the open PR's head checks: green, red, pending or none, the sha, each failed job with its log url; exit 1 on red, 8 on pending
f10 pr checks [--brief | --triage | --log]   # one line; or with what a triage decides by: the first real failure, that job on the default branch, what was tried; or that failure's log
f10 pr checks --rerun | --record fix|left  # rerun a red head's failed jobs, record a fix round, or record a head left red; each refused once spent (see Checks triage)
f10 pr view [PR | id] [--comments]    # branch, base, task and open PR (head sha, target, mine) with its description; says so when there is no PR
f10 pr create --title T --body-file F [--label L]... [--assignee A]... [--draft]   # open a PR for the branch here against pr base; prints its url, or an open one's; the title carries the task id in the tracker.title form (else `<id>: `) unless it already does, and a bundle's names its riders after the lead id
f10 pr edit [PR | id] [--title T] [--body-file F]   # rewrite the open PR's title or body; base, labels and assignees stay; the title gets the task id and a bundle's riders as pr create's does
f10 pr comment [PR | id] [--reply-to URL [--quote T]] < text    # comment, or reply in a review's thread; prints the url, never posts the same text twice
f10 pr status [PR | id] --name N --state S -m TEXT [--sha SHA]   # a commit status on the open PR's head; one set the same way is left alone
f10 pr catchup [--guard implement|pr|judge] [--no-fetch] | --mark '<key> <value>'   # what a catchup of the current branch brings in: base, how found, method and why, the conflict mark; --mark writes that mark on the branch
f10 review pick [n] [--latest] [--json]               # the latest unhandled remote review, as markdown
f10 review wait [n] [--budget 10m] [--every 20s]      # poll it to done within a budget
f10 review ack [n] [--id <id>]                        # leave the declared handled marker on it, or on the review --id names
f10 review settle [n] --id <id> --revision <rev>      # leave the declared settled marker, unless it was rewritten
f10 review round [task-id] [--json]                   # the findings directory, its latest round and the next, or the decline (exit 3)
f10 trace trailers --step <step> [--task <id>] [--round n --fixed n --skipped n]   # the Task:, Step: and Review: lines a step's commit ends in; nothing with traces off
f10 trace state --step <step> [--round n] [--open n] [--asks n] [--task <id>] [--body-file <file>]   # rewrite the hidden state block in the branch's open PR body, or put it into a drafted body; nothing with traces off
f10 status [id] [--all] [--json | --plain] | --drives [--all]   # --drives: the newest 20 drive records, each running, stopped, done or cancelled (--all: every one); else where the run is: a task's in its worktree (bare: the session's task, else the branch's, with that output's --json shape; in the main checkout, a `launched here` tree of what the session started or drove that is still live, nested by `After:` as a parent's children are, task rows under --json), else this session's, every live one, or a row per child of a parent
f10 standup [--since <time> | --week] [-C <checkout>]... [--group <name>]... [--all] [--json]   # your commits and PRs since your last standup, grouped by project and task
f10 standup mark --since <time> [--at <time>]      # record a standup as reported: the next one starts where it ended
f10 board [<window> | --since <window>] [--state] [--remote] [-C <checkout>]... [--group <name>]... [--all] [--detail] [--by person] [--json | --html] [--out <path>]   # what changed today (else the last working day); --state: where every task stands; one project or several
f10 atlas [-C <checkout>]... [--group <name>]... [--all] [--json] [--out <path>]   # your public projects: what ~/.f10/atlas.md declares and what their git history says
f10 stats [--since 30d] [<id>]   # active minutes per task by step group, with size and density; a median row; an id adds its steps
f10 history <path>... [--since 180d] [--task <id>] [--json]   # what the tasks that touched these files went through, in at most 15 lines
f10 audit map <range | all> [--json]                    # churn, tasks and pain per area over a range, for /f10:audit to partition from
f10 ledger dump [--json] [--project p] [--task id] [--session s]   # the recorded events, oldest first
f10 ledger schema        # the JSON Schema of each event kind's payload, by version (docs/ledger.schema.json)

f10 start <id | id+id>[-suffix][:profile]... | <id> -- <id> [--plan] [--flow <name> | --local] [--pace <name>] [--after <id> | --base <branch>] [--agent <profile> | --model <model>] [--parallel <n>] [--answer [<id>=]<text>] [--every 30s] [--foreground]   # each task in a worktree, a Herdr workspace and an agent, run through the flow by a drive in the daemon
f10 start <id | parent> [<id>...] --discuss [note...] | <PR url | #n> [--plan] [--task <id>] [note...]   # one agent in one worktree, prompted once, that reads the tasks and waits
f10 start --resume <n> [--answer [<id>=]<text>] [--every 30s] [--parallel <n>]   # pick drive n up again
f10 stop <id | n>   # stop the drive holding the task, or drive n; nothing more is prompted
f10 forward <id>[-suffix] <text...>   # hand a command or a steering note to the agent working on the task
f10 drive plan <parent | id | id+id>[:profile]... [--flow | --local | --pace | --agent | --parallel] [--facts F] [--json | --hint]   # plumbing: propose what to bundle, run in parallel and order; changes nothing
f10 daemon status | stop [--now] | logs [<drive>] [-f]   # the per-user process drives run in
f10 remote on [--port N] [--mcp] | off | token add <name> --scope <scopes> | token list | token revoke <name> | install | uninstall   # serve the daemon's API to your other devices on the tailnet
f10 mcp [--scope <scopes> | --token <name>]   # the daemon's API as MCP tools over stdio, for Claude Desktop and other MCP clients
f10 inbox [list [--json] [--all] | answer <task> [N=VALUE ...]] [--all]  # the questions driven agents wait on in this project; bare walks them in a terminal
f10 finish [<id>[-suffix]] [--yes] [--ignore-checks] [--empty] [--close <ids|all|none>] [--keep]   # no id: the branch's task; merge the PR (a branch never pushed: here), pull main, archive the plan, close the workspace, remove the worktree; a spike with no PR closes without a merge; a parent whose subtasks are done closes
f10 janitor [--dry-run]  # remove task branches whose PR merged or closed outside finish, locally and on origin; start and finish run it every 6 hours

f10 lore add <kind> --change <id> [--anchor path:line[-end][@commit][#symbol]]... [--supersedes <id>] [kind flags] [-m <text> | -F <file> | stdin]   # write one lore entry, print its id
f10 lore change [<task-id>]         # the change the task's current PR writes to: <id>, or <id>.<n> for its n-th PR
f10 lore show <change> [--json]     # the change's effective entries, the intent first; a task id lists each PR's change
f10 lore why <path:line> [--json]   # the change behind a line: intent, latest verdict, entries on the line
f10 lore lessons <path> [--json]    # the lessons to read before editing a file
f10 lore findings <path>... [--json]   # findings settled without a fix on these files, with their reasons and lines at HEAD
f10 lore check <change>             # exit 1 naming why the change does not pass its gate
f10 lore validate [<change>]        # lint entries against the schema
```

A verb means one thing under every noun.
`read` writes content, `open` follows an address, `search` finds by description.
The matrix stays sparse where a verb has no meaning for a noun.

`f10 --help` lists the commands a person types.
The verbs that steps and skills call, such as `f10 trace`, `f10 review` and `f10 task ref`, are plumbing.
`f10 help --all` lists them under their own headings: top-level ones under `plumbing:`, and subcommands by their full path under `PLUMBING SUBCOMMANDS:`.
`f10 help --agent` prints one line per command, generated from the command definitions.
`bin/f10-inject.sh` tells every f10 skill that runs a step to load that output beside the conventions, so the agent's context holds the CLI as it was built.

`bin/f10-load-project.sh` stays the loader that agents use.
The binary may interpret `project.md` prose, and it shows what it cannot place as `unrecognized`.
The parity suite in `cli/internal/layout` runs every fixture through both the script and the binary.

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
| `herdr` | `herdr` on PATH; scoped to `start`, `forward` and `finish` | install Herdr |
| `remote` | the tailnet listener's setting, this machine's Tailscale address, the address and web client URL the running daemon bound (asked of a running daemon, never starting one), whether it serves HTTPS, the MCP endpoint when `--mcp` turned it on, and the service file with the program it runs; `not needed here` while the listener is off and no service is installed; scoped to `remote` | `tailscale up`, `f10 daemon logs`, enabling HTTPS certificates in the tailnet's admin console, or `f10 remote install` again when the service's program is gone |
| `host` | the `gh` or `glab` the remote routes to, installed and `auth status` green for the host; `not needed here` without a remote | `<cli> auth login --hostname <host>` |
| `driver` | the `f10-drivers` the `.f10/driver` shim runs (the shim itself, or the path or bare name its text gives): its `--version` against the oldest release this binary works with, v0.4.0, the first that takes `--set` on `task create`; a too-old one, or one that predates `--version`, is scoped to `task create --set`; a custom driver is not compared; a tracker that reads only through a driver (Notion, Jira) with no `.f10/driver` is a problem scoped to `task` and `start`, and a host-issues tracker without a driver gets no row | `go install github.com/amberpixels/f10-drivers/cmd/f10-drivers@latest`, or add the shim per [the driver contract](driver-contract.md) |
| `project` | `project.md` frontmatter and the user's own files in `~/.f10` (`agents.md`, `paces.md`): no unknown keys or values; `not needed here` outside a project when the user's files are clean | edit the named file; `f10 config` shows every fact |

Inside a session the plugin judged is the one on the session's PATH (a directory marketplace or `--plugin-dir` included); outside one, the install `installed_plugins.json` records.

It exits 1 when any check that every command needs has a problem.
A scoped problem (`herdr`, `statusline`, `remote`) is printed but affects only what it names.
`not needed here` never fails.

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
The run's pace lays a `judge: { effort }` dial over the `judge` key, and that key's origin reads `pace <name>` ([Paces](project-setup.md#paces)).
The pace is `--pace`, else the branch's record, else the project's or the user's `pace:`.
`f10 agents <step>...` prints each step's resolved profile: `review: claude:opus:high  (model: default (declared (user, ~/.f10/agents.md)), effort: review (declared (main, frontmatter)))  Agent tool: model opus, effort high  - the blind reviewer, a subagent`.
A step scoped by task kind (`plan.bug`) prints a line per kind unless `--kind` picks one.
`--agent <profile>` lays a run's profile over `default` as `f10 start --agent` does, and `--json` emits the same lines.
`f10-load-project.sh <step>...` prints this for the steps it loads, which is where a spawning step reads the Agent tool form.
A key or a profile the vocabulary refuses is a problem naming the file, as a `project.md` fact is.
It stops every command that acts on facts, except `f10 config`, `f10 init` and `f10 doctor`, which report it.

**Your file**, `~/.f10/agents.md`, YAML frontmatter then free prose, holds the `agents:` map every project starts from (`docs/project-setup.md` → Agents).
`F10_USER_DIR` names another directory than `~/.f10` for it, the other user files, the ledger and out-of-tree project roots.
The eval runner uses it to keep a measured run off your own map.
It moves the daemon's run dir too, unless `F10_RUN_DIR` or `XDG_RUNTIME_DIR` names one (**Daemon** below).

## Start

`f10 start 42` is the three hand-offs of starting a task as one command, and the drive that runs it through its flow after.
It derives the branch name (the driver's `branch` verb, else `<ID>/<slug>` with the slug cut from the task's title, else the bare id with a note when the host returns no title), creates the worktree beside the main checkout - through `wt switch` when [worktrunk](https://github.com/max-sixty/worktrunk) is on `PATH`, so the project's hooks keep firing, else with `git worktree add` at the same sibling path - opens it as a Herdr workspace and starts a claude agent in its root pane.
Then the [drive](#drives) prompts that agent one skill at a time.
It sends `/f10:plan <id>` when the flow leads with `plan` and no plan is saved, then `/f10:ship <id>`, then finish when the flow ends in it.
Several ids, a bundle, a range or a parent start the same way, each task once its base has code, or under `--plan` once its base's plan is saved.
The command follows the drive until it ends, an agent asks, or a task halts.
Ctrl+C stops only the following, and the drive goes on in the daemon.

What the start does, case by case:

- each prompt is closed by `--driven` ([driven mode](../modes/driven.md)).
  The marker tells the agent it is prompted from outside, so every question goes through run state and every gap takes its default.
  `--plan` plans every task and implements none.
  A later start of the same tasks without `--plan` ships the saved plans.
  `--flow <name>` ships through that declared flow, as `/f10:ship <name>: <id>`.
  `--local` is `--flow local`, so nothing is pushed.
  A flow with no leading `plan` sends ship alone, and so does a pace that skips `plan`.
  `--pace <name>` is recorded on the branch, where ship's `f10 flow` reads it, so the prompt does not carry it.
  `--plan` with `--flow` or `--local` is refused, since `--plan` stops before ship, which runs the flow
- `--discuss` opens one worktree and one agent, prompted once with no drive.
  The agent waits for you in its own pane, so the prompt is plain text with no `--driven` marker.
  - One task: the agent summarises it and waits.
  - A parent: the worktree is the parent's. The agent prints the subtask tree, each row marking what it waits on, a draft and a finished subtask, then the `f10 drive plan` proposal, and waits.
  - Several ids (`f10 start A B C --discuss`): the worktree is the first id's. The agent reads every task, shows how they depend on each other and the `f10 drive plan` proposal, and waits. A listed id with subtasks also gets its tree.

  The leading words shaped as task ids are the tasks, and the note starts at the first word that is not one.
  A note is what the agent acts on after its summary and before it waits (`f10 start 42 --discuss "/f10:judge it first"`), so quote a note that starts with an id.
  A note goes only with `--discuss` or a PR pickup, because a driven run takes its instructions from the task.
  `--discuss` is exclusive with `--plan`, `--flow` and `--local`, and takes none of the drive's flags; with several ids, `--after`, `--base` and a range are refused too, because one base cannot belong to several tasks.
  The command returns once the prompt is submitted, and the [watcher](#watch) looks after the task.

  The discussion's branch records `branch.<name>.f10-discuss`.
  A flow start run inside that worktree (`/f10:start A+B C`, typed in the agent's pane) continues the conversation there:
  - its first group takes the worktree and the agent. When the group's lead is not the task the worktree opened for, the branch is renamed to the lead's (`git branch -m`, its recorded facts moving with it), and the agent and the workspace are renamed to the lead's. Another lead starts without the discussed task's recorded base, flow, pace and agent profile. A lead that already has a branch, or one with subtasks, is refused before anything changes, and so is another lead when the discussion's branch was built on a base task's branch. A drive the daemon refuses puts the whole discussion back. The worktree's directory keeps its name, because every lookup goes by branch.
  - the other groups get worktrees of their own.
  - the command returns once the daemon has the drive, instead of following it, because the drive prompts that same agent. The drive holds the first skill until the agent's turn ends, then clears the mark. `--foreground` is refused there.
- a start whose every task is closed, none with a branch, opens a discussion instead of a drive, because a drive would run nothing.
  A note says so first, and names the flags the discussion drops: `--plan`, `--flow` and `--pace`.
  It also names any other task's `:profile` as dropped, because one agent opens.
  The first task's `:profile` is the discussion's agent unless `--agent` or `--model` names one.
  The agent's report names the commits that shipped each task and the release that holds them.
  It also names any open task whose id is one digit off, in case the id was mistyped.
  A resume, a range or `--answer` goes on with a drive already agreed, so it is never switched.
- the flow is recorded on the branch as `branch.<name>.f10-flow` in local git config, for `f10 status` and a rerun of `f10 start`.
  With `flow_by_area` declared and no flow named, nothing is recorded and the prompt is `/f10:ship <id>` alone, because ship picks the flow from the task's area
- a plan the main checkout saved for the task (`.f10/plans/<id>.md`, written by `/f10:plan` after a discussion there) stays in place: every worktree's storage root is main's `.f10/` (or the out-of-tree root), so the worktree's agent reads it there.
  The agent ships it, because the drive's first prompt is `/f10:ship <id>` (`/f10:ship <name>: <id>` with `--flow` or `--local`).
  `--plan` ends at once, saying the plan exists and that `/f10:plan <id>` in the task's pane plans it again.
  `--discuss` names the plan's path.
- the agent is started with an `--add-dir` for each dir of the storage root an agent writes to: the task-file dirs and `audits`, created if missing.
  This happens whenever that root lies outside the worktree, so its writes there ask no permission.
  `instructions/` and `driver/` are left out, so a worktree's agent cannot change what every checkout loads.
- `--after <id>` bases the branch on that task's existing branch, local or on origin, and records the dependency on the new branch in local git config (`branch.<name>.f10-after`, the task; `branch.<name>.f10-after-branch`, its branch), so it never leaves the machine.
  The prompt names the base task and the path of its plan as the contract to design against, not the checkout, with no fallback for its absence.
  The path is in the shared storage root, and a note beneath the report says when the plan is not written yet.
  A base still at the default branch's commit is noted as a task with no code yet.
  It fails before creating anything when the task has no branch
- with neither `--after` nor `--base`, the task body decides: a line `After: <id>` (alone on its line, the last of the body by convention) is read as `--after <id>` when that task has a branch, and noted beneath the report.
  A base with no branch is finished when its issue is closed, or, for a tracker with no state, when its plan is in main's `plans/archive/`.
  A finished base puts the branch on the default branch, with a note.
  An unfinished one makes start refuse before creating anything.
  A body that cannot be read is a note, never a refusal
- a parent task with subtasks is never started itself.
  Its subtasks run instead, in `f10 task children` order, as if listed.
  An open subtask labelled `draft` is left out with a line naming it, because a draft is an idea not ready to plan.
  List a draft to run it.
  A finished subtask stays in, so a rerun reports it as finished.
  The expansion is skipped for a parent that already has a branch (started on its own) and for a tracker with no parent link.
  `/f10:start` asks which subtasks to run only when a draft is among them
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
- a new branch never takes a name a merged or closed PR used as its head.
  Finish, the watcher and the drive look PRs up by head branch, so each would read that PR as the new branch's and finish the task at once.
  The task's next PR gets `<name>-2`, `-3` and so on, and a note beneath the report says so.
  A local branch at a merged PR's head with nothing past it is refused, naming the PR and `f10 finish <id>`, which archives that PR's plan with the branch.
  A branch whose only commits past the head are main merged in, or one recreated at main's tip, has nothing past it.
  A suffixed attempt whose plain name landed resumes its newest numbered branch (`<name>-rest-2`) instead of making another.
  Origin's copy of one is left alone, and a branch whose PR closed without a merge is reused, because it holds the only copy of that work
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
A drive takes a bundle as one entry; a task `After:` a rider waits for the whole bundle.
A bundle's PR title names the riders after the lead id, up to two and then a count (`ABC-1 (+ABC-2, ABC-3, 2 more): <lead title>`), because GitHub cuts long commit subjects from the end.
`f10 pr create` and `f10 pr edit` add that tag to the drafted title, and replace one already there.
The board shows such a PR under its lead with the lead's title.
Drive's finish and the watcher ask no riders question, since a resume carries only the latest answer.
They follow the PR body instead.
A rider under `Closes #n` closes.
A rider under `Part of #n`, or not named at all, stays open.
The report names the riders left open.
A bundle merged without a PR has no body, so its riders stay open.

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
Before an open PR merges, finish reads the checks on its head, as `f10 pr checks` does, and refuses with nothing changed unless they pass.
On GitHub the target branch's required checks count when it declares any, else all of them, because a branch with no protection would otherwise gate on nothing.
On GitLab the head pipeline decides, because GitLab marks no check as required.
Green, or no checks at all, merges.
The merge is pinned to the head that was read, so a push since then makes the host refuse.
Red refuses, naming each failed job with its log url.
Pending waits up to 20 minutes, reading the checks again every 15 seconds.
Past that, it refuses, saying the checks were still running.
A host that cannot answer the read refuses too.
`--ignore-checks` merges anyway.
`--yes` does not imply it, because automated finishes pass `--yes`.
A drive's finish does not wait itself.
Instead the drive holds the task as `waiting on checks` and tries again each poll, for up to 20 minutes per head.
On red it sends `/f10:ci` to the task's agent and shows the task as `triaging checks` (**Checks triage** below).
A drive that runs no `finish` holds a shipped PR the same way before it ends the task, so a head that turns red is triaged first.
The watcher finishes only merged PRs, so its finish never waits on checks.
Its own read of an open PR's checks is under **Watch** (**red**).

### Checks triage

A red head is triaged by the `ci` step, which `/f10:ci` runs on its own and ship runs before its next entry.
It reads the first failed job that was not cancelled (`f10 pr checks --log`) and runs verify locally.
It also reads how the same job last ended on the default branch (`f10 pr checks --triage`).
Red verify is the change's own failure, so it gets one fix round.
Green verify with the job green on the default branch is a flake, so the failed jobs are rerun once (`f10 pr checks --rerun`).
Green verify with the job red on the default branch is always a question, because the default branch needs its own fix.
The limits live on the branch as `branch.<branch>.f10-ci`, local and never pushed.
A fix round counts for the branch, because it pushes a new head, and a rerun counts for its head.
Any read that finds the head green clears the record.
`--record fix` and `--rerun` refuse an action already spent, so a second red ends in a question.
A drive sends the step to the agent when finish meets red, and halts when a triage leaves the same red head with nothing new recorded.

For a bundle, finish needs `--close`: the riders the PR delivers, comma-separated, `all` or `none`.
Without it, finish exits **7** before changing anything, naming the riders the PR body closes as the default, and the finish skill asks.
`--close` overrides the body.
Before an open PR merges, each rider left open has its closing keyword in the PR body turned into `Part of #n`.
Each rider chosen to close has its `Part of #n` turned back into `Closes #n`.
After a merge that already happened, each rider left open that the body closed is reopened instead, and each rider chosen to close that the body left open is closed.
A tracker that is not the host closes nothing on merge, so the notes name the riders to close there.
The notes also carry one line on the change's lore (`docs/lore.md`): its intent, decisions and lessons counted, each named when missing, so a change that reached finish without a ship run shows it left none.
That checkout is a worktree start created, or the main checkout itself for a task shipped from main with no worktree.
A finish that stopped halfway is finished by a rerun: a task with no branch whose plan is in the archive reports `already finished` and exits 0, and a branch whose worktree is gone but whose PR merged is cleaned up without one (dependents released, default branch pulled, plan archived, only the branch deleted, no workspace closed).
A branch with no worktree and no merged PR is still refused.
A parent task delivered through its subtasks has no branch or plan of its own, so finish checks its subtasks instead.
Once every one is closed with no branch left, here or on origin, it closes the parent on the host's issues.
It names the subtasks in the report.
On any other tracker it only checks them, and the note says to close the parent there.
A subtask still open or still holding a branch is named in the refusal, and the parent stays open.
Once a finish succeeds, it marks the task finished in every stopped drive record that lists it, so `f10 status --drives` needs no rerun of the drive.
It takes each drive's own lease without waiting, so a running drive's record is left to that drive.
Only the attempt finish acted on is marked: the suffix typed, else the one its branch ends in, so finishing `<id>-b` leaves an `<id>` row as it was.
There finish merges as usual, brings the default branch up to the merge, switches main to it, deletes the local branch, and leaves main's workspace open; the report says the checkout was main's and was switched, not removed.

1. **Refusals, before anything changes.**
   A task a drive holds (its lease, **Drives** below): `GH-12 is owned by drive 3 (pid 4242)`, with `f10 stop 3` named.
   Finish takes the same lease for its own run, so two finishes of one task never run at once; drive's own finish runs under the lease drive already holds.
   A dirty worktree, named file by file, with no `--force`: commit or discard first.
   A cwd inside the worktree being removed, or a `HERDR_WORKSPACE_ID` naming its workspace, unless `--yes` was passed: the message says the command would close its own workspace.
   With the branch checked out in main, nothing closes, so this refusal does not apply.
   Outside Herdr, as start does.
2. **The merge.**
   `gh pr view <branch>` / `glab mr view <branch>` by the branch.
   Open: merged through `gh pr merge` / `glab mr merge`, never with `--admin`; when the host refuses (red CI, a required review, conflicts) its reason is the error and nothing has changed.
   Already merged: confirmed.
   A branch that holds commits made after the merged head, ones the default branch lacks, is refused before anything moves.
   The refusal names the first commit, so a follow-up is never deleted.
   Closed without a merge: a refusal.
   `f10 lore check` runs on the change the merge closes (`<id>`, or `<id>.<n>` for the task's `n`-th PR).
   It runs right before an open request merges, or before a branch never pushed lands (below).
   Under `lore_gate: warn`, the default, its reasons are a note under the report and the merge goes on.
   Under `block` they are the error, with nothing changed.
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
   On a host-issues tracker such as GitHub or GitLab, the squash or merge commit also carries a `Closes #<n>` line for the task and each rider `--close` names.
   It carries them whatever `traces` says, so the issue closes when the commit reaches origin's default branch and never before.
   The default branch then moves: `git merge --ff-only` when the main checkout is on it, else `git update-ref`.
   A conflict, or a rebase behind the default branch, is refused with nothing changed and `/f10:catchup` named; a branch already in the default branch lands nothing.
   A branch whose changes are all in the default branch is a rerun only when a commit there names the task the way a landing does.
   A landing names it in the commit's subject, its `Task:` trailer, its closing line or the merge's own subject.
   The branch's tip being in the default branch proves nothing, because a branch with no commits of its own is there too.
   Any other branch that adds nothing is refused with nothing changed (`<branch> adds nothing to <default>`), because closing it would report the default branch's tip as the task's merge.
   `--empty` finishes it anyway.
   `--yes` never implies `--empty`, because automated finishes pass `--yes`.
   Nothing is pushed, pulled or deleted on origin, and no PR closes the task, so the notes say who does.
   When the commit carries the closing lines, the task closes once the default branch reaches origin.
   A rerun that finds the branch already in the default branch says the same when an unpushed commit there carries those lines.
   Otherwise the task stays open until it is closed on the tracker.
   That covers another tracker, one a driver reads, a rebase, and a branch that landed without the lines.
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
   Either way the merge commit must then be in the local default branch.
   Finishes that share a main checkout pull one at a time: a finish waits up to two minutes for another's pull.
   A pull or fetch that loses a ref's lock to another git retries for a few seconds, because the ref is busy only while that git runs.
   A stale remote-tracking ref in the way of a new one says the same words but is not retried, because only a prune clears it.
   A pull that still fails does not stop the finish, because the merge is already on origin.
   It becomes a note naming the cause and the command that settles it.
   A local default branch with commits origin lacks is settled by `git -C <main> switch <default> && git -C <main> rebase origin/<default>`, or by pushing them.
   A stale origin ref in the way is settled by `git -C <main> remote prune origin`.
   The other causes it names are a ref lock, a busy pull, or git's own error.
   Finish never rebases or resets the default branch.
   The deferred pull is recorded in `<storage root>/pending-pull`.
   A later finish retries it until the merge commit is in the default branch.
   So does each tick of a drive that ran a finish, and each pass of the watcher under `watch: finish`.
   A pending pull keeps the project watched until it lands.
6. **Dependents are released.**
   A branch another task started with `--after` on this one has `branch.<name>.f10-after` and `.f10-after-branch` in local config.
   Both are cleared, since the base is in the default branch now and the dependent's pr targets it from here.
   The dependent branched before that code existed, so it is marked `branch.<name>.f10-landed` with the base task's id.
   `/f10:catchup`, or its next ship unless `catchup: off`, brings the default branch in before anything else runs and clears the mark.
   A note names each dependent.
7. **The plan is archived**: `<storage root>/plans/<ID>.md` moves to `<storage root>/plans/archive/<ID>.md` (`<ID>.<N>.md` when taken), within main's `.f10/` or the out-of-tree root.
   No plan is a note.
   A plan that cannot be moved keeps the worktree and its branch, since a rerun finds the task by its branch.
   **The handoff is archived** the same way: `<storage root>/handoffs/<ID>.md` moves to `handoffs/archive/` with the plan's numbering, so the task's next PR writes its own.
   A merge reports the archived path as a `handoff` row.
   On a terminal, the `plan`, `handoff` and `spike` rows link their path to its `file://` url (OSC 8).
   `f10 start`'s `plan` row and the `wrote` row of `f10 init` link the same way.
   So do the `plan`, `handoff`, `judge` and `review` rows of `f10 status <id>`.
   A drive's copy of the report links when the drive writes to a terminal.
8. **The report**, then the worktree and its local branch are removed - through `wt remove` when worktrunk is on `PATH`, else `git worktree remove` and `git branch -D` - then the Herdr workspace closes (the agent goes with it).
   With `--yes` from inside the worktree, main's workspace is focused before the close so the user lands at home; the close ends the process, which is why it comes last.
   With the branch checked out in main, main is switched to the default branch (`git switch`) and only the local branch is deleted (`git branch -D`, worktrunk or not), and no workspace closes.
   A merge prints the handoff's body under the report.
   Finish reads the file before the merge, while the branch's tip still exists to compare its `head` with.
   A handoff written at an older commit gets a line saying it does not cover the later commits.
   A missing handoff gets a line naming what to check before deploying, and `/f10:handoff` for the next finish.
   Neither line refuses the finish.
   A PR left open and a spike's close ship nothing, so they archive the handoff without printing it.

Once the merge has landed, a failing step does not stop the ones after it.
Releasing dependents and archiving the plan, the reviews and the handoff each run, and finish then exits non-zero naming every step that failed.
A rerun finds the task by its branch.
So a failure in any of these steps keeps the worktree and branch, leaves the workspace open and leaves stopped drive records unmarked.
The error ends in `rerun f10 finish <id>`.
Each step finds its own work done on that rerun, so it completes the rest.
Archiving the plan and copying a spike report out matter twice over, because the worktree may hold the only copy.
Once the worktree and branch are removed, a rerun reads the task as finished.
So a failure after that ends in "settle what failed by hand" instead.
Such a failure is archiving a spike report kept outside the worktree, or focusing or closing a workspace.
A finish closing its own workspace (`--yes` from inside it) leaves it open when a step failed, so the error stays on screen.

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
Once every branch of a task is gone, a bundle's riders included, its plan moves from `plans/<id>.md` to `plans/archive/` with finish's numbering.
Its handoff moves to `handoffs/archive/` the same way, and its images are dropped.
A task with another branch left, such as a second attempt still in a worktree, keeps both, with a line; so does a plan written after the request merged or closed (for a branch never pushed, after its last commit), since the task was planned again.
A step failing keeps that branch, with a line, and the run goes on to the next.

The output is one line per removal and per branch kept for a reason worth knowing; a branch whose request is still open is kept without one.
`nothing to clear` when nothing was.
The last run is the mtime of `janitor.stamp` in the shared storage root (main's `.f10/`, or the out-of-tree root).
The stamp moves before the run, so starts in quick succession run it once; `--dry-run` leaves it alone.
Finish runs it before any workspace closes, since closing its own workspace ends the process; its lines go to stderr, as start's do.
A drive opens and finishes its tasks without the start and finish commands, so it never runs the janitor.

## Forward

`f10 forward 42 "/f10:ship 42"` hands a command to the agent already working on a task.
The plan and ship skills run it first whenever their argument is a task id, because a task started with `f10 start` has its branch, its plan and its agent in a worktree of its own, and running the command anywhere else would run it on the wrong branch without the plan.

1. **The lookup.**
   The task's branch, found the way start and finish find it (the exact name, else the one `<id>` / `<id>-*` branch, else the one local branch carrying the id past a prefix); the worktree that has it checked out; the Herdr workspace showing that worktree; the agent under the name start gave it (below), or under `<id>` lowercased with the suffix alone, the name an agent started by an older f10 carries.
   A task started with a suffix is forwarded to with the same suffix.
2. **Three outcomes.**
   Exit 0: the command was submitted, and the report has three rows - `task`, `workspace`, `sent`.
   A note under them says where the text landed.
   It names the open ask the text answers.
   For an agent idle (or done) while its run says running, it names that run instead, since the text resumes its turn.
   The command returns at submission, since Herdr's prompt call does, and the outcome lands in that tab; nothing here waits or tails.
   Exit 3: the task is in this checkout (its branch is checked out here, or nowhere, or it has no branch) and the command runs locally; a one-line reason says which.
   Anything else is a failure.
   Outside Herdr, it fails with start's own refusal.
   It also fails when no workspace shows the worktree, or when Herdr refused the prompt.
   It fails for an agent idle while its run says running whose pane shows no input box.
   That is because a permission dialog there would take the text's Enter as its answer.
3. **The marker.**
   Text that begins with `/f10:` and does not already carry `--driven` gets it appended, so the agent knows from its first turn that it is driven ([driven mode](../modes/driven.md)).
   Plain text, an answer or a steering note, is sent as it is.
4. **Idle and not reporting.**
   When Herdr lists the workspace's agent as `idle` while a live run recorded against the worktree still says `running`, the agent is waiting on something f10 cannot see - a Claude Code permission dialog in its pane, a question asked outside driven mode.
   Forward names the agent, the workspace and the step, and sends nothing; a prompt submitted behind that dialog would wait with it.
   An idle agent on a `blocked` run is the driven state this verb exists for, and is sent to.

The answer path: `f10 status 42` reads the run the worktree's agent reports, its `ask` row included; `f10 inbox answer 42 1=proceed 2="keep ours"` writes the answers through the inbox, which sends them as that agent's next prompt and closes the ask for every session.
`/f10:status 42` does both in one go, with the question put to you in between.

Judge, demo, handoff and explain are never forwarded, because they can read another worktree's branch and write nothing in it.

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
Each row reads the child's live run in its worktree, else the ledger's last record of it, which outlives the worktree `f10 finish` removes.
The PRs come from one host search for every row.
A row reads its PR on its own only when the search missed it, by its branch or by the url the ledger kept.
On a terminal the id and the PR are links (OSC 8).
`--plain` prints the same tree with no color and no links.
So every row carries its task url on a `url` line, and its PR's url on a `pr` line, under the title.
The gates go on a `gates` line after the `url` and `pr` lines.
Titles, halt and next lines are cut to `$COLUMNS`, else 100, but an ask or a url is never cut.
A parent and `launched here` print a tree whatever the count, and a child's files are one call away in `f10 status <child-id>`.
A skill relays it in a fenced block: relayed text shows an OSC 8 link's escape bytes, and a markdown table is reflowed at pane width.
It applies only to the tree; a run's block prints as without it.

| stage | when | color |
|---|---|---|
| `done` | finished (the issue closed; for a tracker that reports no state, finish ran or the plan archived) or the PR merged | green |
| `shipped` | the ship pipeline ended, the PR not merged yet | faint green |
| `asking` | a driven run stopped on a question; the `ask:` line under it holds the question | red |
| `halted` | a phase failed, partial or blocked, or drive halted the task itself (an idle agent, a refused finish) after its run's last word; `halt:` and `next:` lines under it | red |
| `implementing` | ship running any step but the three below | yellow |
| `reviewing`, `resolving` | ship running review, resolve | orange |
| `judging`, `planning` | ship running judge; capture or plan under way | blue |
| `planned` | the plan is saved, ship not begun | faint blue |
| `waiting` | no run yet, its `After:` base is not done, and a running drive lists it (a bundle's riders included), so the drive holds it for that base; or the record of a running drive this session launched says it parked the task for its base's code or its push (step `on <id>`) or holds its finish until the base is through (step `to finish after <id>`) | faint |
| `not started` | nothing recorded; a task no drive lists keeps `after <id>` as its step while its base is not done | faint |

Color is off when stdout is not a terminal or `NO_COLOR` is set.

A task's run block opens with the ship report's facts block, so a shipped task's facts can be printed again after the conversation moves on.
So does the `run none for` block of a task whose checkout's agent is on something else:

```
task:          FT-414
title:         Show the shipment facts block in f10 status and relay it as yaml
url:         ↗ https://github.com/amberpixels/f10/issues/414
branch:        FT-414/show-the-shipment-facts-block-in-f10
pr:          ↗ https://github.com/amberpixels/f10/pull/416
commit:        1a2b3c4
plan:          /home/me/code/f10/.f10/plans/FT-414.md
judge plan:    /home/me/code/f10/.f10/verdicts/FT-414/1.md
review 1:      /home/me/code/f10/.f10/reviews/FT-414/1.md
```

A row with no value is left out.
Every row but one is read locally.
The title and the url come from the task refs f10 already read, so a run that lost its url gets it back from there, in `--json` too.
The head's short sha shows once the branch has a commit of its own.
The plan, the handoff and the demo report (`demo/<id>/report.html`) show when their files exist.
The PR comes from the ledger's `pr_opened` event.
A project whose PR adapter runs a script around `gh` leaves no such event.
So for a task the ledger saw pushed, status asks the host for the PR by branch.
Each judge verdict and each review findings file gets a row of its own after `plan` and `handoff`.
A verdict's row is `judge <subject>` (task, plan or change), with the file's number added when a subject was judged twice.
A findings file's row is `review <round>`.
Verdicts from before the open series are an earlier PR's and stay out, as the judge gate drops them.
`--json` carries the same files as `verdicts`, each with `subject` and `path`, and `reviews`, each with `round` and `path`.
`--json` also carries the whole block as a `shipment` object.
It sits on each run entry, repeated per run as `agent` is, and on the no-run block.
Its keys are `task`, `title`, `url`, `branch`, `pr`, `pr_state`, `checks`, `commit`, `plan`, `handoff`, `demo`, `verdicts` and `reviews`.
Each is left out when empty, except `task`.
`pr_state` is there only when the host was asked.
`checks` holds an open PR's head checks, as `f10 pr checks --json` prints them, when the host answered.
The flat keys a reader written before the object relies on stay beside it.
A run entry keeps `verdicts` and `reviews`, and the no-run block keeps those plus `plan`, `pr` and `pr_state`.
A run entry's own `plan` key is the plan phase's status, not the plan file.
The run's step rows go on under it as `label: value`, in the same value column, so the whole block reads as one table, and `--plain` prints the same block.
With a run under it, the plan file's row is `plan file`, because the run's `plan` row holds the plan phase.

A finished task, its branch gone, prints the same block, read from what `f10 finish` archived.
It shows the newest archived plan and handoff.
It then shows a `review <round>` row per findings file of the series its last PR closed (`reviews/archive/<id>.<n>/`).
It then shows a `judge` row per verdict written since the series before it closed, and the demo report.
`pr state` and the gates as step rows follow, and `--json` still prints its one `done` row.

The `pr state` row shows whenever the host was asked about the branch's PR, a live run included.
It holds the PR number and its state.
For an open PR it adds its head's checks as `f10 pr checks` reads them, such as `#128 open · checks red (lint)`.
The failed jobs are named in parentheses, or the running ones while the checks are pending, and `--json` carries the whole read as `checks` on the shipment.
The host reads stop half a second short of a caller's deadline, such as the status hook's.
So a slow host leaves the row without its checks, or leaves the row out, and the rest of the block still prints.

`f10 status <id>` lists the task's **gates** under the `ship` row, while the run is going and after it finished.
The block of a task with no branch left prints them as step rows under `pr state`:

```
ship:           running: review:local
  implement:    done
  cleanup:      done
  pr:           done
  review:       local: round 1 running (in ship) · remote: upcoming
  judge:        change: upcoming
  pr:           upcoming
```

- **The flow** is ship's entries as `f10 state pipeline` recorded them.
  Without those, it is the entries after `plan` of the flow the branch records (`branch.<name>.f10-flow`).
  Without that, it is the flow of the newest drive record holding the task that is neither cancelled nor done.
  A stopped drive's record counts only while the task has no branch.
  Else the flow is `default`.
  It is followed by `finish` when the branch carries the finish record a drive writes (`branch.<name>.f10-finish`, below).
  The flow is read at the pace the branch records (`branch.<name>.f10-pace`), else the pace of the drive record holding the task, else the default.
  An entry that pace skips is no row.
  A `judge` before `implement` is the **plan** judge, any later one the **change** judge, and `plan` and `resolve` are no rows of their own.
- **A plain step** is `done` before the step ship is on.
  The step ship is on takes the run's status, such as `running` or `blocked`.
  The rest are `upcoming`: in this run's flow, not reached, and nothing running or waiting.
  Once ship is done, a step it never entered is `skipped`.
  A driven ship parked on its base (`f10 state wait`) reads `waiting on <id>` at that step, and the parent's row `waiting` with step `on <id>`.
- **Review** has a local and a remote review.
  Each reads `upcoming`, `running`, `open`, `settled` or `unknown`.
  `running` shows its round, and `in ship`, `driven` or `manual`.
  It reads `resolve running` while a resolve settles it.
  In `review:local+ci` it reads `round N in, waiting for the other review` once its own findings landed.
  `open` reads `N open of M found`, or `open (not handled)` on the host.
  `settled` reads `3 found (2 fixed, 1 skipped)` or `clean`.
  A review, resolve or judge started outside ship counts from its `run_started` on the task's branch or checkout.
  It counts until its outcome lands, its session stops or starts another skill, or the state TTL passes.
  `ci` in its arguments makes it the remote review's.
  Inside ship, that review reads `resolve running` once a round lands after ship entered the review.
  The remote review is in the flow when an entry names it, or when Review declares a reviewer and the flow opens a PR.
  An entry names it as `review:ci`, or as both in one `review:local+ci`, which shows both on the one review row.
  A `ci?` entry names it only where it would run.
  Its host is asked under a 5 second timeout.
  Past that it reads `unknown (timed out)` and the rest still prints.
- **Finish**, on a branch a drive finishes, reads `upcoming` until ship is done, then `queued (drive)`.
  It reads `running (drive): <action>` while the driver's finish works, such as `merging #204`, `pulling main` or `removing worktree`, and then `done`.
  A finish held for its base reads `queued (drive): waiting for <id>`.
  A finish whose pull was deferred is `done` and the task `finished`, with the note under it in the drive's report.
  A drive that ran a finish retries the pull each tick and prints a cause once.
  The driver records this on the branch as `branch.<name>.f10-finish`: `queued` or `running: <action>`.
  The record's presence is what puts the finish row there, so a drive that stops short of finish sets it back to `queued` rather than removing it.
  Whether the driver still runs is read from the task's lease (**Drives**, below), never from the record.
  Without the lease only the record's presence counts, so a halted or killed driver leaves the row `upcoming`.
  A parent's row puts the same text in its step column while the task is `shipped`, and `f10 board --state`, once the task's ship run is done, names it as the next move (`drive: merging #204`, `drive finishes it`) in place of `f10 finish`.
- **Judge** reads `upcoming`, `running`, or its outcome and reason.
  After the outcome, in parentheses, comes what the ship run did with it, read from the verdict file's `decision:` line.
  Examples are `proceed with changes (2 built) - <reason>`, `(folded into plan)`, `(refactor built)`, `(blocked)` and `(waiting on an answer)`.
  `continue`, or a file with no `decision:` line, adds nothing.
  `--json` carries the `decision` and the `changes` count.
  A judge, or a local or CI review, that the flow does not declare shows once one ran.
  Otherwise its row reads `not in this flow`.
  The **task** judge is the one the stale step runs on a task older than `stale_after`, and any verdict whose subject is the task.
  It is in no flow, and shows nowhere until it runs.
  It leads the gates once it has run, or while the plan phase is on it (`running`, `blocked`).
- **Outcomes** come from lore (`docs/lore.md`), when lore is at least as new as the files and holds the latest round's findings.
  Lore gives findings by `source` and verdicts by `judged`.
  Otherwise the files answer, as after a missed lore write or for a run from before lore.
  Each `reviews/<id>/<round>.md` is counted by its verdict lines: `fix`, `skip`, and open for one still waiting.
  Each block counts for the review its `source:` names, else for the header's.
  The header's is remote when `reviewer:` names only a remote identity, so a file naming both (`local, claude[bot]`) counts once for each review.
  The newest `verdicts/<id>/<n>.md` gives the judge's outcome.
  It counts as the task or plan judge's when its `subject:` is the task or a plan.
  A task with a branch reads its open series only: lore's `<id>.<n>` change, and the verdict files written since its previous PR's series closed.
  A task with no branch left reads the newest change lore holds.

A parent's rows carry the same gates as one column after the step (a `gates` line under `--plain`), such as `󰚔 L 3/4 · R open · 󰊛 J proceed`.
`L` and `R` are the local and CI reviews, each as fixed of found, `N open`, `clean` or `ok`.
`TJ`, `PJ` and `J` are the task, plan and change judges.
`-` is a review the flow does not run, `…` is running, and `?` is unknown.
A review or judge the flow leaves out is left out of the column.
The icons are Nerd Font `md-*` glyphs, the only range Claude Code's output keeps one cell wide.
`󰚔` goes before the reviews and `󰊛` before each judge.
In the PR column, `󰓂` goes before an open PR and `󰘭` before a merged one.
An open PR whose head is red reads `checks red` beside its number, from the rollup state the request search already returns, so the column costs no extra host call.
That rollup counts every check, while finish counts only the required ones where the target branch declares any, so finish may still merge a PR the column marks.
`F10_STATUS_GLYPHS` replaces all four, in that order (open PR, merged PR, review, judge), for a terminal without the font: `F10_STATUS_GLYPHS='PR MR rv jd'`.
`--json` holds them in a `gates` array, on a run and on a row.
`--json` prints the rows as an array.
Each row has `id`, `title`, `url`, `depth`, `after`, `stage`, `step`, `pr` and `pr_state`, and `checks`, `note`, `next` and `ask` where set.
`checks` is an open PR's head rollup: `green`, `red` or `pending`.

A task with no children reports as before: its live run, else its plan and PR.
A task with no branch left (`f10 finish` removed it) prints its facts block (**Status**, above) once it finished.
It counts as finished when the ledger, the tracker or the plan archive says so.
Then `--json` prints a one-row array.
Otherwise it fails as before, with `no run to read`.
A task whose checkout has its branch is read from there first, so only a task with no run to read asks the host for children.

## Drives

Every `f10 start` that runs the flow is a drive: `f10 start 42 43` runs both tasks from the main session.
Each task waits for its one base to have code, every task released that way runs at once, and the driver exits when every task is through, one needs a human, or nothing else can move.
The driver is this binary rather than a Claude session: ordering, prompting and polling are deterministic, and a session polling an agent pays a round trip and a slice of context per check.
The driver runs in the per-user [daemon](#daemon), not in the process that typed the command: that process prints the drive's log as it grows and exits with the drive's exit code, so closing or clearing the session that ran `/f10:start` leaves the drive running.
Ctrl+C stops only the printing, naming `f10 daemon logs <n> -f` to follow it again; rerunning the same command follows the drive already running instead of starting a second one.
`--foreground` runs the driver in this process, as the daemon itself runs each drive; it is for tests and debugging, and on a platform without unix sockets it is how every drive runs.

**The record.**
Every drive writes `<storage root>/drives/<n>.yaml` and prints `drive <n>` first.
The record holds the command line without `--answer`, `--every`, `--parallel`, `--resume` and `--foreground`.
It holds the tasks with their `:profile`, the flow, the run-wide profile and the cap (`parallel`).
It holds the answers given and not yet taken, and each answer taken with its task, skill, text and time.
Per task, it holds the skill in flight and its outcome: its report row and exit code.
Rerunning the same command line picks up the newest record started with it that is neither cancelled nor done (every task through with code 0); `f10 start --resume <n>` picks up record `n` by number and takes only `--answer`, `--every` and `--parallel` beside it.
A rerun and a `--resume` keep the record's cap, unless they pass `--parallel`, which rewrites it.
A driver still running reads the new cap at its next poll.
An answer kept in the record, because the drive stopped before an agent took it, is given again on the resume, unless the resume gives that task another.
The branch's `f10-drive` and `f10-flow` keys stay what position and flow are read from.
The record keeps what a branch cannot.

**The lease.**
Before anything opens, the driver takes an exclusive, non-blocking `flock` on `<storage root>/drives/locks/<id>.lock` for every task not already finished, its pid and drive number written into it, and one for itself (`drive-<n>.lock`).
The key is the task's lead id, so two attempts at one task share it.
A task whose lease another process holds ends at once with the row `owned by drive 3 (pid 4242)` (or `owned by f10 finish (pid …)`, `owned by the f10 watcher (pid …)`), and its dependents halt with it.
A task's lease is released when the task ends, so a halted task can be finished by hand while the drive runs on; the kernel releases every lease when the driver's process ends, however it ends.
A second drive on a record whose driver still runs is refused.
Readers (`f10 status`, `f10 board`) probe the lease with a shared lock for an instant; the driver retries a held lease for about 200ms to absorb that.
On a platform without `flock` nothing is exclusive.
Lock files are never deleted.

**List and stop.**
`f10 status --drives` prints the newest 20 drives (`--all` for every one), each with its time, `running (pid …)`, `stopped`, `done` or `cancelled`, its command line, and a row per task: its outcome, else `queued` or `running <skill>` while its driver runs, else `stopped at <skill>` or `not reached`.
A drive that is not running, a cancelled one included, has each row not yet finished checked against its task.
The check is the rule drive and status use.
A task with no branch here or on origin and no live plan reads `finished` when its issue is closed.
An archived plan counts only for a tracker that reports no state, because an open issue may ship another PR.
A finish the ledger recorded counts the same way, so `f10 status` never calls a task done while `f10 start` would open its next PR.
The tracker is asked only when no branch or live plan decides.
Finish's own `already finished` is narrower, because it asks whether finish got as far as archiving the plan.
The corrected row is saved when the drive's lease is free, and only shown when another process holds it.
A row only ever moves to finished, because only a rerun knows a drive's next step for a halt settled halfway.
`f10 stop <n>` marks the record cancelled.
`f10 stop <id>` does the same to the running drive holding that task, and names every task it stops.
A running driver stops at its next poll, after any finish it is in the middle of.
It ends every task still going with the row `cancelled` and exits **5**.
Nothing more is prompted, and worktrees, branches and agents stay as they are.
A cancelled record is never picked up again: the same command line starts a new one.

1. **The arguments.**
   Task ids first, or a range `42 -- 47` (every id between, inclusive, at most 50).
   The flow is `default`, or the one `--flow <name>` or `--local` names; an undeclared name fails with the `f10 flow` list.
   It gives the skills the driver prompts, which are never typed and never stored.
   They are `plan` when the flow leads with it, then `ship`, which runs every other entry.
   Then comes `finish`, when the flow ends in a `finish` entry that neither the run's pace nor the project's ceiling turns off.
   A task whose ship ended as a spike (its spike report written) is closed right after ship, whatever the flow says, since the close merges nothing.
   Its branch gains the finish record, and a dependent then starts on the default branch.
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
   A task with no branch whose issue is closed, or whose plan sits in main's `plans/archive/` on a tracker with no state, is finished and skipped.
   An open issue is never finished, so a task that ships in several PRs starts its next one.
   A listed parent with no branch is replaced by its subtasks (the parent bullet under **Start**).
   A bundle member with unfinished subtasks is refused, since one branch would ship the whole umbrella.
   `--after` and `--base` set one task's base, so a list, a range or a parent that expands to several tasks refuses them.
3. **The graph.**
   Each task has at most one base: the `branch.<name>.f10-after` its branch recorded once started, else the `After:` line in its body.
   Where the two disagree, the branch wins, with a note.
   The map is rebuilt on every run, and nothing about it is stored.
   A base outside the list is stacked on when it has a branch, and is refused when it has none and is not finished.
   Tasks that wait on each other in a circle are refused too, both before anything is created.
   A task stacked on a base outside the list ends at its PR with a note, because finish refuses a PR stacked on an unmerged branch and only a listed base parks it until that base lands.
   Within the list the graph sets the order: a base runs first wherever it is listed, and among tasks ready at once the one listed first goes first.
4. **Per task.**
   A task starts once its base releases it.
   That happens when the base's branch has a commit of its own, or when the base is through: finished, or every one of its skills run.
   Its own commits are what its branch has beyond the default branch, `origin/<default>`, its own base's branch, and the commit it was created at (its oldest reflog entry), merges left out, so a lagging local default branch, a squash-merged grandparent or a catchup merge never releases it early.
   `f10 pr catchup` prints the same answer as its `code:` line, so ship's guard and the driver agree.
   Implement never commits, so a first commit means a commit or pr step ran with verify green; the driver reads it from local refs on each poll, so a branch never pushed counts.
   A `--plan` run never commits, so there an opened base releases its dependents once its plan is saved, even if it halted after saving it.
   A dependent's own `finish` still waits until its base is through, so a dependent never merges before its base; while it waits its row reads `waiting for <id>`.
   Every task that is ready starts in the same pass, up to the cap.
   `--parallel <n>` caps the tasks running a skill at once, by default half the CPU cores, from 2 to 8.
   A task keeps its slot from one of its skills to the next.
   It frees the slot once it asks, waits for its base's first commit, or waits at finish for its base.
   Open agents are capped too, at twice the cap, because an idle session still holds its memory.
   The agent limit counts the tasks under way and the tasks asking, including agents an earlier run of the drive left open.
   A done or halted task's agent does not count, because the drive is finished with it.
   Counting it would also stall a drive that runs no `finish`, whose done tasks keep their agents open, so no task past the limit would ever start.
   A task past either limit shows as `queued` in the report, `f10 status --drives` and `f10 status`, and starts in the drive's order as slots free.
   A task whose agent an earlier run left open goes first, because it adds no session.
   A turn already running when the driver reaches the task is tracked and counted even past the cap, because the driver cannot stop it.
   The worktree, workspace and agent are opened as `f10 start` opens them, with the body's `After:` as the base: a base still on its branch stacks the new one on it, and a finished base leaves it on the default branch.
   A plan the main checkout saved for the task is read in place under start's rules.
   A checkout Herdr already shows, as an earlier `f10 start` leaves it, is adopted: its workspace, its agent, or a new agent in its idle shell.
   Run state recorded before the driver started that new agent was written by one that is gone, and halts nothing.
   From then on the task's workspace is the one opened, by id, whatever else shows the same checkout.
   Nothing is prompted at open.
   The skills run from the task's position.
   The position is the last skill the driver saw finish, recorded on the branch as `branch.<name>.f10-drive` in local git config.
   Else it is what the task's run state proves, such as plan done or ship done.
   Ship counts as done only when a ship turn wrote the run (its `skill` is `ship`) and the branch carries what the flow delivers.
   That is commits of the branch's own past its base, not counting merges and `Step: catchup` commits, and an open PR when the flow has `pr`.
   A spike's report counts in place of both.
   Anything less sends ship.
   A plan the worktree reads when the driver starts its agent counts as plan done, so the task goes straight to ship rather than planning again.
   A task whose branch already records a flow (`branch.<name>.f10-flow`) keeps it, with a note when the run named another; a recorded flow `project.md` no longer declares gives way to the run's.
   `finish` runs in the driver, exactly as `f10 finish` would from the main checkout.
   It records what it is doing on the branch as `branch.<name>.f10-finish` for `f10 status` (above).
   On any other end, an interrupt included, the driver sets it back to `queued`.
   A PR someone else opened is the driver's own ask at finish, `[local | merge]`, answered with `--answer` like any other.
   Finish takes a keyed answer only for that question.
   An answer to the agent's own question goes to the agent as its `ci` turn, and finish runs again once the triage reports.
   The agent's own question is one its run carries as an ask, as a ci triage's does.
   Each finished task's handoff, the body finish printed under its report, repeats beneath the drive's report as `<id> handoff:` with the body indented.
   So the driving session relays every merged task's list at once.
   Every other skill is prompted to the agent, marked `--driven`, which takes every gap on its default and reuses a saved plan.
   A dependent task's plan and ship name the base task and its plan.
   Ship names its flow unless it is `default`.
   With `finish` among the skills, ship's prompt says the driver finishes the task after it and removes the worktree.
   So the agent's report ends with that next step.
5. **Catchup when the base or the target moves, after finish, and before it.**
   On each poll, a running dependent whose branch lacks its base branch's head, read while the base agent is not working, is marked `branch.<name>.f10-base-moved` with that head; a dependent mid-catchup is not marked.
   Before a task's next skill, when its branch is on origin, the driver fetches once and marks the branch if its pull request's target moved past it, so an open PR never sits behind.
   The mark is `f10-landed` with the default branch's name when the PR targets it, `f10-base-moved` with the base's head for a base branch no task in the drive owns.
   A branch already marked, one its target is already in, or one never pushed is left alone, and a fetch that fails skips the poll.
   The catchup follows `catchup:`, or `catchup_stacked:` for a base branch.
   Once it ends clean the driver pushes a branch origin has, plain after a merge and `--force-with-lease` against origin's tip when it sent the catchup after a rebase or an `onto`, so the PR carries the integration before the next skill runs; a rejected push halts the task with the branch caught up locally.
   A branch origin moved on while the catchup ran is not pushed: the task halts, saying to bring `origin/<branch>` in and run `/f10:catchup` there, since the rebased branch lacks what was pushed meanwhile.
   The drive saves what it read when the catchup went out in its drive record, so a resumed drive leases the push the same way.
   With nothing saved, a rewritten branch is never force-pushed, and the task halts instead.
   A catchup that brought in commits origin does not hold yet, a base merged from its local branch, is not pushed: the next `pr` entry or finish pushes it, so another task's unpushed work never rides into this PR.
   `finish` marks each branch that depended on the finished task `f10-landed`.
   Before a marked task's next skill, the driver catches the branch up itself, never interrupting a turn.
   It fetches, merges or rebases by the method `f10 pr catchup` prints.
   Then it runs verify, clears the marks and pushes as above, with no agent turn.
   It runs verify when the frontmatter declares a `verify` list, when verify is detected, or when the Verify heading is backticked commands joined by ` / `, as `f10 init` writes it.
   With a heading in any other prose, the driver still integrates, and verify becomes the agent's turn.
   The driver prompts `/f10:catchup --driven` and says the base came in cleanly.
   It asks the agent to run verify, fix a red by the branch's intent, and clear the marks once verify is green.
   The drive says once why the agent verifies.
   It reads the cleared marks as verify green.
   A PR whose base on the host is not the base the task was started on (retargeted by hand) is never caught up.
   The task halts naming the PR and its base.
   A PR whose base cannot be read on a poll, because the host did not answer, is not marked on that poll.
   A conflict leaves the merge or rebase in progress and prompts `/f10:catchup --driven` naming the base and the conflicted files.
   A red verify after a clean integration prompts it naming the failing command and a log of its output.
   After that turn git decides whether the catchup settled.
   It settled when no merge or rebase is in progress and no conflict marker is in a path the branch changes.
   The branch must also hold its base, and verify must be green when the driver runs it again.
   Anything left is a failed attempt, so the next prompt names what is left, such as a rebase still at commit 2 of 3.
   Before a task's own `finish`, the driver fetches and tries origin's copy of the branch against `origin/<default>` in memory (`git merge-tree`), as the host will; a branch origin does not have is tried against the local default branch, where finish merges it.
   A conflict, as a sibling that merged first leaves one, marks the branch `f10-landed` with the default branch's name and sends the same catchup.
   Once it ends clean, the push above reaches origin and then finish merges.
   A catchup attempt's fingerprint is the target sha plus the conflicting paths with each side's blobs.
   It leaves out the head, because a merge moves the head without making progress.
   A branch that conflicts again after a catchup is a failed attempt naming the files.
   If the target moved or other files conflict, the fingerprint differs, so the count starts over and the drive catches up again.
   The third catchup in a row with the same fingerprint halts the task.
   After a catchup the caught-up local branch is what is tried, since origin's copy is the tree before it.
   A ship stopped by its catchup guard gets the same catchup.
   The guard marks the branch and records the stop as the guard's (`f10-state.sh stop guard`).
   Then ship is prompted again at the entry it stopped on (`resume at <entry>`).
   Its dependents wait, and a second guard conflict counts by the same fingerprint.
   The driver keeps its own copy of that stop's entry and place on the task's record (`guard:`) until ship is prompted again.
   So it resumes there even when the run file no longer carries the guard's kind.
   The copy counts only while the run is still stopped at that entry and place, so a later stop elsewhere is not read as the guard's.
   A ship that failed for another reason under a mark is not the guard's stop, and halts.
   The catchup's end is the `done catchup` signal its report writes into run state (`f10-state.sh done catchup`), dated after the prompt.
   Git, not the signal, says whether it settled.
   A catchup writes no phase.
   From its skill's start to its turn's end, the hooks record the turn on the run (`turn catchup`).
   In that window `f10 state` refuses `set` and `stop` with exit 1, and the Stop hook promotes no running phase.
   An idle agent without it is a stall after the same grace a ship or plan turn gets, and is never read as done.
   A catchup git shows settled goes on to verify.
   Any other catchup halts, as a stalled ship or plan turn does.
   The drive's own catchup resumes the task once git shows it settled and the driver's verify is green.
   The driver clears the marks then.
   A red verify the agent's turns do not fix counts as a failed attempt, so the same command red against the same target three times halts.
   When the agent verifies (a prose Verify heading), the task resumes only on a clean end.
   A clean end has the signal there and the mark cleared, which catchup does once verify is green.
   It also has no merge or rebase in progress, and a clean tree.
   A driven catchup never asks.
   It settles an ambiguous conflict by the task's intent.
   It records each choice as a lore decision and as a line under a Catchup heading in the PR body.
   A catchup that fails, or makes no progress, counts as a failed attempt.
6. **Waiting.**
   It looks once a run file, prompt mark or open wait about one of its worktrees is written (a step boundary, a turn's end, a prompt taken), or its record is (a cancel), checked every second; other sessions' writes to the shared state dir do not wake it. It also looks and every `--every` (30s by default) regardless; each look reads two signals once for every task in flight: Herdr's workspace list, and the live runs, the newest one recorded against each worktree.
   A turn has begun once Herdr shows the agent working, the run reports after the prompt, or the agent's session left a prompt mark after it: the `UserPromptSubmit` hook writes `<session>.prompted` beside the run file, naming the checkout and the time, so a turn that began and ended between two looks still counts as taken once the mark is five seconds old.
   Herdr's `done` lasts from the previous turn until someone looks, so it proves nothing.
   No sign within two minutes halts the task, because a prompt never taken may sit under a startup dialog that typing it again could answer.
   A workspace that still shows no agent status by then halts it too.
   Once the agent is idle again, the run says how the turn ended:
   - **blocked with an ask** - the question is printed with its options and kept in the inbox (**Inbox** below), naming this drive.
     The driver keeps driving every other task: a guard stop still gets its catchup, a base still releases its dependents.
     Once nothing else can move (every other task ended, asking, or held on its base), it prints every task's ask and the command that answers them all, and exits **4**;
   - **blocked waiting on its base** - the task parks, with no ask and no halt, and its row reads `waiting on <id>`.
     This is `f10 state wait`: a driven ship that found its base without code before `implement`, or not on origin before `pr`.
     A wait for code ends once the base is released and its branch head has moved past the one the ship found.
     Then ship is sent again with `resume at implement`, and the same head never sends it twice.
     A wait at `pr` ends once the base's branch is on origin or the base has finished, and ship is sent again with `resume at pr`.
     A wait on a base this drive does not run, or one already through, halts the task with the run's note;
   - **blocked without one** - a decision, not breakage, such as a judge stop or a stale task's verdict.
     The task halts with the run's note and next, since sending the skill again would only roll the decision again;
   - **failed** or **partial** - the driver reads the branch, since the turn may not be the one it sent (a ship typed by hand while it followed plan).
     Each skill the branch shows done is recorded and passed, so a ship stopped by its guard gets its catchup.
     A stop the branch does not get past is a failed attempt of the skill in flight;
   - **still running** under plan or ship, or a catchup with no `done catchup` signal since its prompt, a minute after the agent went idle - the agent stopped without reporting.
     An agent whose session launched background work its hooks still count as pending is not idle.
     Such work is a background command, a Monitor or a subagent.
     The agent reads as working for up to 30 minutes from that launch, so a long verify or a wait on a subagent is no stall.
     Past that, or with no background work, the driver reads the branch the same way.
     A branch with its plan, commits of its own, a settled tree and an open PR carrying them counts as shipped.
     Anything less halts the task naming the workspace, because the agent is usually waiting on a permission dialog in its pane, and typing the skill again could answer it.
     Found at prompt time, a run still running halts the same way and says how to leave a cancelled run.
     Quit that agent and rerun, and a new one is started in its place;
   - anything else - the skill is done, recorded on the branch, and the next one is sent.

   **When a turn fails.**
   A failed attempt sends the same action again at the next look.
   Three failures in a row of one action, with nothing changed between them, halt the task with the last reason and a line per attempt.
   The things compared are the branch, origin's copy, the marks, a merge or rebase in progress, the plan file, the PR, the review rounds and change verdicts, and the run's phases.
   The halt and its attempts go to the ledger.
   A failure after which any of those moved starts the count over, and so does a step that goes through.
   A catchup's attempts compare the conflict it leaves instead, because a catchup that merges moves the branch every time.
   Only the entry it guarded going through clears them, not a clean catchup.
   A catchup turn that leaves the tree unsettled is counted apart from them.
   So a red verify and an unsettled turn that take turns do not start each other's count over.
   A halted task halts every task that waits on it.
   One not started yet never starts, and one already running halts before its next skill, its work left on its branch.
   Every other task runs on, and the driver exits **5** once nothing can move.
   The count lives in the running drive only, so a rerun or a resumed drive starts it over.

   **Every prompt checks first.**
   An agent working is attached to, not prompted.
   A `plan` or `ship` its run already shows `done` (or `prior`) is counted done and recorded with no prompt, as when a driver stopped mid-ship and the agent finished the step before the rerun saw it.
7. **The report.**
   Progress lines as it goes, an end included: a halt or an ask is printed the moment the driver sees it, not only beneath the report.
   Then one row per task in graph order: `already finished`, `finished`, `done: <skills>`, `waiting on <id>` (parked for its base's code, or for its branch on origin), `waiting for <id>` (its finish waits on its base), `asked at <skill>`, `halted at <skill>`, `halted: base <id> halted`, `owned by drive <n> (pid …)`, `cancelled`, `not reached`.
   Exit 0 when every task went through its skills, 4 when any task asks, else 5 when any halted.
   Exit 6 is a drive stopped for a daemon upgrade, and exit 8 a drive asking its daemon to start it again.
   Only the daemon and the command following it see them.

Rerunning the same command, or `f10 start --resume <n>`, is the resume: the record is picked up, and each task's position is read from its own branch and run state.
With `--answer "1. … 2. …"`, the first task stopped on a question gets the text as its agent's next prompt, plain, the way `f10 forward` sends an answer, and the driver polls that turn as it would any other.
Several asking tasks take one answer each, keyed by id: `--answer "42=1. proceed" --answer "45=1. no"`, each sent to its own task's agent.
An answer sent closes the task's ask in the inbox. An answer the inbox already holds for a task goes first: one typed in the agent's pane is followed as the turn it started, with nothing sent; any other is sent as an `--answer` would be, and an `--answer` for the same task goes unused with a note.
A drive whose task was answered after it last looked, while it still held its lease, runs again once it has stopped.
In the daemon it exits 8, and the daemon runs `--resume <n>` in its place.
Outside the daemon, or under a daemon too old to restart it, it prints `f10 start --resume <n>` for the answer.
`f10 stop <n>` closes the drive's open asks.
An agent found mid-turn is attached to rather than prompted again.
The turn is read by what it reports: one that turns out to be a catchup is followed like a catchup the drive sent, so a clean one resumes ship at the entry its stop names.
Two kinds of task are prompted again on a rerun once a catchup settled the branch.
One is a ship the catchup guard stopped before `implement`, `pr` or a `judge`.
That stop is told by the `stop guard` kind its run records, never by the wording of its next.
The other is a driven catchup left `failed`.
The branch counts as settled by the same checks as point 5: no mark, no merge or rebase in progress, and a clean tree.
The prompt carries `resume at <entry>`, so ship starts where it stopped.
A branch still marked gets the catchup first.
A task halted for another reason stays halted until its run changes: settle it in that workspace, or send steering through `--answer`.

`/f10:start` runs the command in the background, answers an exit 4 through `/f10:inbox` (the questions in one questionnaire, the answers written to the inbox, the drive resumed), and prints the final report.

Out of scope: more than one base per task, and ordering tasks by anything but their recorded dependencies.

### Plan

`f10 drive plan <parent | id>...` is plumbing: it proposes how to start the tasks and prints it.
It opens nothing, writes no record, takes no lease, starts no daemon and changes no task.
So the daemon, a hook or a status line may call it.
A parent expands into its open subtasks in `f10 task children` order, a `draft` child left out with a line naming it.
A closed task is left out the same way.
A typed bundle stays bundled.

For each task it reads, cheapest first, the `After:` line and the labels from the tracker.
Next it reads a saved plan's `Kind:`, `Areas:` and `Size:` header lines.
A small plan counts as a quarter of `bundle_max_lines`, marked estimated, because it is one change in one area.
Next it reads the flow `flow_by_area` maps the first mapped area to.
If `--flow` or `--local` names a flow, all tasks share that one.
Last it reads the task's `:profile` and the facts sheet `--facts` names.
A label's kind wins over a plan's, and a plan's over the sheet's, as investigate settles it.
The labels `bug`, `defect` and `regression` read as a bug, and `refactor` and `tech-debt` as a refactor.
`spike`, `research` and `investigation` read as a spike.
The labels `migration` and `risky` mark a task risky.

The rules, in order, each leaving a one-line reason on every task and bundle:

1. **Alone**: a task stays alone when it is a spike, because a spike merges nothing.
   A risky task stays alone, and so does a task with no size (`alone: not sized`).
   A task over `bundle_max_lines` stays alone, and so does a task the user kept alone.
2. **Bundle**: tasks of the same flow, the same agent profile and the same kind class share a bundle when they share a file or an area.
   The kind classes are bugs, and features with refactors, so a regression fix keeps its own PR.
   A bundle carries at most one `After:` outside itself, so a task joins only a bundle with its base, or with its base inside.
3. **Cap**: a bundle stops at `bundle_max_lines` changed lines, in the parent's order, and the task left out says `split: N lines with <id>'s group, over the cap of M`.
4. **Order**: a task the sheet says builds on another, with no `After:` saying so, gets a proposed `After:` edit.
   So do two entries that share a file but cannot bundle, such as a bug and a feature.
   The later goes after the earlier, since side by side they would conflict.
5. **Stages** are each entry's depth in the `After:` graph, the way drive releases them.
   `--parallel` is the widest stage, capped by the `--parallel` given, else by drive's default.

It prints the stages, and the drive command with the drive flags given carried onto it.
It prints `saves about N CI waits and M finishes against starting them one by one`.
It prints the `After:` edits waiting on a yes, and `f10 task update <id> --after <base>` applies one.
When anything is bundled, it prints once that a bundle is one PR, one revert and one review over a mixed diff.
It adds that a later bug is traced to its lead with the riders named.
`--json` prints the same for a skill, the tasks' gathered facts included.
`--hint` prints one line when the rules alone save at least one CI wait, and nothing otherwise.
A bundle the user typed or named with `with` is not counted.
The line names the start command that saves the waits, and `/f10:start` prints it under the tree.

**The facts sheet** is a JSON object keyed by task id, written by a skill from a short read of each task against the code.
An unknown field is refused, so a misspelt one never reads as not sized:

```json
{
  "FT-41": {"areas": ["cli ui"], "files": ["cli/internal/ui/table.go"], "lines": 20},
  "FT-47": {"areas": ["status"], "lines": 40, "buildsOn": "FT-44"},
  "FT-50": {"lines": 50, "risky": "migration"},
  "FT-45": {"alone": true},
  "FT-46": {"with": "FT-45"},
  "FT-48": {"after": "none"}
}
```

`kind`, `areas`, `files`, `lines`, `buildsOn` and `risky` are what a read of the task found.
`lines` is marked estimated in the reasons.
`alone`, `with` and `after` are the user's edits to the proposal.
`with` bundles with that task, whatever the rules say.
`after` is an id, or `none` to drop the line.

## Daemon

`f10 daemon` is one process per user that runs drives, so a drive outlives the session that typed it, and watches the projects tasks were started in (**Watch** below).
Nothing registers it as a service.
Every control-plane command below starts it when none listens.
It exits after ten minutes with no drive running, no project watched and no call.
Its socket serves the control-plane API over Connect ([proto/README.md](../proto/README.md)).
`f10.v1.DaemonService` serves its own lifecycle.
The rest of `f10.v1` and `agents.v1` serve tasks, runs, the board, events and agents.
`WatchEvents` opened with `tail` and no cursor starts at the ledger's newest row, and its first response carries only the cursor to resume from.

- **Where.** The run dir is `F10_RUN_DIR`, else `$XDG_RUNTIME_DIR/f10`, else `run` in the user's f10 dir. The user's f10 dir is `~/.f10`, or `F10_USER_DIR`. The run dir is kept `0700` and refused when another user owns it. It holds the socket `daemon.sock` and the daemon's own `daemon.log`. It holds a log per drive run under `logs/`, with its `.exit` file and an `index`, and finished ones are pruned after seven days. It holds the locks under `drives/locks/`, and `drained.json` between an upgrade's two daemons. It holds `watch.json` (`0600`) for as long as each project is watched: the watched projects with the environment their passes run with, tokens such as `GH_TOKEN` included. Each watched project gets a `logs/watch-<hash>.log`.
- **Starting one.** A client dials the socket; with nothing listening it takes the `start` lock, dials again (another client may have started one), else starts `f10 daemon` detached in its own session and waits up to 5s for it to listen. The daemon holds its own `daemon` lock for life and exits at once if another holds it, so two clients at once get one daemon.
- **A drive.** The client sends the command line, its directory and an allowlisted copy of its environment. The copy holds `PATH`, `HOME`, `USER`, `LOGNAME`, `SHELL`, `TMPDIR`, `LANG`, `LC_*`, `TERM`, `SSH_AUTH_SOCK`, `XDG_*`, `GH_*`, `GITHUB_TOKEN`, `GLAB_*`, `GITLAB_TOKEN`, `HERDR_*`, `F10_*`, `CLAUDE_*`, and the keys the frontmatter's `drive_env` lists. The daemon reads the storage root from that directory. It runs `f10 start --foreground <command line>` in it with exactly that environment, its output going to the drive's log. `F10_DRIVE_FILE` names a file the drive writes its record number to once the record is open. When the drive ends, the daemon writes `<log>.exit` with the exit code and the drive's number. For exit 6 or 8 it also writes the reason. The reason `upgrade` means the next daemon resumes it. `stopped` means it waits for `--resume`. `resumed` goes with exit 8 and means this daemon runs it again. A request for a drive already running in the same storage root (the same command line, or `--resume` of its number) with no `--answer` follows that drive's log instead.
- **Upgrades.** A client says hello first and reads the daemon's version and API version from the reply. A daemon kept by the rule below whose API version differs from the client's is an error naming both versions, never a decode error. Only a client of a strictly newer release (`0.46.0` over `0.45.2`) replaces the daemon. An older client, or a development build, uses the daemon running and says which version it runs. `f10 daemon stop` is how to replace one by hand. A client following a drive never starts a daemon while another drains. The replacing client starts the next one. A follower starts one only when none has listened for 5s after the old one is gone. A replacing client asks the old daemon to drain. Each drive stops at its next tick, after any finish it is in the middle of, ends no task, releases its leases and exits 6. The old daemon writes those drives to `drained.json` (`0600`) and exits. Each entry holds the drive's storage root, directory, number and environment. The client then starts its own binary's daemon, which runs each drained drive again as `--resume <n>`. Every client that was following one follows it into the new daemon, whatever its version. A drive that has not stopped after ten minutes is stopped outright and not resumed. Rerunning its command resumes it.
- **Releases before the Connect API** spoke JSON on the same socket. A new client that finds such a daemon drains and replaces it through that protocol, and the new daemon resumes its drives from `drained.json` as above. A new daemon answers an old client's hello, so the older client keeps it by the rule above, and answers its `run`, `status` and `watch` with "update f10".
- **The CLI as a client.** `start`, `forward`, `status`, `inbox` (its list, `answer` and the terminal walk), `stop`, `status --drives`, `finish`, `board`, `task create` and `task read` parse their flags, call the API and print the reply. So a remote client gets what the terminal gets, from one implementation. `f10 start` runs its drives in the daemon, and a discussion or a pickup through the same API.
  Their output, `--json` shapes and exit codes are what they were in process, with one difference in timing. A write prints what the daemon wrote for it: its stdout, then its stderr. So the lines a command reports while it works, such as `f10 finish` waiting on checks, print once the call returns, not as they happen. A read renders the model the daemon read, with the command's own renderer. A refusal carries its exit code and what the command printed before it failed.
  Each call passes where it runs: its directory and `-C` checkout, its Claude session and its Herdr workspace. It also passes its environment, and whether its output is a terminal, for links. The daemon honors that only on the socket.
  When the daemon cannot start, the command fails with the daemon's error. Off unix, where no daemon runs, the commands serve the same API in their own process.
  `f10 finish` run from the task's own workspace closes that workspace itself, after it printed the report, because a close inside the daemon would end the command before it printed anything.
- **Left in process.** The step plumbing that runs inside an agent's worktree stays local, because routing it through the daemon adds a failure point to every step. That plumbing is `trace`, `lore`, `pr create`, `pr edit`, `review round`, `task ref` and the like.
  `f10 inbox hook` and the status the Stop hook prints stay local, because a daemon start would break their time budget.
  `f10 start --foreground` is the drive the daemon runs, so it cannot call the daemon. `f10 drive plan` and `f10 janitor` stay local too.
- **Herdr from the daemon.** A call from a local command runs its `herdr` commands in that command's own session, so two sessions calling at once never cross, and keeps that session as the newest. The other handlers that reach agents run `herdr` with the environment of the newest local call, drive or watch registration made inside Herdr. That environment is kept in `session.json` (`0600`) across restarts. With none kept, they answer that no Herdr session has registered.
- **A crash or a reboot** leaves running drives stopped, their records intact: rerunning the drive's command resumes it.

`f10 daemon status` prints the pid, version and start time.
When **Remote** below turned the tailnet listener on, it prints the listener's address, or why it is not listening.
Then it prints each drive with its project, number, pid and log, and each task's standing from its record.
Then it prints each watched project: its last pass, that pass's exit, and its log.
`f10 daemon stop` drains the daemon the same way but keeps no drained list: each drive stops at its next tick, a client following one ends with `f10 start --resume <n> goes on`, and nothing resumes until someone runs that. `--now` stops each drive at once, and cuts a drain in progress short: the daemon keeps answering `status` and `stop` until its drives are gone.
`f10 daemon logs` prints the daemon's log, `f10 daemon logs <n>` drive `n`'s newest log in this project, `-f` following it until the drive exits.

### Remote

`f10 remote` serves the daemon's API to your other devices on the tailnet, through the same handlers as the socket.

- **The listener.** `f10 remote on [--port N]` turns it on (port 7410 by default) and starts the daemon. `f10 remote off` turns it off. The setting is `remote/config.json` in the user's f10 dir. The daemon binds only this machine's Tailscale IPv4 address. On Linux that is an address in 100.64.0.0/10 on `tailscale0`. On macOS it is an address in that range on a `utun` interface that also holds a Tailscale IPv6 address in `fd7a:115c:a1e0::/48`. The range alone is not enough, because it is also carrier-grade NAT space that a Wi-Fi interface or another VPN's `utun` can hold. Nothing listens on any other interface. It closes any connection whose peer is outside that range before reading a byte, because macOS accepts a packet for a local address on any interface. It checks the setting and the address when it starts and every minute after. So it binds once Tailscale comes up after login, rebinds when the address changes, and closes when the setting goes off. A connection idle for a minute is closed.
- **HTTPS.** The listener serves HTTPS with this machine's Tailscale certificate, because a browser installs a web app, runs its service worker and shows notifications only over HTTPS. WireGuard already encrypts the traffic either way. The daemon finds the `tailscale` CLI on PATH, else inside `/Applications/Tailscale.app`. It reads the machine's MagicDNS name from `tailscale status --json`. It runs `tailscale cert --cert-file - --key-file -` and writes the pair it prints to `remote/tls/` (`0600`). The pair comes on stdout because the App Store build's sandboxed CLI cannot write outside its own container. It renews the certificate 30 days before it expires without rebinding, and retries a failed fetch at most once an hour. Until a certificate exists, or when the tailnet has HTTPS certificates turned off, it serves plain HTTP. The admin console's DNS page turns them on. `f10 daemon status`, `f10 remote on` and `f10 doctor` say why.
- **Device tokens.** `f10 remote token add <name> --scope read,answer` prints a token once. `remote/tokens.json` (`0600`) keeps only its SHA-256 hash, its scopes and when it was made. `f10 remote token list` shows names and scopes, and `f10 remote token revoke <name>` deletes one, which refuses that device's next call without a restart. Every call on the tailnet carries `Authorization: Bearer <token>`: a missing or unknown token is refused `unauthenticated`, and a token without the method's scope `permission_denied`, naming the scope.
- **Scopes.** A method missing from this table is refused on the tailnet. The socket keeps full access, because only the local user can open it.

  | scope | methods |
  |---|---|
  | any token | `Hello` |
  | `read` | `GetDaemonStatus`, `GetTask`, `GetRunStatus`, `GetBoard`, `WatchEvents`, `ListAsks`, `ListWorkspaces`, `ListAgents`, `GetAgentStatus`, `ListArtifacts`, `GetArtifact`, `ListProjects`, `ListDrives` |
  | `answer` | `AnswerAsk` |
  | `steer` | `SendText`, `Forward` |
  | `start` | `Start`, `Drive`, `CancelDrive`, `CreateTask` |
  | `finish` | `Finish`, `CloseWorkspace` |
  | socket only | `StopDaemon`, `WatchProject` |

  A remote call's environment fields are ignored, because a remote client has no local session to pass.
  `Hello` returns the caller: its token's name, scopes and the procedures they allow, so a client hides what it cannot call.
- **The web client.** The listener also serves a web app at its address, `https://<machine>.<tailnet>.ts.net:7410/`, which `f10 remote on` and `f10 daemon status` print.
  Its files load without a token, because they hold no data. Every API call still carries one.
  You sign in once with a device token, and the browser keeps it. A refused token signs the page out.
  Once the certificate is in place, the `http://` address redirects to the `https://` one. The browser keeps the token per address, so you sign in once more there.
  The app has these screens:
  - **Runs**: every live run, grouped by project, with why a run stopped and its PR's checks.
  - **Inbox** (`/inbox`): the open asks, answered in place by tapping an option. The old `/asks` address redirects there until v0.68.
  - **A task**: its flow and a box that sends a command or a note to its agent, as `f10 forward` does (`Forward`). It also shows the plan, the spike report and the handoff. It shows the verdicts, the findings and the demo report. The files are read through `ListArtifacts` and `GetArtifact`, by kind and name, never by path.
  - **Board**, **Start** (start one task or several, a range, a plan-only run or a discussion) and **Capture** (create a task).

  A screen or button that the token's scopes do not allow is hidden.
  Live updates come from `WatchEvents`.
  Once you allow notifications (**Notify me**), a new ask shows a notification while the page or the installed app runs. Nothing is pushed to a closed app.
  Over HTTPS, add the page to the home screen to install it.
  The app's source is `ts/web`. `just web::build` rebuilds `cli/internal/webui/dist`, which the binary embeds.
- **MCP.** `f10 remote on --mcp` also serves the API as MCP tools at `/mcp` on the listener's address, beside the web client (**MCP** below). A bare `f10 remote on` turns that off again. Each request carries a device token as above. The tools it lists are the ones that token's scopes reach, read again on every request. So a revoke or a narrower token applies at once. With the setting off, `/mcp` is not served.
- **Always on.** With the listener on, the daemon does not exit when idle. `f10 remote install` writes and loads a launchd agent (`~/Library/LaunchAgents/io.github.amberpixels.f10.daemon.plist`) on macOS, or a systemd user unit (`~/.config/systemd/user/f10-daemon.service`) on Linux. That service runs `f10 daemon --service` at login and restarts it whenever it exits, `f10 daemon stop` included. So with the service installed, a stopped daemon comes back within seconds. `f10 remote uninstall` stops the service and removes it. Like `f10 daemon stop`, each drive stops at its next tick, and `f10 start --resume <n>` goes on with one. The service manager waits up to 11 minutes for that before killing what is left.
  The service runs the `f10` the installing shell's PATH finds, which is the plugin's shim in a session, else the running binary. It carries that shell's `PATH`, `HOME`, `F10_RUN_DIR`, `XDG_RUNTIME_DIR` and `F10_USER_DIR`. So it uses the same run dir and finds the same tools. A shim under a versioned plugin directory stays on that release after an upgrade, and `f10 doctor` flags it as older than the binary running doctor. Run `install` again after an upgrade like that, or after any of the others moves. Installing drains the running daemon as an upgrade does before the service restarts, so the restarted daemon resumes its drives.
  A `--service` daemon never idles out, and while another daemon holds the lease it waits and takes over when that one exits. A client that replaces an older daemon writes its version to `newest` in the run dir first, and again once the old daemon has drained. For two minutes after that, a service older than it keeps waiting. So the service manager restarting an old binary cannot take the drained drives from the daemon that client starts. Past those two minutes an old service serves again, and the next newer client replaces it as usual.

### MCP

`f10 mcp` serves the daemon's API as MCP tools over stdio, for Claude Desktop and local agents, with no network involved. Claude Desktop runs it from its config, by the absolute path `command -v f10` prints, because Desktop does not start servers with your login shell's PATH:

```json
{ "mcpServers": { "f10": { "command": "/Users/you/.local/bin/f10", "args": ["mcp", "--scope", "read,answer"] } } }
```

- **One tool per method.** Every unary method of `f10.v1` and `agents.v1` is a tool named after the method in snake case (`list_asks`, `answer_ask`, `get_run_status`). Its description is the rpc's comment and its input schema the request message, in proto field names. Both are generated from the proto schema by `protoc-gen-f10-mcp` into `cli/gen/mcptools`. So `just proto::gen` after a new method is all it takes to add its tool. The plugin refuses an rpc with no comment.
- **Scopes.** The tools offered are the methods the **Remote** scope table places under the scopes given. `--scope` names the scopes. `--token <name>` takes the scopes of the device token with that name, and is refused once that token is revoked. The socket checks no token, so its secret is never needed here. With neither flag, the scopes are `read` and `answer`. `Hello`, `StopDaemon` and `WatchProject` are never tools. Neither is `WatchEvents`, because it streams and a tool call returns once, so poll `get_run_status` instead.
- **Calls.** Each call starts the daemon when none listens, so a client that stays connected past the daemon's idle exit keeps working. A result is the response as JSON. A refused call, such as an ask already answered or a misspelled field, is a tool error naming why.

## Inbox

A driven agent's ask (`f10-state.sh ask`, `modes/driven.md`), and a drive's own finish question, wait in one per-user inbox until the first answer from any way in.
`f10 asks` and `/f10:asks` were its names until v0.67; `f10 asks` still runs, hidden, for one release.

- **Where.** `~/.claude/f10/inbox/` (`F10_INBOX_DIR` overrides), beside the run state and outside every project: one YAML file per open ask, `<root hash>-<TASK>.yaml`, naming its task, url, storage root, main checkout, worktree, herdr pane, drive (none for an agent no drive watches), skill, the ask's text and its parsed questions. A project is its storage root, shared by every worktree of a repo, and the root hash at the front of the file name names it. The first answer moves it to `answered/` with the answer, the way in (`pane`, `asks`, `skill`, `drive`, `cancelled`) and the time, through a rename only one caller wins; a later one is told who answered and when. At each session start, answered asks older than a week are pruned, and so is an open ask whose worktree is gone.
- **Raised.** `f10-state.sh ask` writes the ask as the agent stops; the drive that sees it adds its number. The same text is one ask, so the user is told once.
- **Shown.** `f10-state.sh ask` prints the ask as a banner, which the asking agent prints verbatim as its last message (`modes/driven.md`); the context hook and `f10 inbox` draw the same banner. Inside Herdr, raising the ask reports the agent's pane blocked (`herdr pane report-agent --source f10:inbox`), and the first answer from any way in releases it (`herdr pane release-agent`), handing the pane back to Herdr's own detection.
- **Told.** The status line counts the open asks of the session's project, ` f10 · 󰚇 2 asks`, with or without a run, and every project's outside git (`docs/statusline.md`). A `UserPromptSubmit` and `SessionStart` hook (`f10 inbox hook`) adds each open ask of the session's project to its context once, as its banner, leaving out the asking agent's own checkout. It tells a drive's agents nothing: `f10 start` launches them with `F10_DRIVEN=1` in their Claude Code settings env, which every hook inherits, the prompt that carries an answer included. A discussion's agent is not marked. A session outside git is told every project's asks. The desktop notification stays global, since it never enters an agent's context. And `notify` runs per new ask: `~/.f10/inbox.md` frontmatter, `notify: ntfy publish f10 "{task} asks: {question}" {url}`, run through `sh -c` for up to 10s, with `{task}`, `{question}` (the first question) and `{url}` standing for the shell variables `${F10_ASK_TASK}`, `${F10_ASK_QUESTION}` and `${F10_ASK_URL}`, so an agent's text is never run as shell; quote them as any variable; `notify: off` runs nothing, and absent it is a desktop notification (`osascript` on macOS, `notify-send` on Linux when installed).
- **Answered.** Typed in the agent's own pane, the prompt is the answer: the agent goes on, and its drive, if stopped, is resumed to follow the turn. `f10 inbox answer <task> 1=b 2=8080` answers questions by option letter, number or text; free text only for a question with no options, and every question not named takes its default (one with no default must be named). `<project>:<task>` picks between two projects' asks, and `answer` finds an ask in any project. Bare `f10 inbox` in a terminal walks this project's open asks (`--all` every project's), Enter taking each default and `s` skipping an ask; elsewhere it is `f10 inbox list`. `/f10:inbox` asks them in one questionnaire and runs `f10 inbox answer --by skill`.
- **Delivered.** An answer to a drive's task goes to its drive: a stopped drive is resumed through the daemon (`--resume <n>` from its main checkout, with the answering process's allowlisted environment), and a drive still running takes it as it stops. An answer to an agent no drive watches is sent to the agent, as `f10 forward` would; one that cannot be sent is printed to type in its pane.

`f10 inbox list` prints this project's open asks, oldest first, each as its banner, then those answered in the last day; `--all` lists every project's, each banner naming its project, and outside git it lists every project's too. `--json` prints `{open: [...], answered: [...]}`. The API's `ListAsks` takes the storage root as `project`, which MCP's `list_asks` passes on; empty, as the web client sends it, lists every project.

## Watch

The daemon watches every project `f10 start` ran in, for the tasks no drive owns: a task started by hand whose PR merged on the host, an agent left idle, a run that stopped with nobody looking.
`watch: finish` is the default, and under it a pass also finishes a task whose PR merged.
`watch: report` only reports, and `watch: off` watches nothing.
`watch_catchup: on` lets a pass also catch an open PR up with its target when it needs it (**catchup**, **left** and **behind** below).
`watch_ci: on` lets it send a red PR to its agent (**red** below).
Owning is the task's lease (**Drives**): a task a drive or a finish holds is never looked at, and the watcher's own finish takes the lease first, so a drive started later is refused with `owned by the f10 watcher`.

- **Registering.** After its report, a discussion or a pickup hands the daemon the project's storage root, its main checkout and the same allowlisted environment a drive gets. A flow start does the same before it follows its drive. It never replaces or waits on a running daemon: a daemon of another version is used as it is, and one that is stopping is skipped with one line, so the next `f10 start` hands the project over. A failure is one `watch: warning:` line, never a failed command. The project stays watched across daemon restarts and upgrades (`watch.json` in the run dir, which keeps the newest registration's environment, tokens included), and leaves the list once a pass finds no task worktree left (under `watch: finish`, no local task branch either, since its janitor still has work), once its main checkout is gone, or after ten passes in a row fail. A registration that arrives while a pass runs gets one more pass after it, so a task started meanwhile is seen. `watch: off` in the frontmatter registers nothing.
- **A pass.** Every 3 minutes, and at once on registering, the daemon runs `f10 watch --pass` in the main checkout with that environment, its output appended to the project's watch log. For each task worktree whose lease nobody holds and that `f10 watch off` did not hand back, it reads the PR (`gh pr list --head`, one call per branch), the newest run reported from the worktree and the agent's status, and says at most one thing, in this order:
  - **merged**: the PR merged on the host. Under `watch: finish` it is finished when the branch's tip equals the PR's head and the agent is not working. The pass takes the task's lease, then reads the agent's status and the branch's tip again. It finishes the task as `f10 finish` would: the worktree removed, the workspace closed, the plan archived and main pulled. Its finish refuses anything but a merged PR, so the watcher never merges. A refusal, such as a dirty worktree or someone else's PR, is reported with `f10 finish <id>` to run by hand. A bundle's riders are closed as the PR body says (**Bundles**). A branch with commits the PR lacks is only reported.
  - **closed**: the PR closed without a merge; reported, never cleaned up.
  - **stalled**: the agent idle while its run says plan or ship is running and the run has not changed for 10 minutes: the test a drive halts on, with a longer grace, measured from the run's last write since a pass keeps no memory between runs.
  - **stopped**: the run ended `blocked` (with its question), `failed`, `partial`, or ship `done`.
  - **red**: the open PR's head checks failed, told once per head with the first failed job that was not cancelled. A head that read green is not read again until it moves, so a quiet PR costs no call. Under `watch_ci: on`, an idle agent with a clean tree and no background work gets `/f10:ci --driven` once per head, under the task's lease. A task handed back with `f10 watch off`, or one a drive holds, is never sent one. Off by default, because an unattended agent would commit and push a fix.
  - **catchup**, **left** and **behind**, under `watch_catchup: on` only. When any undriven task has an open PR and an idle agent, or a catchup in flight, the pass fetches origin once. It reads what each such branch targets as the catchup step does: the branch it was started after, else the default branch. It cross-checks that against the PR's base. A target that moved past a branch whose tip is origin's is caught up only when the move matters to the branch, because a catchup's push restarts the PR's CI. It matters in three cases. The branch conflicts with the target, checked by an in-memory `git merge-tree`, which touches neither the worktree nor the index. A catchup is already owed: an `f10-landed` or `f10-base-moved` mark is on the branch, or a failed catchup is on record. The host will not merge the PR while it is behind: GitHub's `mergeStateStatus: BEHIND` under a require-up-to-date branch protection, or GitLab's `need_rebase` under a fast-forward merge method. **left** is any other behind PR. It merges into its target clean, so it is left alone and told once per pushed head. Another clean move of the target is silent, and a leave after a catchup is told again. A branch that needs a catchup, with an idle agent and a clean tree, is marked with the drive's marks, `f10-landed` or `f10-base-moved`. A mark already there, left by a finish, a ship guard or a failed catchup, is set again. The branch is then integrated by the same engine the drive uses, under the task's lease. The agent is then prompted with a driven catchup for what is left: the conflicts git stopped on, then verify. The watcher always leaves verify to the agent, so a pass stays short. A prompt that cannot go out puts the branch back where it was. A later pass reads the end from git. The end is clean when the mark is gone, no merge or rebase is open and the target is held. There must also be no conflict marker and a clean tree. The pass then pushes the branch under the lease, plain after a merge and `--force-with-lease` after a rebase. The push is against the origin tip the catchup started from. So a commit pushed to the PR meanwhile is never overwritten, and such a catchup is told once as not pushed. One that did not end clean is told with `/f10:catchup` to run in the worktree, and the branch is left as it is. It is sent again only once its fingerprint moves: the target, or what the branch conflicts on. **behind** is a PR the watcher will not catch up. It has no agent in its workspace, was retargeted to a branch the task was not started on, or has a tip that is not origin's. A working agent or a dirty tree waits for a later pass. So does an idle agent whose last turn launched background work, for up to 30 minutes from the launch. No catchup is sent to it then. During that wait, a catchup in flight that did not end clean is not failed, while one git shows clean is still pushed. A task a drive holds, or one handed back, is never touched.

  A pass lists only this checkout's branches, so it asks the network nothing before the host, except the one fetch `watch_catchup: on` needs. An agent whose status cannot be read (no listing from Herdr) is never finished. A host that does not answer for a task's PR leaves the task out of the pass, and the pass says so in one line naming the error and the tasks left out, every pass until the host answers; `f10 watch` shows it under `host`.
  Each fact is told once: `watch.yaml` in the storage root keeps the last report per task and every fact told for it (the PR's state, the run's update time), so a fact is never repeated, even after another, and a newer one is told. Under `watch: finish` the janitor then runs if it is due; under `report` it never runs from a pass, since it deletes branches.
  A pass exits 7 when nothing is left to watch, which drops the project from the daemon's list.
- **Telling.** A pass reports through its own output, one line per report, into the project's watch log. `f10 watch` prints the last report per task. Each report is also recorded once, when first told, as a `watch_reported` ledger row, which `WatchEvents` streams. The host trouble a pass repeats every pass is recorded only when it starts.
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
| `mark` | the key and value a conflict sets (`f10-landed <default>`, `f10-base-moved <sha>`), written with `--mark` |
| `code` | for an `f10-after-branch` base: `yes`, or `none` when it has no commit of its own, merges not counted |

`--guard <entry>` decides as ship's guard before `implement`, `pr` or `judge`: before `pr` and `judge` every base is read from origin, a branch already on origin is merged so its push never forces, and `catchup: off`, the default branch itself, no recorded base before implement or a base not on origin yet before `pr` print `method: skip`.
`--mark '<key> <value>'` writes the `mark` row on the branch and prints the branch and the mark.
It decides nothing, fetches nothing and takes no `--guard`.
The branch is the one checked out, or the one a stopped rebase is rebasing, since a rebase detaches HEAD.
With neither it fails.
It refuses a key that is not `f10-landed` or `f10-base-moved`, and an `f10-base-moved` value that names no commit.
A guard that stops on a conflict marks the branch this way, never with `git config` by hand.
Under a stopped rebase, `git config` writes the key under an empty branch name that git then refuses to read.
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

`f10 review round` is the one local verb: it reads the storage root's findings files and needs no remote review declared.
The directory is `reviews/<task id>` (a bundle's lead), else `reviews/<branch>` with `/` as `-` for work no task names.
The task comes from the argument or the branch, never from the task this session's run recorded.
A directory holds one series of rounds, and a series belongs to one PR, named on each round's `pr:` header line.
For a branch checked out here, the command checks whether the series has ended before it counts rounds.
A series ends when the latest round's PR merged or closed on the host.
A round that names no PR ends its series when HEAD cannot reach its `reviewed:` sha.
An ended series moves to `reviews/archive/<id>.<N>/`, and the next round is 1.
`f10 finish` and the janitor archive the directory the same way.
A merge they see ends the series even when it had no rounds: it leaves an empty `reviews/archive/<id>.<N>/`, because the archive counts a task's PRs for its lore change (`docs/lore.md`).
Each archived series holds a `closed` file naming its PR, its head and when it closed.
So finishing the same PR again, or the janitor after it, closes nothing more.
A series that ended only because a rebase left its `pr: none` rounds unreachable is marked `restart: yes`.
Those rounds belong to the PR still to come, so it counts as no PR for the lore change.
A host that cannot be read keeps the series going, with a line on stderr, because archiving by mistake would hide findings that `/f10:resolve` still needs.
It prints `dir:`, and `archive:` when a series moved.
It prints `latest:`, the highest round's file, absent with none.
It prints `pr:`, the branch's open PR for the next header, `none` without one.
Last it prints the next round's `round:` and `path:`.
Three cases decline, exit 3, with a `decline:` line and the findings behind it instead of the next round:

- A branch whose work its base already holds by content, as after a squash merge ("already merged, nothing to review").
- `1.md` holding a finding without a verdict.
- `2.md` present, the last round, naming each finding whose latest block is an `ask` or still waits for a verdict.
`--json` emits the same.

## Standup

`f10 standup` reads what you did over a period: your commits from git, your PRs or MRs, and other people's you reviewed or merged.
It groups them by project, then task, and gives each task where it ended the period.
It changes nothing and does not fetch: local and remote-tracking branches are read as they are, while PR states come fresh from the host.

**Host reads** come in two passes, so a busy repository answers in seconds.
The searches run first.
Then your PRs' commits and force-pushes are read, eight at a time, only for the PRs updated since the period starts.
A push always moves a PR's update time, so a PR updated earlier has nothing in the period.
With no stored standup, the start is settled from git and the searches before the second pass, and settled again after it.
A host 5xx is retried once per call.
A login or a search that still fails fails the command, because without them the report has no PRs.
A PR whose commits still fail becomes a `warnings` line, and the report prints without them.

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

**The ledger half.**
The ledger (`~/.f10/f10.db`, [ledger.md](ledger.md)) adds the work that leaves no commit, and what each task waits on.
It is read once per repository: the project's sessions active in the period, by the scoped read.

- **Whose work**: a row counts when it was recorded in one of the repository's checkouts (the main checkout or a worktree of it).
  It also counts when it was recorded in the worktree `f10 start` makes for the row's branch, still matched after `f10 finish` removed it.
  A row no checkout recorded counts when its task is one your commits or PRs already name.
  A drive counts, because its agents run in your worktrees.
  A teammate's clone elsewhere on the machine does not.
- **Period**: a row counts by its time, in the same period as commits, so `--since`, `--week` and the stored standup apply unchanged.
  A task with ledger rows in the period but no commit or PR is reported too, its state `started`.
- **Per machine**: the ledger is local, while git and the host are not, so on a second machine the ledger half is partial and `ledger.scope` says so.

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
      "commits": [],
      "blocked": {"kind": "ask", "since": "...", "text": "1. Refactor as agreed? [yes | stop]", "later_commit": "..."},
      "activity": [{"at": "...", "kind": "review", "text": "round 1: 3 fixed, 1 skipped"}],
      "stage": "review round 2",
      "driven": 12,
      "active_minutes": 95
    }
  ],
  "other": {"prs": [], "commits": []},
  "reviewed": [
    {"repo": "svc-a", "number": 41, "title": "...", "url": "...", "state": "shipped", "author": "them",
     "actions": [{"kind": "reviewed", "at": "..."}, {"kind": "merged", "at": "..."}],
     "days": ["2026-10-15"]}
  ],
  "drives": [{"repo": "svc-a", "number": 12, "tasks": 5, "shipped": 4, "needs_you": 1}]
  }
  ],
  "ledger": {"scope": "this machine only", "contributed": true},
  "warnings": []
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
| `tasks[].blocked` | what the task waits on when the period ends, and `since` when: `ask` (an ask with no answer yet, the question in one line), `halt` (a drive halted it after its run's last word, with the skill and reason) or `failed` (its run stopped `failed` or `partial`, with the step and the note, such as a red verify or CI); absent when nothing |
| `tasks[].activity` | the steps run in the period, by time, each once a day: `plan` (saved or superseded), `debug` (cause found, cause unconfirmed, or run), `spike` (report saved), `review` (`round 2: 3 fixed, 1 skipped`, or `round 2 opened`) and `judge` (the verdict and its subject) |
| `tasks[].stage` | where the run left the task, in words: `planned, not started`, `planning`, `review round 2`, `waiting on CI`, `waiting on <base>`, `shipping: <step>`; absent when the run has nothing `state` lacks, as after a finished ship. `state` keeps its values, because the carry-over and the classes read them |
| `tasks[].driven` | the newest drive that lists the task and ran in the period; absent when none |
| `tasks[].active_minutes` | the task's active minutes in the period, counted as `f10 stats` counts them, waits on you left out; always present. The `/f10:standup` skill renders it only when your `standup.md` asks for it |
| `tasks[].blocked.later_commit` | on an `ask`: the sha of the task's first commit authored after it, which may have settled it while the ledger still holds the ask open; absent when none |
| `drives` | each drive that ran in the period (created in it, or with a task your ledger rows show active in it), with its task count, the tasks shipped and the tasks that ended needing you; a task the report shows `shipped` counts as shipped whatever its drive outcome, because a drive that stops before finish ends non-zero on a PR merged later |
| `warnings` | one line per PR whose commits or force-pushes the host did not return after a retry, such as `svc-a#40: commits not read: gh: HTTP 504`; the report printed without them. Always present |
| `ledger` | present when a ledger exists: `scope` is always `this machine only`, and `contributed` says whether the ledger added anything to the report |

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

## Atlas

`f10 atlas` prints what each of your public projects is, how it has moved and how they connect.
Each aliased private project shows as an anonymous node.
For each project it prints what `~/.f10/atlas.md` declares: role, status, tagline and links.
It also prints what the project's checkout says: language, first and last commit, and commits per month.
It adds the latest tag and the dependencies.
`docs/atlas.md` specifies the Atlas JSON and the declared file, and `docs/atlas.schema.json` is its schema.

**The scope** is the board's: this checkout, or the union of `-C <path | name>`, `--group <name>` and `--all`.
`-C` and `--group` are repeatable, and a group is one in `~/.f10/board.md`.
`--all` is every checkout under `F10_ROOTS`, and with it unset it is an error.
A linked worktree counts as its main checkout, and a group entry naming a repository with no checkout is skipped, because the atlas reads local history only.

**What is printed** follows the privacy rule in `docs/atlas.md`:
- A project whose entry says `public: true` is printed in full.
- A private project whose entry gives an `alias` is printed as an anonymous node: its alias, its kind and its edges, nothing else.
  Only its manifests are read, never its history.
  So nothing of its name, remote, path or rhythm reaches the document.
- A project with no entry, or a private one with no alias, is dropped and never read.
The declared file is checked whole on every run.
An unknown key or status, a bad or repeated alias, or a key matching two checkouts fails the run naming the entry.
With `F10_ROOTS` set, a key or a link that names no checkout under any root fails too, because it is a typo.
One naming a checkout outside this run's scope is skipped.
Two public checkouts with one id, such as two clones of one remote, fail the run naming both, because an edge could not tell them apart.

**Edges** join two printed projects in scope, never two anonymous ones.
A `depends` edge comes from the root `go.mod` (direct requirements and `replace` targets), `package.json`, gemspec or `Gemfile`.
It is matched by the name the other project's own manifest declares, never its directory name.
A `link` edge is an entry's `links` item, with its relation in your words.
An edge from an anonymous node carries no `manifest` and no `dev`, because the file would name the private project's language.
An edge to a dropped project, or to one outside the scope, is left out.

**The history** is the default branch's: origin's `HEAD`, else a local `main` or `master`, else `HEAD`.
A checkout whose history does not read gets an `error` on its node, with its path removed, and every other project still prints.
An anonymous node has no `error` field.
So a malformed manifest there prints a fixed `warning:` line on stderr under its alias, naming neither the manifest nor a path.
The node keeps its declared links but draws no `depends` edge of its own.

- With no flag, it prints a line per project, or the project's error.
  The line holds the name, status, language, last commit date and tagline.
  Anonymous nodes follow, each as its alias and `anonymous`.
- `--json` prints the document.
  Over several projects it is still one document.
- A project with edges gets a second line: `depends on:` lists what it requires or links to, and `used by:` lists what requires or links to it.
  A link shows its relation in parentheses, and a development-only dependency shows `(dev)`.
- `--out <path>` writes `--json` to a file, such as a site's data directory.

It asks no host and reads no ledger, so a run over forty checkouts takes about a second.

## Stats

`f10 stats` is how long each part of a delivery took, per task of the current project, newest first, then a median row.

- **plan**: active minutes. A `/f10:plan` run counts from its start to the plan it saves; inside `/f10:ship`, the plan step counts from entering it to entering the next step.
- **implement**: active minutes of the `implement` step, summed over its stretches (a driven run blocked on an ask and resumed is two).
- **review**: the review loop, review, resolve and judge together. A `review` entry runs both review and resolve under one claim. Review ends when the round's findings file is first written. The rest of the entry is resolve, a second round and a resolve resumed after an ask included. Under `review:local+ci` the file is first written by the local review, so the wait for the CI review counts as resolve. Every `judge` flow entry counts here, the one before implement as well. The judge the stale step runs counts as plan.
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

## History

`f10 history <path>...` is the brief the plan and judge steps read before choosing or ruling on an approach: what the tasks that touched these files went through.
It is a lookup over git, the ledger and lore, so the same history prints the same brief.

1. **Neighbors** are the tasks whose commits touched the paths since `--since` (default `180d`), read in one `git log` at `HEAD`.
   A directory stands for the files `HEAD` holds under it.
   A commit's task is its `Task:` trailer, where a bundle counts once, under its lead.
   Else it is its `Change:` trailer, else what `f10 task regression` would attribute it to.
   That is the closest unmerged branch, then a subject opening with an id.
   `--task`, else the branch's task, is left out, because a task is not its own history.
2. **Stale** neighbors are dropped and counted.
   A neighbor is stale when its added lines in these paths stand under 30% at `HEAD`, by `git blame`.
   It was mostly rewritten since, so its record no longer applies.
   Only the files a neighbor added lines to are blamed.
   A neighbor that only deleted lines has nothing to measure and stays.
3. **Per neighbor**, the ledger gives its kind, size and implement minutes.
   The kind is the `Kind:` line of its live or newest archived plan, and minutes count as `f10 stats` counts them.
   It also gives the review rounds, the finding categories raised and the fixed and skipped counts resolve recorded.
   Last come plans superseded, halts, judge verdicts, and the bugs later traced to it.
4. **Regressions**: the `regression_found` rows in the window whose lines are on these paths or whose origin is a neighbor.
5. **Lore**: the effective lessons and decisions anchored to the paths, a decision with its rejected options.
   A catchup's decision (its body opens `catchup:`) is left out, because which side a merge kept says nothing about the design.
6. **Statistics** print only over 3 reviewed neighbors or more, the live ones the ledger holds a review round for.
   That is because work from before the ledger has none to count.
   The statistics are the median review rounds, and each category raised in at least half the reviewed neighbors.
   A review round is a round label at one reviewed sha, so a follow-up PR's rounds add to the first PR's.
   Under 3, the neighbors are listed and no statistic is printed.

Counts and outcomes come from the ledger and reasons from lore, so a finding written to both (`finding_verdict` and a lore `finding`) counts once.
The ledger reads are the scoped ones: the sessions of the neighbors and the project's `regression_found` rows, both indexed.

The text is at most 15 lines, each citing task or lore ids, most decision-relevant first.
First come the neighbors by id with the stale count, then regressions and recurring findings.
Then come the median, decisions and lessons, and last a line per neighbor.
Lines past 15 drop from the bottom.
Every line is cut at 160 characters.
Nothing found prints `No history for these files.`

`--json` prints the whole brief, uncapped:

| key | holds |
|---|---|
| `paths`, `since` | the repo-relative paths and the window's start |
| `neighbors` | per live task: `task`, `commits`, `added`, `standing`, `kind`, `lines`, `implement_minutes`, `rounds`, `categories` (`raised`, `fixed`, `skipped` per category), `plans_superseded`, `halts`, `halt_reasons`, `judge_verdicts`, `regressions` (the bug tasks traced to it) |
| `stale` | the dropped tasks: `task`, `commits`, `added`, `standing` |
| `regressions` | `bug`, `origin` (a task, `pre-f10` or `outside-f10`), `class` |
| `aggregates` | `null` under 3 reviewed neighbors, else `n` (the reviewed neighbors), `median_rounds`, `recurring` (`category`, `tasks`), `regressions` |
| `decisions`, `lessons` | `id`, `change`, `path`, `anchors` (on the paths, in the form `f10 lore add --anchor` takes), `text`, and for a decision `chosen` and `rejected` |

A value never recorded is absent, never `0`.

## Audit

`f10 audit map <range | all>` is the map the `/f10:audit` skill partitions its reviewers from: churn, tasks and pain per area over a range.
`all` is the newest 300 commits, because the full history would crowd out the code.

1. **Areas** are each changed file's parent directory, repo-relative.
2. **Churn** is the lines added and deleted per area, and the commits that touched it, from one `git log --numstat`, merges left out.
3. **Tasks** are the range's commits by task, each with its commits and its areas.
   A commit's task is found as `f10 history` finds it.
   It is its `Task:` trailer (a bundle under its lead), else its `Change:` trailer, else what `f10 task regression` would attribute it to.
   A commit with no task goes to `upkeep`.
4. **The pain map** compares each area's tasks with every task of the range (the project), using the same per-task counts as `f10 history`:
   - review rounds, the median and the max, over the tasks a round was recorded for
   - halts, each with its task and reason
   - plans superseded
   - recurring categories: a finding category with a **fixed** verdict in at least 3 tasks of the area, and never one only skipped
   - regressions: the bugs traced to the area's tasks, with their class
   - implement minutes per 100 lines, over the tasks with both a size and implement time

   Medians need at least 3 tasks recording them, in the area and in the project.
   Halts, replans and regressions are compared per task.
   An area is listed only when it is over the project on at least one signal, with only the signals that put it there.
   The ledger reads are the scoped ones: the sessions of the range's tasks and the project's `regression_found` rows.

The text is one block per part, and the pain block holds one line per area, each citing task ids.
With no ledger rows for the range it is `No ledger history for this range.`
With rows and no area over the project, it is `No area over the project median.`

`--json` prints the whole map:

| key | holds |
|---|---|
| `range` | the range as given |
| `churn` | per area, most changed first: `dir`, `added`, `deleted`, `commits` |
| `tasks` | by id: `id`, `commits` (short shas), `areas` |
| `upkeep` | the short shas of commits naming no task |
| `pain.project` | `tasks`, `median_rounds` and `implement_minutes_per_100_lines` (`null` under 3 tasks), `halts_per_task`, `superseded_per_task`, `regressions_per_task` |
| `pain.areas` | per listed area: `dir`, `tasks`, and the signals over the project: `median_rounds` with `max_rounds`, `halts` (`task`, `reason`), `superseded` (a task per plan), `recurring` (`category`, `tasks`), `regressions` (`bug`, `origin`, `class`), `implement_minutes_per_100_lines`; then its text `line` |
| `pain.lineages` | per task of the range with bugs traced to it: `origin`, `bugs`, `classes` |
| `ledger` | whether the ledger held rows for the range's tasks |

## Ledger

`f10 ledger dump` prints the rows `~/.f10/f10.db` holds, and `f10 ledger schema` prints the JSON Schema of each kind's payload.
[docs/ledger.md](ledger.md) is the spec: versions, how old rows read, and the indexed payload columns.

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
`children` prints a tree to a terminal and JSON to a pipe or with `--json`; `--plain` prints the tree into a pipe too, with no urls (they stay in `--json`), and `--open` leaves out the finished rows as well, a dependent of a finished base moving up to its base's level.
A row is finished when its issue closed, or, for a tracker that reports no state, when its plan is archived.

## Stale

`f10 task stale 71` answers the stale step's check under `stale_after`, one line on stdout:

| line | meaning |
|---|---|
| `fresh` | `stale_after` is not declared, or the task is younger than it; the tracker is not asked when it is not declared |
| `judged <path>` | older, but a verdict file with subject `task <id>` reads `proceed` and was written within `stale_after`; the newest one is named |
| `needs judge` | older, and no such verdict |
| `undated` | no created date was found |

`--created <YYYY-MM-DD>` passes the date the task's read showed, which spares a tracker call; without it the date comes from the host's issue, and on any other tracker the answer is `undated`.
Verdict files are read from the storage root and, from a worktree that keeps its own `.f10`, from main's too.
A bundle is checked on its lead.

## Regression

`f10 task regression 72` records where the bug task 72 came from, as a `regression_found` row on the ledger.
It prints one line for the plan's `Regression of:` header: `<origin> (<class>, <confidence>)`, or `none (<why>)`.
The debug step calls it once the root cause is known.

The agent passes what only reading the code settles:

| flag | value |
|---|---|
| `--commit` | the commit that introduced the fault; left out when none was found, which still records the row |
| `--lines` | `path:start-end` of the faulty lines, numbered as `--base` (default `HEAD`) has them; repeat for several |
| `--found-by` | `bisect`, `blame` or `none` |
| `--confidence` | `confirmed`, `likely` or `unclear` |
| `--repro` | the debug pass's outcome: `obvious`, `full`, `traced`, `trusted`, `skipped` or `unconfirmed` |
| `--class` | `out-of-reach`, when the fault is an interaction no diff review could see |

The command settles the rest.
The origin task is the commit's `Task:` trailer, else its `Change:` trailer, else the closest unmerged branch holding the commit, else a subject that opens with a task id.
A commit with none of them is `pre-f10` when it is older than the repo's first `Task:` commit, else `outside-f10`.
The class reads the origin task's findings, live and archived, and its verdicts.
The first matching row wins:

| class | when |
|---|---|
| `skipped` | a finding's `where:` falls within 3 lines of a faulty range and its last verdict is `skip`; the row names its round and id |
| `missed` | a review round's `reviewed:` commit already held the faulty lines |
| `unreviewed` | rounds exist and none held the lines, or nothing reviewed or judged the task |
| `judged-proceed` | no round, but a judge verdict of `proceed` |

A reviewed commit the clone lacks is fetched once from the PR's head.
When no round's commit can be read, the class comes from the findings alone, the confidence drops to `unclear`, and the row's `note` says why.
Only a bad flag exits non-zero: attribution never stops a run.

`--claimed <origin>` records a `regression_claimed` row instead, the origin the task's own text names, with `--text` the phrase that names it.

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
| `finished` | no branch, no live plan, and closed (its plan archived, for a tracker with no state), as drive decides it |
| `depth` | 0 directly under the parent, one more per `After:` step |

A task with no subtasks prints `[]`.
Exit 3 means the tracker has no parent link: a driver that declines the verb on a tracker no host reaches, or a host without sub-issues or work items.

## Create

`f10 task create` creates a task through the driver's `create` verb ([driver contract](driver-contract.md#creating-a-task)), else, on a tracker that is the host's issues, as a GitHub or GitLab issue.
It reads the task as markdown on stdin, `# <title>` on the first line, and prints the new id in the project's id format.
The body's `After: <id>` line reaches the driver as a link to the base task, `After: [<id>: <title>](<url>)`, titled as `f10 task ref` would; a base it cannot title stays bare.
The flags `--status`, `--priority`, `--label`, `--parent` and `--set key=value` go to the driver.
The body's `After:` line goes as `--after`.
`--priority` is one of `urgent`, `high`, `medium` or `low`.
`--label` is repeatable, and so is `--set`, which sets a property the driver knows.
An unset property passes no flag.
On the host, `--status` takes only open, and `--priority` becomes the label `tracker.priority` maps to it. `--parent` links the new issue as a GitHub sub-issue. A link that fails is a warning, and the id still prints. An issue has no named properties, so `--set` there is refused before anything is created. GitLab nests tasks, not issues, under an issue, so there `--parent` always warns and the parent's body lists its subtasks.
It exits **3** when neither creates tasks here (no driver, or one declining `create`, on a tracker that is not the host's): the capture step then uses the project's Create adapter.
A failed create is never retried, and its error says the task may exist already, unless the driver exited 4 to say it failed before writing ([driver contract](driver-contract.md#exit-codes)).
With `tracker.title` declared on the host's issues, the new issue's title is then written through it; a failure there is a warning naming `f10 task retitle`, since the task exists.

## Ref

`f10 task ref FT-109 FT-132` prints one line per task, `FT-109 (<title>)`; with `--md`, `[FT-109: <title>](<url>)`, brackets in the title escaped.
This is the form text that leaves the machine uses to name another task.
Titles and urls come from the ledger (`~/.f10/f10.db`), which `task read`, `task create`, `task search`, `task open --print`, `task children`, `board` and `status` fill as they read the tracker.
A task not there costs one tracker call (the host's issue, or the driver's `read` and `url`), whose answer is kept.
`--fresh` asks the tracker for every task and rewrites what is kept.
A title renamed on the tracker stays stale until f10 reads that task again.
An id the tracker does not know, or a tracker that fails, prints bare with a note on stderr, and the exit is 0.
A closed task's line ends in ` [closed]`, in both forms.
A tracker read this call made decides, so `--fresh` shows a task reopened after its finish as open.
Otherwise it is closed when the ledger records an `f10 finish` of it that still stands (no live plan re-planned it since), which costs no tracker call.
The state is never kept, so a task closed outside f10 shows closed only once f10 reads it again (`--fresh`).
Text that leaves the machine never carries the marker.
`task create` warns on stderr when the body's `After:` line names a closed task, and links it anyway.

## Report

`f10 task report` prints the facts block a skill's report opens with (`conventions/report.md`), from the same titles and urls as `task ref`.
One id prints its `task`, `title` and `url` rows.
Several ids print a row each, in the order given: the id as the label, `↗ <url>` as the value.
A task with no url known prints bare, and the exit is 0.
`--children` takes one parent and hangs its open subtasks under it, nested by `After:` as `f10 task children` orders them.
Each task's url goes on the line under its title.
A tracker with no parent link prints the parent's facts with a note on stderr, and the exit is 0 either way.
It shows no run stages or gates, which a task just captured cannot have.

## Comment and update

`f10 task comment` posts markdown on stdin as a comment on the task, through the driver's `comment` verb, else the host's issues (`gh issue comment`, `glab issue note`), and prints the comment's url when one comes back.
It refuses a closed task, exiting 1 with `<id> is closed; pass --closed to comment on it anyway` and posting nothing, because a comment there is usually meant for open work.
It reads the tracker's state, which wins over the ledger because a reopen never clears a finish.
Where the tracker gives no state, as a driver does, or fails to answer, a standing finish in the ledger refuses the comment.
With no standing finish, the comment goes through.
`--closed` skips the check.
`f10 task update` sets the task's `--status`, `--priority`, `--label` (repeatable, adds), `--assignee` (replaces) and `--set key=value` (a property the driver knows), through the driver's `update` verb, else the host's issues, where a status is `open` or `closed`, a priority is the label `tracker.priority` maps it to, and `--set` is refused ([driver contract](driver-contract.md#commenting-on-and-updating-a-task)).
Both exit **3** on a tracker that is not the host's when no driver takes the verb, and a ship run then reports its shipment locally only.
A failed comment is never retried, and its error says it may have posted.
On the host's issues with `tracker.title` declared, an update also puts the prefix back on a title renamed by hand.
`--after <id>` writes the body's `After:` line, replacing the first one or adding one at the end, and `--after none` drops it.
`f10 drive plan` proposes these edits.
It goes to the driver as an update of its own (`update <n> --after <m>`, an empty number to drop it).
Else the host's issues rewrite the body, and a tracker that edits no body exits **3**.
`--parent <id>` links the task under that parent, moving it from a parent it already has, and `--parent ""` takes it out from under its parent.
It goes to the driver as an update of its own, after the other properties, so a tracker without parent links declines only the link.
On GitHub issues it is a sub-issue link.
GitLab nests no issue under an issue, so there update exits **3**, as it does on a tracker whose driver declines it.

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
