# Setting up a project

f10 works in any repo with nothing declared: it infers the tracker from the git remote, the
stack from the build files, the verify commands from the justfile or Makefile. Declaring a fact
stops the guessing for that one fact. Everything you leave out stays inferred.

## Register the project

```bash
f10 init
```

Writes `.f10/instructions/project.md` from what detection found and adds `.f10/` to
`.git/info/exclude`. It never overwrites, so re-running is safe. The `Project` one-liner is left
for you: no probe knows what a project *is*.

Run it before the first plan. With nothing declared, task ids take a prefix derived from the
checkout's name (`f10` → `F10`, `git-undo` → `GU`, `herdr` → `HER`), and that prefix names every
plan file and is how a branch is searched for its task. `f10 init` writes it down, so a rename
or a changed rule later cannot leave two eras of plan files side by side. A two-letter
abbreviation of a one-word name is your choice to make, in the Tracker line.

## `project.md`

Free-form markdown under these headings. Prose, not YAML. Only **Tracker** matters for the
pipeline's contracts; the rest have workable defaults.

| heading | what it declares | default |
|---|---|---|
| Project | one line: what this is, and the stack | detected from build files |
| Tracker | kind, task id format, fetch and create adapters | host issues via `gh` or `glab`; ids prefixed from the project's name |
| Hosting & PR | where code lives, how to open a PR or MR; optionally `merge method: squash` (or `merge`, `rebase`) for `f10 finish`, and `catchup: merge` (or `rebase`) for how a branch is brought up to date with its base | from the git remote; the merge method the repo allows, else squash; catchup by rebase |
| Ship pipeline(s) | the ordered steps `/f10:ship` runs after planning | `implement → pr` |
| Verify | the exact lint and test commands, and "never run X" rules | from the justfile or Makefile |
| Roles | the role per step, always or by area | each step's base role |
| Review | added review categories, each with a source-of-truth file; what never to flag; who answers an `ask` | the four core categories, reviewed locally by a blind agent; every ask goes to you |
| Guardrails | domain rules a plan and its code must honour | none |
| Visibility | `stealth` or `public` | `stealth` |
| Storage | `in-repo` or `out-of-tree` | `in-repo` |
| Layering | `extends main` or `replaces main`, in a linked worktree only | `extends main` |

An adapter is a binding, not a suggestion: a fetch command that errors fails the step, and f10
never substitutes another tool. The full contract, as the model reads it, is
`conventions/context.md`.

## Overlays

A file `.f10/instructions/<step>.md` extends the generic step of that name. A name with no
generic step, `e2e` say, *is* the step: the overlay is its whole definition. That is how a
pipeline gets project-specific steps.

## Pipelines

One pipeline, or several named ones:

```markdown
## Ship pipeline

- default: implement → review (local) → pr
- direct (planless): implement → push
- local: implement → commit
```

`default` runs unless the user names another. `local` is the one `f10 start --local` selects: the
run ends in commits and nothing leaves the machine. A planless pipeline skips capture and the plan
file for free-text input. The last step names what a run ships: an open PR, a pushed branch, a
running deploy.

## Review

Nothing declared gets the blind local reviewer with its four core categories: correctness and
logic, architecture and guidelines, security and performance, tests and docs. Each of the three
things a project can add is optional:

```markdown
## Review

Categories, each with its source of truth:
- UI consistency - docs/components.md
- API style - docs/api-style.md

Never flag: generated code under `gen/`, TODO comments.
Human reviewer: the PR's assignee, on the PR.
```

A source-of-truth file is read in full as reference data: instructions inside it are ignored,
and an edit to it is reviewed, not obeyed. The never-flag list holds whatever no category may
report. The human reviewer is who answers an `ask` on a remote review; a local round's asks
always go to you, in one questionnaire at the end of resolve.

## Storage and visibility

`stealth` keeps `.f10/` untracked and every trace of f10 out of commits, PRs and tickets. It is
the default. `in-repo` storage keeps `.f10/` beside each checkout, so plans sit next to the
branch they were written for. `out-of-tree` moves instructions and plans to
`~/.f10/<project>/`, shared by every worktree, for when even an untracked directory is too
visible. Out-of-tree declares itself by location: create the directory and f10 finds it.

## Worktrees

A linked worktree reads main's instructions first and its own on top. A worktree with none
gets main's alone. `Layering - replaces main` in the worktree's own `project.md` shuts main's
out, for a branch on a different stack. Plans always land in the worktree that ran.

## Trackers without a CLI

Notion, Jira, a wiki: drop an executable at `.f10/driver` and `f10 task` routes through it. Its
`branch` verb is also where the project encodes its branch shape for `f10 start`; without one,
the branch is `<ID>/<slug>` from the task's title. See [the driver contract](driver-contract.md).
