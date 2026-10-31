# Convention · context - per-project instructions

f10 is **generic**.
Everything project-specific lives under **`.f10/instructions/`**: tracker, stack, roles, verify commands, PR flow, review flow and domain guardrails.
Every f10 run starts by loading it.

## Vocabulary

- **skill** - an entry point the user invokes (`/f10:*`).
  A skill runs one or more steps.
  `/f10:start`, `/f10:drive`, `/f10:finish`, `/f10:status`, `/f10:board`, `/f10:read` and `/f10:asks` run the binary and no step.
- **step** - a unit of work in `steps/*.md`: `brainstorm`, `debug`, `capture`, `fetch`, `plan`, `judge`, `explain`, `catchup`, `implement`, `cleanup`, `commit`, `pr`, `push`, `review`, `resolve`, `demo`, `deploy`, `standup`.
  The skill owns the invocation: argument, modes, authorization and where the session stops.
  The step owns the work.
  Where they disagree, the skill wins on the invocation and the step on the work.
  A step's `Context:` line names the `project.md` sections it reads.
- **base role** - the role a step's `Role:` line names, which `project.md → Roles` or the step's overlay can replace (**Roles** below).
  Under a replaced role, an instruction phrased for code (critical files, migrations, a failing test) applies by analogy or not at all, and the project's overlay for that step says which.
- **overlay** - `.f10/instructions/<step>.md`, extending the generic step of that name.
- **project-defined step** - a flow entry with no generic step, such as `e2e`.
  Its overlay is the whole step.
- **stage** - one ordered unit inside a plan, never a step or a skill.
- **task id** - the tracker's id, in the project's format.
  It names the plan file, the branch and the report's rows.
  A planless run from free text has none, and no step may invent one.
- **task kind** - `feature`, `bug`, `refactor` or `spike`.
  Fetch settles it, and the plan records it as a `Kind:` line.
  A plan with no `Kind:` line is a feature.
  It is not the frontmatter's `tracker.kind`.
- **plan** - `<storage root>/plans/<TASK-ID>.md`.
  Bare `plan` is ambiguous, so write `/f10:plan` for the skill and the **plan** step for the step.
  "Run the **plan** step" means follow its instructions, never re-invoke the skill.
- **findings** - `<storage root>/reviews/<TASK-ID>/<round>.md` (`steps/review.md`).
- **verdict file** - `<storage root>/verdicts/<key>/<n>.md`, one per judge run (`steps/judge.md` point 8).
  `<key>` is the task id, or for an idea, a kebab slug of it.
