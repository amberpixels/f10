# Convention · context - per-project instructions

f10 is generic, and everything project-specific lives under `.f10/instructions/`, which every run loads first.

## Vocabulary

- **skill** - an entry point the user invokes (`/f10:*`), which runs one or more steps.
  `/f10:start` (bar `--propose`), `/f10:finish`, `/f10:status`, `/f10:board`, `/f10:read` and `/f10:inbox` run the binary and no step.
- **step** - a unit of work in `steps/*.md`, named by its file.
  The skill owns the invocation: the argument, the modes, the authorization and where the session stops.
  The step owns the work, and each wins on its own side when they disagree.
- **overlay** - `.f10/instructions/<step>.md`, extending the step of that name.
  For a flow entry with no generic step, such as `e2e`, the overlay is the whole step.
- **base role** - a **senior engineer** in the project's stack, unless the step's `Role:` line names another.
  **Roles** below or the overlay can replace it.
  Under a replaced role, an instruction phrased for code applies by analogy or not at all, as the overlay says.
- **stage** - one ordered unit inside a plan, never a step or a skill.
- **batch** - the tasks one `f10 start` started, which the daemon prompts skill by skill.
  A discussion or a PR pickup has none.
- **task id** - the tracker's id, which names the plan, the branch and the report's rows.
  A planless run from free text has none, and no step invents one.
- **task kind** - one of `feature`, `bug`, `refactor` or `spike`.
  The plan records it as a `Kind:` line, and a plan without one is a feature.
  It is not the frontmatter's `tracker.kind`.
- **plan** - `<storage root>/plans/<TASK-ID>.md`.
  Write `/f10:plan` for the skill and the **plan** step for the step, because running a step means following it, never invoking the skill.
- **Other files** under the storage root are keyed the same way, each defined by its step:
  - findings in `reviews/<TASK-ID>/<round>.md`
  - verdicts in `verdicts/<task id or idea slug>/<n>.md`
  - `spikes/`, `handoffs/` and `audits/`

## Reading the project load

`f10-load-project.sh` prints the layered instructions, the resolved verify, the inferred signals, the steps the skill named and their overlays.
Steps come only from it, never from `steps/` read by hand.

1. **`storage root:`** is absolute: use it verbatim, never re-derived from the cwd.
2. **Layers.**
   The main checkout's instructions print first and a worktree's on top, and the last statement stands: the generic step, then each layer's `project.md` and `<step>.md`.
   A field the worktree does not mention keeps main's value.
   A contradiction you cannot resolve is said, not picked.
3. **Inferred defaults** fill what `project.md` leaves out:
   - the host, from the remote
   - the task id prefix, from the project name
   - the stack, from its build file
   - verify, resolved as **Verify** below says

   State them once, the prefix included, and suggest `f10 init`, which writes them down.
   Never refuse to run for missing config.
4. **With no loader**, read `.f10/instructions/` from the main checkout and the worktree, else `~/.f10/<project>/instructions/`, and apply the points above by hand.
   `layering: replaces` in the worktree's frontmatter drops main's layer.
5. **Agent profiles.**
   A step that spawns a subagent passes the Agent tool the `model` and `effort` that its profile line's `Agent tool:` part names.
   That line is `<step>.<kind>` when the plan has a kind, else `<step>`, and a line with no `Agent tool:` part passes neither.

## `project.md`

The YAML frontmatter holds the facts the binary acts on, each described where it is acted on, and an unknown key is an error.
Headings hold the rest, all optional, and `docs/project-setup.md` is the full reference.

- **Tracker** - the frontmatter's `tracker:` gives the kind and the task id format.
  The heading binds the **fetch** and **create** adapters, and optionally a **parent** adapter (`steps/capture-adapter.md`) and **Properties** (`steps/capture.md`).
- **Hosting & PR** - how to open or refresh a PR or MR: a skill, or the branch naming, labels and assignee that `f10 pr` follows.
  A task's branch always carries its task id.
