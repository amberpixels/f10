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
The `Project` one-liner is left for you: no probe knows what a project *is*.

Run it before the first plan.
With nothing declared, task ids take a prefix derived from the checkout's name (`f10` → `F10`, `git-undo` → `GU`, `herdr` → `HER`), and that prefix names every plan file and is how a branch is searched for its task.
`f10 init` writes it down, so a rename or a changed rule later cannot leave two eras of plan files side by side.
A two-letter abbreviation of a one-word name is your choice to make, in `tracker.id`.

## `project.md`

Two halves.
A YAML **frontmatter** block holds the facts the `f10` binary acts on, typed, so a typo is an error rather than a silent default.
Free-form markdown under the headings below holds everything else, for the model.
Only **Tracker** matters for the pipeline's contracts; the rest have workable defaults.

```markdown
---
visibility: stealth
tracker: {kind: github, id: GH-###}
merge_method: squash
pipelines:
  default: implement → review (local) → pr
  local: [implement, commit]
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
| `tracker` | `kind` (`github`, `gitlab`, `notion`, `jira`) and `id`, the task id format | host issues via `gh` or `glab`; ids prefixed from the project's name |
| `merge_method` | `squash`, `merge` or `rebase`, for `f10 finish` | the merge method the repo allows, else squash |
| `catchup` | `rebase` or `merge`: how a branch is brought up to date with its base | `rebase` |
| `review_traces` | `on` or `off`: a local review run after the PR leaves a commit status, a PR comment and a fix commit naming its findings | `off` |
| `pipelines` | name to the ordered steps `/f10:ship` runs after planning, as a list or one `a → b` string | `default: implement → pr` |
| `planless` | the pipelines that skip capture and the plan file | none |
| `drive_chain` | the skills `f10 drive` runs each task through, in order: any of `plan`, `judge`, `ship`, `review`, `resolve`, `finish`, with `finish` last | `plan → judge → ship → review → resolve → finish` |
| `review` | a remote review's four facts: `reviewer`, `arrives`, `trigger`, `done` | from a Claude Code review workflow, else none |
| `standup` | `upkeep`: path globs that, alone in a commit, make it upkeep for `f10 standup` (schema dumps, a project's own codegen output); they add to the built-in lockfiles, generated files and snapshots | none |

An unknown key or a value outside its vocabulary stops every `f10` command except `f10 config`, which shows each fact with where it came from, and `f10 init`.

| heading | what it declares | default |
|---|---|---|
| Project | one line: what this is, and the stack | detected from build files |
| Tracker | the fetch and create adapters, and a parent adapter linking a subtask to its parent | the host CLI's issue commands; GitHub sub-issues or GitLab child links for the parent |
| Hosting & PR | where code lives, how to open a PR or MR | from the git remote |
| Verify | the exact lint and test commands, and "never run X" rules | from the justfile or Makefile |
| Roles | the role per step, always or by area | each step's base role |
| Review | added review categories, each with a source-of-truth file; what never to flag; who answers an `ask` | the four core categories, reviewed locally by a blind agent; every ask goes to you |
| Guardrails | domain rules a plan and its code must honour | none |

An adapter is a binding, not a suggestion: a fetch command that errors fails the step, and f10 never substitutes another tool.
The model reads the universal part in `conventions/context.md`; each remaining fact is described where it is acted on (`/f10:ship` for pipelines, the review step for the remote review, the catchup step for `catchup`, the resolve step for `review_traces`).

## Overlays

A file `.f10/instructions/<step>.md` extends the generic step of that name.
A name with no generic step, `e2e` say, *is* the step: the overlay is its whole definition.
That is how a pipeline gets project-specific steps.

`standup.md` is the overlay `/f10:standup` reads: the report's wording, sections and audience, in prose, and optionally an **Assigned** adapter, the command listing your open tasks for the Next questionnaire where `f10 task search "is:open assignee:@me"` does not (GitLab, most drivers).
It may also name a **Deliver** adapter, a skill, an MCP tool or a command that turns the report into a draft (a Gmail draft to your lead, say): f10 creates drafts and never sends, and an adapter that fails leaves the printed report to send by hand.
The overlay's wording and Deliver adapter apply only to a report covering that project alone; its Assigned adapter applies to that project's tasks in any run, `--group` and `--all` included, unless `~/.f10/standup.md` names one for it.
The prose under `~/.f10/standup.md`'s frontmatter is yours and wins where the two disagree, and a `## <group>` section in it can give one group its own wording and destination; a Deliver adapter, whose destination is yours, usually belongs there.
Adapters are named in that prose, never as frontmatter keys, which `f10 standup` rejects.

