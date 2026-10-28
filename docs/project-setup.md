# Setting up a project

f10 works in any repo with nothing declared: it infers the tracker from the git remote, the stack from the build files, the verify commands from the justfile or Makefile.
Declaring a fact stops the guessing for that one fact.
Everything you leave out stays inferred.

## Register the project

```bash
f10 init
```

Writes `.f10/instructions/project.md` from what detection found and adds `.f10/` to `.git/info/exclude`.
It never overwrites, so re-running is safe.
A `project.md` written before the frontmatter existed is moved over with `f10 init --migrate`: it puts the facts the file declares in prose into a frontmatter block above it, deletes nothing, and names the prose you can now delete.
On a file that already has a block, it adds only the prose facts the block does not declare yet, keeping the block's key order and comments.
The `Project` one-liner is left for you: no probe knows what a project *is*.

Run it before the first plan.
With nothing declared, task ids take a prefix derived from the checkout's name (`f10` → `F10`, `git-undo` → `GU`, `herdr` → `HER`), and that prefix names every plan file and is how a branch is searched for its task.
`f10 init` writes it down, so a rename or a changed rule later cannot leave two eras of plan files side by side.
A two-letter abbreviation of a one-word name is your choice to make, in `tracker.id`.

## `project.md`

Two halves.
A YAML **frontmatter** block holds the facts the `f10` binary acts on, typed, so a typo is an error rather than a silent default.
Free-form markdown under the headings below holds everything else, for the model.
Only **Tracker** matters for the flow's contracts; the rest have workable defaults.

```markdown
---
visibility: stealth
tracker: {kind: github, id: GH-###}
merge_method: squash
flows:
  default: plan implement review:local pr
  local: plan implement commit
---
# my-project · f10 project instructions

## Project

Go service for invoices.

## Tracker

- **Fetch** - `gh issue view <n> --comments`
- **Create** - `gh issue create`

## Verify

`just lint` / `just test`
```