- **Verify** - the lint and test commands, and any "never run X" rules.
  The load prints it resolved on a `verify:` line.
  The line reads `declared (<layer>)`, `detected`, `judgment` or `absent`.
  `detected` means the justfile's or Makefile's entries.
  Run what it names, keeping the "never run X" rules.
  A `judgment` or `absent` line is yours to settle, and the report names the commands you chose.
  A `note:` in its place means the binary is missing or too old, so settle verify the same way.
- **Roles** - **Always** roles apply to every run, and **Conditional** ones when the task touches a named area.
  A bullet `+ <role>` adds a role, and `= <role>` replaces the base role.
  A step list scopes either (`plan, judge: = senior marketing analyst`), and none means every step.
  A role named in prose adds, because only `=` replaces.
  The base role is the first of these that applies:
  1. a Conditional `=`
  2. the overlay's `Role:` line
  3. an Always `=`
  4. the step's own role

  At the same rank, a step-scoped `=` beats an unscoped one, and the worktree's beats main's.
  A tie left is said, not picked.
  The investigate step settles the areas and the base role.
  With two Conditional `=` roles, the deliverable's area gives the base, and the other is added.
  A step takes its roles from the plan's `Roles` line, else the investigation brief, else the task's _Areas_ note, else none.
- **Review** - categories, a never-flag list and a human reviewer (`steps/review.md`).
- **Guardrails** - domain rules the plan and its code must honour.

Every adapter is a binding: a missing, erroring or ambiguous one fails its step, and no other tool stands in for it.

## Storage and stealth

The storage root holds every file f10 keeps for a task, keyed by its id.
Every worktree shares it.
A worktree's own `.f10/` holds only its `instructions/` layer.

Unless `project.md` says `visibility: public`:

- `.f10/` stays untracked, through `.git/info/exclude` rather than `.gitignore`, and nothing under it is committed or pushed.
- Nothing that leaves the machine mentions f10, plans or `.f10/`.
  That covers commits, branches, PR text and comments.
- Code and decisions are justified by the domain reason, never by the pipeline ("per the plan").
- The `Task:`, `Step:` and `Review:` trailers (`docs/traces.md`) and tracker links are allowed.

## Commits

Every committing step, the ship skill's catchup guard included, commits this way.

- Small, conventional commits in the repo's style, with no AI attribution lines.
- **Trailers**: run `trailers="$(f10 trace trailers --step <step>)"` first, on its own, because a non-zero exit fails the step.
  Then `git commit -m "<subject>" [-m "<body>"] -m "$trailers"`.
  With `traces: off` it prints nothing.
- `--step` is `implement` for implemented code, and otherwise the committing step, with any flags its caller names.

## Task references

Text that leaves the machine names a task by its title, never a bare id, because an id does not autolink on most trackers.

- **Text that renders links** links the first mention of every id as `<ID>: <title>`, the work's own task included.
  Such text is a PR or task body, a comment or a published page.
  Later mentions may stay bare, and code spans, `After:` lines and trailers stay as written.
- **The binary links on the way out.** The commands that send such text link a first mention left bare: `f10 pr create`, `pr edit`, `pr comment`, `task create` and `task comment`.
  Text f10 does not send, such as a page or a skill adapter's PR body, runs `f10 refs link <file>` first.
- **Text that renders nothing**, such as a title or a commit message, names a task other than the work's own as `<ID> (<title>)`.
- Each body, reply and commit message names the task in full once, and may use the bare id after.
- **One `f10 task ref <id>... --md`** gives every title and url, and without `--md` it prints `<ID> (<title>)`.
  A trailing `[closed]` is for you, never for the text.
  Pass `--fresh` only where `steps/pr.md` says.
- An unknown id, or a failed read, stays bare with no invented title.
  It does not fail the step.
- An `After:` line holds one id, bare or as the markdown link `f10 task ref` prints, because the binary parses it.
- A closing line keeps the form its host needs, such as `Closes #83`.