- **spike report** - `<storage root>/spikes/<TASK-ID>.md`, a spike's answer (`steps/implement.md`).
- **shipment** - what the ship flow leaves, decided by its **last delivering entry**:
  - `implement`: verified code, in prose.
  - `commit`: local commits, a sha.
  - `push`: pushed commits, the branch and a sha.
  - `pr`: an open PR/MR, its url.
  - `deploy`: a running environment, its url.
  - a project-defined step with a `Shipment:` line: its own output.

  It declares that output on a `Shipment:` line in its overlay, naming each row, its label and what it holds (`Shipment: report - the published report's url`).
  It needs no explicit selection as `push` does, because naming it in a declared flow is the selection.
  A `judge` or `demo` after it adds rows (a demo's report path or url) and never replaces them.
  So a flow ending `pr judge demo` ships the PR.
  A spike's shipment is its spike report, whatever the flow.

## Reading the project load

`f10-load-project.sh` prints the layered instructions, resolved verify, inference probes, the generic steps the skill named and each layer's overlays for them.

Steps come only from it, never from the plugin's `steps/` read by hand.
The loader only concatenates.
You interpret `project.md` and bind adapters.
If the loader is unavailable, follow the points below by hand.

1. **`storage root:`** is absolute, anchored to the checkout root (`git rev-parse --show-toplevel`), never the cwd.
   Use it verbatim, never re-derived.
2. **Layers.**
   The main checkout (the first `git worktree list` path) and a linked worktree can each hold `.f10/instructions/`.
   **Main's prints first, and the worktree's on top.**
   A worktree with none gets main's.
   `layering: replaces` in a worktree's frontmatter drops main's.
   The project facts file is the first layer's `project.md`.
   A `.f10/instructions/` below the checkout root is ignored.
   Plans never layer, because they go to this run's storage root.
   If neither checkout holds one, the loader tries **`~/.f10/<project>/instructions/`**, where `<project>` is the main checkout's basename, or the cwd's outside git.
   Found there, it makes the storage root out-of-tree.
3. **Precedence.**
   The last statement stands, in this order: the generic step, `main/project.md`, `main/<step>.md`, `worktree/project.md`, `worktree/<step>.md`.
   The base role is the exception, ranked as **Roles** below says.
   A field the worktree does not mention keeps main's value.
   Incompatible stacks call for `layering: replaces`, never a field-by-field merge.
   A contradiction you cannot resolve is said, not picked.
4. **`instructions files:`** names every `.md` per layer.
   Layer blocks and `absent:` cover only the requested names.
5. **Inferred defaults** fill what a partial or absent `project.md` leaves out:
   - The remote host gives the PR/MR host, which the `f10 pr` and `f10 task` commands drive.
   - The project name gives the task id prefix (`f10` is `F10`, `git-undo` is `GU`, `herdr` is `HER`).
   - `go.mod`, `Gemfile` or `package.json` gives the stack and role.
   - Verify comes resolved (**Verify** below).
     With no load, use the justfile's or Makefile's lint and test entries, else the stack's idiomatic commands.

   State them once, the prefix included, and suggest `f10 init`, which writes `.f10/instructions/project.md` from the same probes.
   Never refuse to run for missing config.
6. **Agent profiles.**
   A step that spawns a subagent (review's blind reviewer, `judge --blind`, cleanup's rules check) passes the Agent tool the `model` and `effort` its profile line's `Agent tool:` part names.
   That line is `<step>.<kind>` for the plan's `Kind` when the load prints one, else the plain `<step>` line.
   A line with no `Agent tool:` part passes neither, so the tool's own defaults apply.
   Every other step runs inline, on the task agent's profile (`docs/project-setup.md` → Agents).

## `project.md` contract

A YAML frontmatter block holds the facts the `f10` binary acts on, and an unknown key or value is an error.
Prose under headings holds the rest.
The full reference for humans is `docs/project-setup.md`.

A fact belongs to whatever acts on it, which says what it means:

- `flows`, `flow_by_area`, `report_back`, `tracker.status`: `/f10:ship`.
  `flows` and `tracker.status`'s `started` also go to `f10 start` and `f10 drive`.
- `drive_finish`: `f10 drive`.
- `review`, `catchup`, `stale_after`: the **review**, **catchup** and **fetch** steps.
- `traces`: the **commit**, **resolve**, **judge** and **catchup** steps, and `f10 finish`.
- `agents`: `f10 start` and `f10 drive` (the task agent's `default`), and the **review**, **judge** and **cleanup** steps where they spawn a subagent.

Every heading is optional, and only **Tracker** matters for the pipeline's contracts.

- **Project** - one line: what this is and its stack.
- **Tracker** - kind and **task id format** go in the frontmatter's `tracker:` (`ABC-####`, `GH-###`).
  Under the heading go the **fetch** and **create** adapters, each a skill or a CLI command (`gh issue view <n> --comments`, `gh issue create`).
  An optional **parent** adapter links a subtask to its parent (`steps/capture.md`).
  An optional **Properties** bullet names the properties capture sets on a new task (`steps/capture.md`).
- **Hosting & PR** - how to open or refresh a PR/MR: a skill, or the branch naming, labels and assignee `f10 pr create` and `f10 pr edit` follow.
  The branch's shape is the project's, but a task's branch always carries its task id (`steps/pr.md`).
- **Verify** - the exact lint and test commands, plus any "never run X" rules.
  The load prints it resolved as a `verify:` line every step runs from, a multi-line declaration indented beneath it.
  It is one of:
  - `declared (<layer>)`: that layer's `project.md`.
  - `detected`: the justfile's or Makefile's `lint`, `test` and `check` entries.
  - `judgment`: a build file with none of them, or no build file, so the stack's idiomatic commands (`go test ./...`, the linter the repo configures).
  - `absent`: nothing declared or detected.

  Run what it names, keeping the "never run X" rules.
  A `judgment` or `absent` line is yours to settle, and the report names the commands you chose.
  A `note:` in place of the line means no binary, or one too old, so settle verify by hand (**Reading the project load**, point 5).
- **Roles** - **Always** roles apply to every run, and **Conditional** ones when the task touches a named area.
  A bullet `+ <role>` adds a role on top of the base role ("+ senior UI/UX engineer on user-facing UI").
  A bullet `= <role>` sets the base role, replacing the step's `Role:` line.
  A step list scopes either (`plan, judge: = senior marketing analyst`), and none means every step.
  A role named per step in prose ("plan as a **senior software architect**") adds, like `+`, because only `=` replaces.
  The base role is, highest first: a **Conditional** `=`, the step overlay's own `Role:` line, an **Always** `=`, the generic step's `Role:`.
  At the same rank, a step-scoped `=` beats an unscoped one, and the worktree's beats main's.
  Two still tied are a contradiction, said rather than picked.
  Every applicable `+` adds on top of whichever base won.
  Fetch settles the areas and the base role.
  If two Conditional `=` roles apply, the deliverable's area gives the base, and the other is an added role, named in the brief.
  The plan's `Roles` line records the result for `/f10:ship`.
  A step takes its roles from the plan's `Roles` line, else this run's fetch brief, else the task's _Areas_ note (as capture and brainstorm do), else no area applies.
- **Review** - categories, a never-flag list and a human reviewer (`steps/review.md`).
- **Guardrails** - domain rules the plan and its code must honour: a UI component gallery, PII handling, preferred dependencies.

Every **adapter** is a binding.
A missing, erroring or ambiguous one fails its step (`conventions/failure.md`).
Never substitute a different tool.

## Storage

Frontmatter `visibility:` is `stealth` or `public`, **stealth** when absent.
Frontmatter `storage:` places the storage root.

**in-repo**, the default, puts it in the main checkout's `.f10/`, shared by every worktree and untracked per the visibility.
Plans, findings, verdicts, spike reports and lore are keyed by task id.
So parallel tasks never share a file, and removing a worktree loses none of them.
A worktree's own `.f10/` holds only its optional `instructions/` layer.
A worktree whose `.f10/` already holds `plans/`, `reviews/`, `verdicts/` or `spikes/` predates this rule.
It keeps its own storage root until it finishes, and the loader says so.

**out-of-tree** puts it at `~/.f10/<project>/`, shared by every worktree, with nothing f10-related in the project directory.
It declares itself by location (**Reading the project load**, point 2).
Two projects with the same basename collide there, so rename one dir, or keep one in-repo.

Roots report as physical paths, symlinks resolved.

## Stealth mode

Unless `project.md` says `public`:

- `.f10/` stays untracked.
  Prefer **`.git/info/exclude`** to `.gitignore`, so even the ignore entry is never committed.
- Nothing that leaves the machine carries an f10 trace.
  Commits, branch names, PR/MR text, tracker comments and code comments never mention f10, plan files or `.f10/` paths.
- Never justify code or a decision by the pipeline ("per the plan", "per step 3").
  State the *domain* reason the plan recorded.
- Never commit or push files under `.f10/`.
- The `Task:`, `Step:` and `Review:` trailers `f10 trace trailers` prints are allowed, and so is the `Task:` line `f10 finish` puts on a squash commit.
  They name a task and a step, never f10, a plan or a findings file (`docs/traces.md`).
  They exist only with `traces: on`, and the default, `off`, writes none.
- A tracker link is not an f10 trace.

## Commits

Every step that commits (commit, push, pr, resolve, judge, catchup, the ship skill's catchup guard) commits this way.

- Small, conventional commits in the repo's style, with no AI attribution lines.
- **Trailers**: the message ends in the lines `f10 trace trailers` prints, as its own last `-m` paragraph.
  Run it first, on its own: `trailers="$(f10 trace trailers --step <step>)"`.
  A non-zero exit fails the step (`conventions/failure.md`), so never commit without its output.
  Then `git commit -m "<subject>" [-m "<body>"] -m "$trailers"`.
  With `traces: off` it prints nothing, and the message stays as it was (`docs/traces.md`).
- `--step` names the step whose work the commit carries.
  It is `implement` for implemented code, and the committing step for its own work (`resolve`, `judge`, `catchup`), with any extra flags its caller names.

## Task references

Text that leaves the machine (PR/MR text and replies, commit messages, tracker task bodies) names a task other than its own by its title, and a url where the text renders one, never a bare id.
The reason: `FT-70` does not autolink on GitHub, and a Jira, Linear or Notion id means nothing to a reader.

- **Text that renders links** (PR/MR bodies and replies, task bodies on a tracker whose renderer takes links): the first mention is a link titled `<ID>: <title>`, in the renderer's form.
  That is `[<ID>: <title>](<url>)` in markdown, or a native link on Notion or Jira (`steps/capture.md`).
- **Text that renders nothing** (PR/MR and task titles, commit messages, an unknown renderer): `<ID> (<title>)`, no url.
- **First mention in each text**: every body, reply, comment and commit message is read on its own, so each names the task in full once.
  Later mentions in the same text may use the bare id.
- **Title and url** come from one `f10 task ref <id>... --md` for every task a text names, and without `--md` it prints `<ID> (<title>)` lines.
  It answers from what f10 already read, asking the tracker only on a miss.
  Pass `--fresh` only where `steps/pr.md` says.
- **An id the tracker does not know**, or a read that fails, stays bare, with no invented title.
  That does not fail the step.
- **Fixed-shape lines keep their shape**: `After:` holds one id, bare or as the markdown link `f10 task ref <id> --md` prints, never a tracker's native link.
  The binary parses it (`f10 start`, `f10 drive`, `f10 task children`) and ignores a line with anything else after the id or the link.
- The task the work belongs to keeps the form its host needs (a closing keyword such as `Closes #83`).