| frontmatter key | what it declares | default |
|---|---|---|
| `visibility` | `stealth` or `public` | `stealth` |
| `storage` | `in-repo` or `out-of-tree` | `in-repo` |
| `layering` | `extends` or `replaces`, in a linked worktree only | `extends` |
| `tracker` | `kind` (`github`, `gitlab`, `notion`, `jira`), `id`, the task id format, an optional `priority` map from the four levels to the host labels that carry them, and an optional `status` map from `started` and `shipped` to the tracker's own words ([Reporting back](#reporting-back)) | host issues via `gh` or `glab`; ids prefixed from the project's name; no priority; no status moved |
| `merge_method` | `squash`, `merge` or `rebase`, for `f10 finish` | the merge method the repo allows, else squash |
| `catchup` | `rebase`, `merge` or `off`: how a branch is brought up to date with its base; `off` stops ship integrating before implement, `pr` and a change judge (it still asks about a base with no code), and leaves `/f10:catchup` and `f10 drive` running | `rebase` |
| `traces` | `on` or `off`: what f10 leaves on the host. `Task:`, `Step:` and `Review:` trailers on every commit a step makes, `Task:` on the squash commit `f10 finish` lands, a hidden state block in the PR body rewritten by each step after the PR, and a local review's or a judge's commit status and PR comment after the PR; off, nothing | `off` |
| `stale_after` | a number of days or weeks (`10d`, `3w`): fetch judges a task created longer ago than this before it is planned, from the created date the tracker shows; anything but proceed stops the run blocked | off |
| `driver_timeout` | a positive Go duration (`45s`, `2m`): how long f10 waits on one verb of the project's [driver](driver-contract.md) before it kills the driver and fails the command with `no answer within <limit>` | `30s` |
| `lore` | `local` or `committed`: where `f10 lore` keeps its entries, `lore/` under the main checkout's storage root on this machine, shared by its worktrees, or `.lore/` at the checkout root, committed with the code ([docs/lore.md](lore.md)) | `local` |
| `lore_gate` | `warn` or `block`: what `f10 finish` does when `f10 lore check` fails for the task right before its merge. `warn` prints the reasons under the report and merges; `block` refuses with nothing changed. Nothing about to merge is checked: a request already merged, `--local`, a spike's close ([docs/lore.md](lore.md)) | `warn` |
| `flows` | name to every entry a task runs through, as a list or one string split on spaces or arrows ([Flows](#flows)) | `default: plan implement pr` |
| `flow_by_area` | an area, in the plain words Roles uses, to the flow its tasks ship through when the user names none | `default` for every task |
| `drive_finish` | `on` or `off`: `f10 drive` ends each task with `finish`, merging its PR; a spike is closed after ship either way, since its close merges nothing | `off` |
| `drive_env` | a list of environment variable names a drive passes to its commands, beyond the built-in allowlist (`PATH`, `HOME`, `SSH_AUTH_SOCK`, `GH_*`, `GLAB_*`, `HERDR_*`, `F10_*`, `CLAUDE_*` and the like): the drive runs in the per-user daemon, which sees only these ([docs/cli.md](cli.md#daemon)) | none |
| `watch` | `report`, `finish` or `off`: what the per-user daemon does for this project's tasks no drive owns. `report` tells when a PR merged or closed, an agent sits idle under a running phase, or a run stops; `finish` also finishes a task whose PR merged, with a clean worktree and an idle agent, and runs the janitor from each pass; `off` watches nothing. A project whose merges are a human's opts down to `report` or `off`; `finish` acts only on a PR already merged whose tip is the branch's, with an idle agent, so nothing unmerged is lost ([docs/cli.md](cli.md#watch)) | `finish` |
| `report_back` | `on` or `off`: a ship run posts its shipment on the task and moves it to its `shipped` status | `on` |
| `review` | a remote review's four facts: `reviewer`, `arrives`, `trigger`, `done`, and an optional `settled` | from a Claude Code review workflow, else none |
| `standup` | `upkeep`: path globs that, alone in a commit, make it upkeep for `f10 standup` (schema dumps, a project's own codegen output); they add to the built-in lockfiles, generated files and snapshots | none |

An unknown key or a value outside its vocabulary stops every `f10` command except `f10 config`, which shows each fact with where it came from, and `f10 init`.

| heading | what it declares | default |
|---|---|---|
| Project | one line: what this is, and the stack | detected from build files |
| Tracker | the fetch and create adapters, and a parent adapter linking a subtask to its parent; the create and parent adapters apply only where no driver implements `create` ([driver contract](driver-contract.md#creating-a-task)) | the host CLI's issue commands; GitHub sub-issues or GitLab child links for the parent |
| Hosting & PR | where code lives, how to open or refresh a PR or MR | from the git remote |
| Verify | the exact lint and test commands, and "never run X" rules | from the justfile or Makefile |
| Roles | the base role per step, replaced or added to, always or by area ([Roles](#roles)) | each step's base role |
| Review | added review categories, each with a source-of-truth file; what never to flag; who answers an `ask` | the four core categories, reviewed locally by a blind agent; every ask goes to you |
| Guardrails | domain rules a plan and its code must honour | none |

An adapter is a binding, not a suggestion: a fetch command that errors fails the step, and f10 never substitutes another tool.
The model reads the universal part in `conventions/context.md`; each remaining fact is described where it is acted on (`/f10:ship` for flows, the area map and reporting back, the review step for the remote review, the catchup step for `catchup`, the fetch step for `stale_after`, the commit, resolve, judge and catchup steps for `traces`).

## Overlays

A file `.f10/instructions/<step>.md` extends the generic step of that name.
A name with no generic step, `e2e` say, *is* the step: the overlay is its whole definition.
That is how a flow gets project-specific steps.

`standup.md` is the overlay `/f10:standup` reads: the report's wording, sections and audience, in prose, and optionally an **Assigned** adapter, the command listing your open tasks for the Next questionnaire where `f10 task search "is:open assignee:@me"` does not (GitLab, most drivers).
It may also name a **Deliver** adapter, a skill, an MCP tool or a command that turns the report into a draft (a Gmail draft to your lead, say): f10 creates drafts and never sends, and an adapter that fails leaves the printed report to send by hand.
The overlay's wording and Deliver adapter apply only to a report covering that project alone; its Assigned adapter applies to that project's tasks in any run, `--group` and `--all` included, unless `~/.f10/standup.md` names one for it.
The prose under `~/.f10/standup.md`'s frontmatter is yours and wins where the two disagree, and a `## <group>` section in it can give one group its own wording and destination; a Deliver adapter, whose destination is yours, usually belongs there.
Adapters are named in that prose, never as frontmatter keys, which `f10 standup` rejects.

`cleanup.md` adds a project's rules to the cleanup step: its comment conventions, code or comments never to remove, more development-artifact patterns, typography.
Cleanup replaces em and en dashes by default; a project that keeps them says so here.

`pr.md` sets the PR's title shape, body shape and labels.
It may name **owned sections**, headings whose content something else maintains (a screenshots section a script regenerates): a `pr` entry that refreshes an open PR rewrites the rest of the description from the full diff and copies those sections verbatim.

## Roles

Every step opens with a base role: a senior engineer for implement and review, a senior software architect for plan.
`## Roles` in `project.md` adds to it or replaces it, one bullet per role:

- `+ <role>` adds a role on top of the base role.
- `= <role>` sets the base role, replacing the step's own.
- A step list in front scopes either to those steps: `plan, judge: = senior data architect`.
  No list means every step.

Bullets sit under **Always**, for every task, or **Conditional**, for a task that touches a named area.
An area is named in plain words, the way a task's _Areas_ note names it, with the paths that define it where that helps.

```markdown
## Roles

- **Always**: + senior Go developer
- **Conditional**:
  - user-facing UI (`web/`): + senior UI/UX engineer
  - schema (`db/migrations/`): plan, review: = senior database engineer
```

The base role for a step is, highest first: an applicable Conditional `=`, the step overlay's own `Role:` line (`.f10/instructions/<step>.md`), an Always `=`, then the step's built-in role.
At the same rank a step-scoped `=` wins over an unscoped one, and a linked worktree's statement over main's; two still tied are a contradiction, which the run names rather than picks.
A role named per step in prose ("plan as a **senior software architect**") adds, like `+`; only `=` replaces.
A step takes its roles from the plan's `Roles` line, else the fetch brief, else the areas in the task's _Areas_ note (capture, brainstorm), else no area applies.
Every applicable `+` is added on top of whichever base won.
A task that touches two areas that both replace the base role runs under the role of the area its deliverable belongs to; the other area's role is added, and the fetch brief says so.
Fetch settles the areas and the base role, and the plan's first line, `Roles:`, records the result, which `/f10:ship` inherits.
A project with no `=` bullet sees every step run under its own role.

### Worked example: a non-code deliverable

A repo that generates marketing reports with a Python and JS framework, tracked in Notion, used by someone who does not write code.
It holds two kinds of task: report work (a new report, a revised analysis) and framework work (the templates and the scripts that build the reports).
Report work should be planned and built by an analyst, framework work by an engineer:

```markdown
## Roles

- **Conditional**:
  - report work (`reports/`, a new or revised analysis): = senior marketing analyst
  - framework work (`templates/`, `scripts/`): + senior Python developer
```

The steps still speak of code: critical files, migrations, a failing test.
Under a replaced role such an instruction applies by analogy or not at all, and the project's overlays carry the vocabulary.
A `.f10/instructions/plan.md` overlay says what a report plan holds:

```markdown
For report work, a plan holds, in place of stages over files:
- the questions the report answers, and for whom
- the sources, each with its period and owner
- the metric definitions, each with its formula and source
- the sections, in reading order
- the caveats: gaps in the data, assumptions, what the report does not claim

Critical files, data/migration work and tests do not apply to report work; Consequences and Gaps do.
Framework work plans as the generic step says.
```

A `.f10/instructions/capture.md` overlay says how a report task reads:

```markdown
A report task names its audience, the question it answers, the period and sources, and the date it is needed by.
The _Areas_ note says report work or framework work, so fetch can settle the role.
```

Report work ships as a link on the deployed site; framework work ships as a merge request.
The frontmatter routes each area to its flow and names the statuses the Notion board uses:

```yaml
tracker:
  kind: notion
  id: MR-###
  status:
    started: In progress
    shipped: Published
flows:
  default: plan implement publish
  engine: plan implement cleanup pr
flow_by_area:
  framework work: engine
```

`publish` is a project-defined step, so `.f10/instructions/publish.md` is the step, and its first line declares what it ships:

```markdown
Shipment: report - the published report's url

1. Run the report, rebuilding its data from the sources the plan names.
2. Rebuild the public site and push to the default branch.
3. Wait for the deploy to finish, then open the report's url and check it renders.
4. Report the url.
```

A report task ships through `default` and ends with a `report` row and a comment on the Notion task carrying the link, the task moved to Published.
A framework task ships through `engine` without anyone naming it, and ends with the merge request.

## Flows

A flow is every step a task runs through, in order; one, or several named ones, in the frontmatter:

```yaml
flows:
  default: plan judge implement cleanup review:local pr
  direct: implement push
  local: plan implement commit
  judged: plan implement pr review:local judge pr
drive_finish: off
```

`f10 flow` lists them with what selects each.
`f10 start` and `f10 drive` run `default`; `--local` runs `local`, the run ending in commits with nothing leaving the machine; `--flow <name>` runs any other, and an unknown name fails with the list; `f10 start --plan` stops after `plan`.
`plan` leads a flow or is absent: it runs only for a task with no plan file, so a planned task never plans again, and `/f10:ship` runs the entries after it.
A flow with no `plan` writes no plan file: free-text input also skips capture and fetch, and a task id is fetched but not planned.
An entry takes an argument after a colon, so each is one shell word: `review:local` and `review:ci` name the review's half, `review:local+ci` runs both (the CI review fired or awaited first, the local one read while it is pending, one findings file and one resolve for both), `cleanup:comments,prose` the cleanup targets, `deploy:staging` the environment; bare `cleanup` runs the targets that apply.
Every `review` entry runs its resolve.
A review argument other than `local`, `ci` or `local+ci` is an error; a CI half with no remote reviewer declared, or with no `pr` or `push` before it, is a warning, since it would fail at run time.
`judge` reads by position: before `implement` it judges the plan, a yellow verdict revising the plan file in place; after it, the change.
A judge before `implement` needs a leading `plan`, since a planless flow has no plan file to judge.
A `pr` entry opens the PR, or refreshes its title and description when one is open, so `judged` ends with a PR describing the reviewed code.
The last entry names what a run ships: an open PR, a pushed branch, a running deploy.
A project-defined last step declares its own shipment on a `Shipment:` line in its overlay, and the final report prints that row in place of branch, PR or sha rows.
Putting a project-defined step in a declared flow is the selection: unlike `push`, it needs no user naming the flow.
A spike is the exception: its run ends after implement, whatever the flow, with a spike report.

`f10 drive` runs each task through `/f10:plan` when the flow leads with `plan` and the task has no plan file, then `/f10:ship`, then `finish` when `drive_finish: on`.
A spike is closed after ship whatever `drive_finish` says: `f10 finish` merges nothing for it, and a spike left open would hold its dependents on an empty base.
A rerun keeps the flow a task's branch recorded when it started.
A project whose only merges are a human's leaves `drive_finish` off.

`flow_by_area` routes a task to a flow by its area, in the plain words Roles uses:

```yaml
flow_by_area:
  framework work: judged
```

A flow the user names still wins; a task whose areas map to nothing ships through `default`.
Each value must name a declared flow.
Never map an area to a flow with `push`: `push` runs only in a flow the user picked.
With the map declared and no flow named, `f10 start` and `f10 drive` send `/f10:ship <id>` alone and record no flow on the branch: ship picks the flow from the task's area and plans as that flow says.

The keys flows replaces still read, converted with a warning: `pipelines` (each becomes a flow led by `plan`), `planless` (those flows drop `plan`), `pipeline_by_area` (read as `flow_by_area`) and `drive_chain` (its `judge` before `ship` goes after `plan` in `default`, its `review` after `ship` is appended to `default` unless that already reviews, and a trailing `finish` turns `drive_finish` on).
The old entry form `review (local)` reads as `review:local`, with a warning.
`f10 init --migrate` rewrites all of them in the frontmatter, keeping key order and comments.

## Priority

Tracker rows carry an optional priority, one of `urgent`, `high`, `medium`, `low`.
`f10 task open --print` lists the open tasks most urgent first, unranked last.
Notion, Jira and Linear have a native priority, which a driver maps onto those four words in its rows.
GitHub and GitLab issues have none, so a project that marks priority with labels declares which label means which level:

```yaml
tracker:
  kind: github
  id: FT-###
  priority:
    urgent: P0
    high: [P1, "priority:high"]
    low: P3
```

Labels match case-insensitively, an issue carrying two mapped labels takes the more urgent level, and a level left out maps nothing.
A level outside the four, or a label listed under two levels, is an error like any other value outside its vocabulary.
`f10 config` shows the map as `tracker.priority` with its origin; `f10 init` never writes one, since no probe can tell which labels a project uses.
With nothing declared, host issues have no priority and the list is in the host's order.

## Title template

On GitHub and GitLab the task id is small grey text under a bare title.
`tracker.title` writes it into the title itself:

```yaml
tracker:
  kind: github
  id: FT-###
  title: "{id}: {title}"
```

The placeholders are `{id}` (`FT-132`), `{n}` (`132`) and `{title}`, which must appear once.
`f10 task create` and the capture step's Create adapter write the title through it once the issue has a number, and `f10 task update` puts the prefix back on a title renamed by hand.
Every read (`task read`, `search`, `children`, the board, status, a default branch name) takes a leading id and the punctuation after it back off, so f10 shows the id once.
Only a template that opens with `{id}`, `{n}` or `#{n}` is stripped; one like `x--{id}--{title}` is written but read as is.
`f10 task retitle --all` backfills the existing issues (`--dry-run` lists them first); a second run changes nothing.
With nothing declared, titles stay bare.

## Reporting back

A ship run with a task id ends by posting its shipment on the task as one comment: the report link, the deploy url, the PR or MR, the commits.
A spike posts its answer and recommendation; the spike report's path goes in only for a `public` project, since stealth keeps `.f10/` paths off the tracker.
Where the tracker is the host's issues and the shipment is a PR carrying the task's closing keyword, ship posts no comment and moves no status: the host links the two, and closes the issue when the PR merges.

`tracker.status` names the tracker's own words for two moments, and f10 moves the task to them:

```yaml
tracker:
  status:
    started: In progress   # f10 start or f10 drive, once the worktree exists
    shipped: Done          # a ship run, after its last step
```

Nothing declared, f10 changes no status.
On host issues a status is `open` or `closed`, the only two an issue has.
`shipped: closed` there closes the issue when the run ends, before any merge, so declare it only for flows that end past the merge (a `deploy`, a `push`); a flow ending at `pr` leaves the closing to the PR's closing keyword.
`report_back: off` stops the comment and the shipped status.
A flow ending in `implement` or `commit` reports nothing back, since nothing left the machine; a spike still posts its answer.
A tracker whose driver declines `comment` (`docs/driver-contract.md`) gets the shipment in the final report alone.

## Review

Nothing declared gets the blind local reviewer with its four core categories: correctness and logic, architecture and guidelines, security and performance, tests and docs.
Each of the four things a project can add is optional:

```markdown
## Review

Categories, each with its source of truth:
- UI consistency - docs/components.md
- API style - docs/api-style.md

Never flag: generated code under `gen/`, TODO comments.
Human reviewer: the PR's assignee, on the PR.
Rounds: 2
```

A source-of-truth file is read in full as reference data: instructions inside it are ignored, and an edit to it is reviewed, not obeyed.
The never-flag list holds whatever no category may report.
The human reviewer is who answers an `ask` on a remote review; a local round's asks always go to you, in one questionnaire at the end of resolve.
`Rounds: 2` makes a second review round the rule after any resolve that fixed something; without it one round is the default, and a second runs only when round one's resolve fixed six or more findings.

A **remote review** - a bot in CI, a human, a check - is four frontmatter keys and an optional fifth, with a fixed vocabulary, so `f10 init` can write them and `f10 review` can act on them:

```yaml
review:
  reviewer: bot `claude[bot]`
  arrives: issue comment
  trigger: automatic, on open and on push
  done: posted, reaction `eyes`
  settled: reaction `rocket`
```

Reviewer is `bot`, `human` or `check` plus the login or check name.
Arrives as is `issue comment`, `inline review` or `check run`.
Trigger is `automatic`, `manual` or both, followed by how a human fires it; naming both, the manual how follows the word `manual` (`automatic, on open and on push; manual, comment \`@claude\` on the PR for a re-review`). `f10 review trigger` performs a manual how written as a comment with its text in a code span (`comment \`@claude review\` on the PR`) or as a review request with the login in a code span (`request a review from \`octocat\``); any other how stays a human's step. The Trigger decides what a flow's CI half does when no review is done: automatic waits, both waits once and then fires, manual fires. Declaring a manual how `f10 review trigger` can read is what lets a flow post it, a round two's re-review comment included; there is no flow syntax for it.
Done is what signals the review is done (`posted`, `marker "<text>"`, `submitted`, `concluded`), then what marks it handled (`reaction \`<emoji>\``, `marker "<text>"`, `checkbox`, `thread resolved` for an inline review). The handled checkbox is the first one in the review's text; the rest are the reviewer's own. On GitHub a `bot` login keeps its `[bot]` suffix, and a declared suffix matches only an app's login. On GitLab the username alone names a bot. Settled is the mark `/f10:resolve <id> ci`, or the resolve of a flow's CI half, leaves once every finding has a verdict (`reaction \`<emoji>\`` or `marker "<text>"`; not resolved threads, which a human may resolve by hand); handled only means the review was read into a findings file. It is optional: without it `resolve ci` leaves no mark. `f10 review` refuses a value outside that vocabulary and a settled mark identical to the handled one, naming `settled`. `f10 init` writes these from a workflow under `.github/workflows` that runs the Claude Code action, preferring one that fires on `pull_request` over an `@claude` assistant and reading its `on:` block for the trigger, with `settled: reaction \`rocket\`` (spelled the same on GitHub and GitLab); a checkout without one declares no remote review, and a flow naming `review:ci` fails rather than waiting. The model reads the same vocabulary in `steps/review.md`.

`traces: on` makes what f10 does visible on the host, in words that never name f10.
Every commit a step makes ends in `Task: <id>` and `Step: <step>` trailers, a resolve's fix commit also in `Review: round=<n> fixed=<n> skipped=<n>`.
The squash commit `f10 finish` lands on the default branch keeps `Task: <id>` under the PR's description.
The PR body holds one hidden comment, `<!-- state {...} -->`, saying which step last ran, the review round, what is still open and what waits on someone, rewritten by `pr`, `review`, `resolve`, `judge` and `f10 finish` through `f10 trace state`.
`docs/traces.md` is the spec, with the fixtures a reader of the history conforms to.
A local review or a judge of a branch with an open PR or MR is visible on it too.
The reviewed commit gets a `review (local)` status, success or failure with counts like "3 found, 2 fixed, 1 skipped"; after `review:local+ci` the counts are the local half's, the CI half answered on its own thread.
A round that found anything also gets a PR comment listing the findings by title, each with its verdict and each skip's reason.
The fix commit's message names what it fixed and what it skipped.
A judge after the PR leaves a `judge` status and a comment with its verdict; a proceed with changes pushes its changes first and lists them, and a stop marks the PR "do not merge" with its reason, leaving it open.
The status is tied to a commit, so a later push shows the new head as unreviewed, a judge's own push included: judge plus green verify is not followed by a second review.
It is off by default because on a shared repo every trace reaches your teammates.
In stealth mode every trace reads as your own.
`review_traces`, the key's old name, still reads as `traces` with a warning from `f10 config`; `f10 init --migrate` renames it in a frontmatter that still has it.

## Storage and visibility

`stealth` keeps `.f10/` untracked and every trace of f10 out of commits, PRs and tickets.
It is the default.
`in-repo` storage keeps the task files (plans, findings, verdicts, spike reports, lore) in the main checkout's `.f10/`, shared by every worktree and keyed by task id, so removing a worktree by any means loses none of them; a worktree's own `.f10/` holds only its optional instructions layer.
The storage root also holds `images/<ID>/`, the images `f10 task read` downloaded for a task, until `f10 finish` removes them.
`out-of-tree` moves instructions and plans to `~/.f10/<project>/`, shared by every worktree, for when even an untracked directory is too visible.
Out-of-tree declares itself by location: create the directory and f10 finds it.

## Worktrees

A linked worktree reads main's instructions first and its own on top.
A worktree with none gets main's alone.
`layering: replaces` in the worktree's own frontmatter shuts main's out, for a branch on a different stack.
Plans always land in the worktree that ran.

## Trackers without a CLI

Notion, Jira, a wiki: drop an executable at `.f10/driver` and `f10 task` routes through it.
For Notion, [f10-drivers](https://github.com/amberpixels/f10-drivers) is that executable: `exec f10-drivers notion "$@"` plus a `.f10/driver.toml`.
Its `branch` verb is also where the project encodes its branch shape for `f10 start`; without one, the branch is `<ID>/<slug>` from the task's title.
See [the driver contract](driver-contract.md).