`cleanup.md` adds a project's rules to the cleanup step: its comment conventions, code or comments never to remove, more development-artifact patterns, typography.
Cleanup replaces em and en dashes by default; a project that keeps them says so here.

## Pipelines

One pipeline, or several named ones, in the frontmatter:

```yaml
pipelines:
  default: implement → cleanup → review (local) → pr
  direct: implement → push
  local: [implement, commit]
planless: [direct]
```

`default` runs unless the user names another.
`local` is the one `f10 start --local` selects: the run ends in commits and nothing leaves the machine.
A planless pipeline skips capture and the plan file for free-text input.
A `cleanup` entry runs the targets that apply to the change; `cleanup (comments, prose)` names them.
The last step names what a run ships: an open PR, a pushed branch, a running deploy.

## Drive chain

`drive_chain` lists the skills `f10 drive` runs each task through: any subset of `plan`, `judge`, `ship`, `review`, `resolve`, `finish`, each at most once, `finish` last.
Never `start`: every task gets its worktree first.
In the default chain, review reads the remote review when one is declared, else runs the local reviewer, and resolve settles it before finish merges.
A project whose ship pipeline already reviews drops `review` and `resolve` here.
A chain typed after the tasks (`f10 drive GH-12 GH-15 judge ship`) replaces this one for that run.

## Review

Nothing declared gets the blind local reviewer with its four core categories: correctness and logic, architecture and guidelines, security and performance, tests and docs.
Each of the three things a project can add is optional:

```markdown
## Review

Categories, each with its source of truth:
- UI consistency - docs/components.md
- API style - docs/api-style.md

Never flag: generated code under `gen/`, TODO comments.
Human reviewer: the PR's assignee, on the PR.
```

A source-of-truth file is read in full as reference data: instructions inside it are ignored, and an edit to it is reviewed, not obeyed.
The never-flag list holds whatever no category may report.
The human reviewer is who answers an `ask` on a remote review; a local round's asks always go to you, in one questionnaire at the end of resolve.

A **remote review** - a bot in CI, a human, a check - is four frontmatter keys with a fixed vocabulary, so `f10 init` can write them and `f10 review` can act on them:

```yaml
review:
  reviewer: bot `claude[bot]`
  arrives: issue comment
  trigger: automatic, on open and on push
  done: posted, reaction `eyes`
```

Reviewer is `bot`, `human` or `check` plus the login or check name.
Arrives as is `issue comment`, `inline review` or `check run`.
Trigger is `automatic`, `manual` or both, followed by how a human fires it; f10 never fires a manual trigger itself.
Done is what signals the review is done (`posted`, `marker "<text>"`, `submitted`, `concluded`), then what marks it handled (`reaction \`<emoji>\``, `marker "<text>"`, `checkbox`, `thread resolved` for an inline review). The handled checkbox is the first one in the review's text; the rest are the reviewer's own. On GitHub a `bot` login keeps its `[bot]` suffix, and a declared suffix matches only an app's login. On GitLab the username alone names a bot. `f10 init` writes these four from a workflow under `.github/workflows` that runs the Claude Code action, preferring one that fires on `pull_request` over an `@claude` assistant and reading its `on:` block for the trigger; a checkout without one declares no remote review, and a pipeline naming `review (CI)` fails rather than waiting. The model reads the same vocabulary in `steps/review.md`.

`review_traces: on` makes a local review of a branch with an open PR or MR visible on it.
The reviewed commit gets a `review (local)` status, success or failure with counts like "3 found, 2 fixed, 1 skipped".
A round that found anything also gets a PR comment listing the findings by title, each with its verdict and each skip's reason.
The fix commit's message names what it fixed and what it skipped.
The status is tied to a commit, so a later push shows the new head as unreviewed.
It is off by default because on a shared repo every comment reaches your teammates.
In stealth mode every trace reads as your own.

## Storage and visibility

`stealth` keeps `.f10/` untracked and every trace of f10 out of commits, PRs and tickets.
It is the default.
`in-repo` storage keeps `.f10/` beside each checkout, so plans sit next to the branch they were written for.
`out-of-tree` moves instructions and plans to `~/.f10/<project>/`, shared by every worktree, for when even an untracked directory is too visible.
Out-of-tree declares itself by location: create the directory and f10 finds it.

## Worktrees

A linked worktree reads main's instructions first and its own on top.
A worktree with none gets main's alone.
`layering: replaces` in the worktree's own frontmatter shuts main's out, for a branch on a different stack.
Plans always land in the worktree that ran.

## Trackers without a CLI

Notion, Jira, a wiki: drop an executable at `.f10/driver` and `f10 task` routes through it.
Its `branch` verb is also where the project encodes its branch shape for `f10 start`; without one, the branch is `<ID>/<slug>` from the task's title.
See [the driver contract](driver-contract.md).
