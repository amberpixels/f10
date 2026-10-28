# Convention · context - per-project instructions

f10 is **generic**.
Everything project-specific (tracker, stack, roles, verify commands, PR flow, review flow, domain guardrails) lives in the project under **`.f10/instructions/`**.
Every f10 run starts by loading it.

## Vocabulary

- **skill** - an entry point the user invokes (`/f10:*`).
  `/f10:start`, `/f10:drive`, `/f10:finish`, `/f10:status`, `/f10:board` and `/f10:read` run the binary and no step.
- **step** - a unit of work in `steps/*.md`: `brainstorm`, `debug`, `capture`, `fetch`, `plan`, `judge`, `explain`, `catchup`, `implement`, `cleanup`, `commit`, `pr`, `push`, `review`, `resolve`, `demo`, `deploy`, `standup`.
  A skill runs one or more.
  A skill owns the invocation (argument, modes, authorization, where the session stops); a step owns the work.
  Where they disagree, the skill wins on the invocation and the step on the work.
- **stage** - one ordered unit *inside* a plan.
  Never a step or a skill.
- **task id** - the tracker id, in the project's format.
  It names the plan file, the branch and the report's rows.
  A planless run from free text has none, and no step may invent one.
- **task kind** - `feature`, `bug`, `refactor` or `spike`; fetch settles it and the plan records it as a `Kind:` line, absent meaning feature.
  Not the frontmatter's `tracker.kind`.
- **plan** - `<storage root>/plans/<TASK-ID>.md`.
  Bare `plan` is ambiguous: write `/f10:plan` for the skill, the **plan** step for the step.
  "Run the **plan** step" means execute its instructions, never re-invoke the skill.
- **findings** - `<storage root>/reviews/<TASK-ID>/<round>.md` (`steps/review.md`).
- **verdict file** - `<storage root>/verdicts/<key>/<n>.md`, one per judge run, `<key>` the task id or, for an idea, a kebab slug of it (`steps/judge.md` point 8).
- **spike report** - `<storage root>/spikes/<TASK-ID>.md`, a spike's answer (`steps/implement.md`).
- **shipment** - what the ship flow leaves, named by its **last delivering entry**, one of `implement`, `commit`, `push`, `pr`, `deploy` or a project-defined step with a `Shipment:` line: verified code (`implement`, reported in prose), local commits (`commit`: a sha), pushed commits (`push`: the branch and a sha), an open PR/MR (`pr`: its url), a running environment (`deploy`: its url), or a project-defined step's output.
  A project-defined step declares its own in its overlay, on a `Shipment:` line naming each row, its label and what it holds (`Shipment: report - the published report's url`).
  It carries no explicit-selection rule like `push`'s: the project naming it in a flow it declares is the selection.
  A `judge` or `demo` after it adds rows (a demo's report path or url), never replaces them: a flow ending `pr judge demo` ships the PR.
  A spike's shipment is its spike report, whatever the flow.

**Step headers.**
A step's `Role:` is its base role unless `project.md → Roles` or the step's overlay replaces it (the Roles bullet below); added roles go on top.
Under a replaced role, a step instruction phrased for code (critical files, migrations, a failing test) applies by analogy or not at all, and the project's overlay for that step says which.
`Context:` names the `project.md` sections the step reads; its same-named overlay (`.f10/instructions/<step>.md`) extends it.

## Reading the project load

`f10-load-project.sh` prints the project's instructions, layered, the inference probes, the generic steps the skill named, and each layer's overlays for them.
Steps come only from it, never from the plugin's `steps/` read by hand.
It concatenates; you interpret `project.md` and bind adapters.
If it is ever unavailable, the points below say what to do by hand:

1. **`storage root:`** is absolute, anchored to the checkout root (`git rev-parse --show-toplevel`), never the cwd.
   Use it verbatim, never re-derive it.
   The project facts file is the first layer's `project.md` (point 2).
2. **Layers.**
   The main checkout (first `git worktree list` path) and a linked worktree can each hold `.f10/instructions/`; **main's prints first, the worktree's on top**.
   A worktree with none gets main's; `layering: replaces` in a worktree's frontmatter drops main's.
   Plans never layer: they go to this run's storage root.
   A `.f10/instructions/` *below* the checkout root is ignored.
   Neither checkout holding one, the loader tries **`~/.f10/<project>/instructions/`** (`<project>` = main checkout's basename, or the cwd's outside git), which makes the whole storage root out-of-tree (**Storage** below).
3. **Precedence is scope-major, specificity-minor**: the generic step, then `main/project.md`, `main/<step>.md`, `worktree/project.md`, `worktree/<step>.md`.
   The last statement stands, except for the base role, which the Roles bullet below ranks.
   A field the worktree does not mention keeps main's value.
   Incompatible stacks call for `replaces main`, never a field-by-field merge.
   A contradiction you cannot resolve: say so rather than pick.
4. **`instructions files:`** names every `.md` per layer; layer blocks and `absent:` cover only requested names.
   A flow entry with no generic step (like `e2e`) is a **project-defined step**: its `.f10/instructions/e2e.md` *is* the step.
5. **Inferred defaults**, when `project.md` is absent or partial: the remote host gives `gh`/`glab`; the project name gives the task id prefix (`f10` is `F10`, `git-undo` is `GU`, `herdr` is `HER`); `go.mod` / `Gemfile` / `package.json` give stack and role; `justfile` / `Makefile` give verify commands.
   State them once, the prefix included, and suggest `f10 init`, which writes `.f10/instructions/project.md` from the same probes.
   Never refuse to run for missing config.

## `project.md` contract

A YAML frontmatter block holds the facts the `f10` binary acts on (an unknown key or value is an error), then prose under headings holds the rest.
Every heading is optional, and only **Tracker** matters for the pipeline's contracts.
A fact belongs to the step or skill that acts on it, which says what it means: `flows`, `flow_by_area`, `report_back` and `tracker.status` to `/f10:ship` (`flows` and `tracker.status`'s `started` also to `f10 start` and `f10 drive`), `drive_finish` to `f10 drive`, `review` to the **review** step, `catchup` to the **catchup** step, `stale_after` to the **fetch** step, `traces` to the **commit**, **resolve**, **judge** and **catchup** steps and `f10 finish`.
The full reference for humans is `docs/project-setup.md`.

- **Project** - one line: what this is and its stack.
- **Tracker** - kind and **task id format** in the frontmatter's `tracker:` (`ABC-####`, `GH-###`); under the heading, the **fetch** and **create** adapters, a skill or a CLI command (`gh issue view <n> --comments`, `gh issue create`), and an optional **parent** adapter that links a subtask to its parent (`steps/capture.md`).
- **Hosting & PR** - how to open or refresh a PR/MR: a skill, or `gh pr create` / `glab mr create` and `gh pr edit` / `glab mr update` mechanics (branch naming, labels, assignee).
  The branch's *shape* is the project's; that it carries the task id is not (`steps/pr.md`).
- **Verify** - the exact lint and test commands, plus any "never run X" rules.
- **Roles** - **Always** for every run, **Conditional** when the task touches a named area.
  A bullet `+ <role>` adds a role on top of the base role ("+ senior UI/UX engineer on user-facing UI"); `= <role>` sets the base role, replacing the step's `Role:` line.
  A step list scopes either (`plan, judge: = senior marketing analyst`); none means every step.
  A role named per step in prose ("plan as a **senior software architect**") adds, like `+`; only `=` replaces.
  The base role is, highest first: an applicable **Conditional** `=`, the step overlay's own `Role:` line, an **Always** `=`, the generic step's `Role:`.
  At the same rank a step-scoped `=` beats an unscoped one, and the worktree's beats main's; two left tied are a contradiction, said rather than picked.
  Every applicable `+` adds on top of whichever base won.
  `fetch` settles the areas and the base role; two applicable Conditional `=` roles leave the deliverable's area as the base and the other as an added role, named in the brief.
  The plan's `Roles` line records the result for `/f10:ship`.
  A step takes its roles from the plan's `Roles` line, else this run's fetch brief, else the areas in the task's _Areas_ note (capture, brainstorm), else no area applies.
- **Review** - categories, a never-flag list and a human reviewer (`steps/review.md`).
- **Guardrails** - domain rules the plan and its code must honour: a UI component gallery, PII handling, preferred dependencies.

Every **adapter** is a binding: missing, erroring or ambiguous fails that step (`conventions/failure.md`); never substitute a different tool.

**Visibility** is frontmatter `visibility: stealth | public`, **stealth** when absent (below).
**Storage** is frontmatter `storage:`.
**in-repo** (default): storage root the main checkout's `.f10/`, shared by every worktree and untracked per Visibility.
Plans, findings, verdicts, spike reports and lore are keyed by task id, so parallel tasks never share a file and removing a worktree, by any means, loses none of them.
A worktree's own `.f10/` holds only its optional `instructions/` layer.
A worktree whose `.f10/` already holds `plans/`, `reviews/`, `verdicts/` or `spikes/` predates this and keeps its own storage root until it finishes; the loader prints a note saying so.
**out-of-tree**: storage root `~/.f10/<project>/`, shared by every worktree, nothing f10-related inside the project directory; it declares itself by location (point 2 above).
Two projects with the same basename collide there: rename one dir, or keep one in-repo.
Roots report as physical paths (symlinks resolved).

## Stealth mode

Unless `project.md` says `public`:

- `.f10/` stays untracked; prefer **`.git/info/exclude`** over `.gitignore`, so even the ignore entry is never committed.
- No f10 traces in anything that leaves the machine: no mention of f10, plan files, or `.f10/` paths in commits, branch names, PR/MR text, tracker comments, or code comments.
- Never justify code or decisions by the pipeline (no "per the plan / per step 3"); state the *domain* reason the plan recorded.
- Never commit or push files under `.f10/`.
- The `Task:`, `Step:` and `Review:` trailers `f10 trace trailers` prints, and the `Task:` line `f10 finish` puts on a squash commit, are allowed: they name a task and a step, never f10, a plan or a findings file (`docs/traces.md`).
  They exist only with `traces: on`; the default, `off`, writes none.

## Commits

Every step that commits (commit, push, pr, resolve, judge, catchup, the ship skill's catchup guard) commits this way.

- Small, conventional commits in the repo's style; no AI attribution lines; in stealth mode, no f10 traces in messages.
- **Trailers**: the message ends in the lines `f10 trace trailers` prints, as its own last `-m` paragraph.
  Run it first, on its own: `trailers="$(f10 trace trailers --step <step>)"`; a non-zero exit fails the step (`conventions/failure.md`), never a commit without its output.
  Then `git commit -m "<subject>" [-m "<body>"] -m "$trailers"`.
  With `traces: off` it prints nothing, and the message stays as it was (`docs/traces.md`).
- `--step` names the step whose work the commit carries: `implement` for implemented code, the committing step for its own work (`resolve`, `judge`, `catchup`), with any extra flags its caller names.

## Task references

Text that leaves the machine (PR/MR text and replies, commit messages, tracker task bodies) names a task other than its own by its title, and a url where the text renders one, not a bare id: `FT-70` does not autolink on GitHub, and a Jira, Linear or Notion id means nothing to a reader.

- **Text that renders links** (PR/MR bodies and replies, task bodies on a tracker whose renderer takes links): the first mention is a link titled `<ID>: <title>`, in the renderer's form (`[<ID>: <title>](<url>)` in markdown, a native link on Notion or Jira, per `steps/capture.md`).
- **Text that renders nothing** (PR/MR and task titles, commit messages, an unknown renderer): `<ID> (<title>)`, no url.
- **First mention in each text**: every body, reply, comment and commit message is read on its own, so each names the task in full once; later mentions in the same text may use the bare id.
- **Title and url** come from one `f10 task ref <id>... --md` for every task a text names (`<ID> (<title>)` lines without `--md`), answered from what f10 already read and asking the tracker only on a miss; `--fresh` only where `steps/pr.md` says.
- **An id the tracker does not know**, or a read that fails, stays bare, with no invented title; `f10 task ref` prints it bare, and it does not fail the step.
- **Fixed-shape lines keep their shape**: `After:` holds one id, bare or as the markdown link `[<ID>: <title>](<url>)` that `f10 task ref <id> --md` prints, never a tracker's native link; the binary parses it (`f10 start`, `f10 drive`, `f10 task children`) and ignores a line with anything else after the id or the link.
- The task the work belongs to keeps the form its host needs (a closing keyword such as `Closes #83`).

A tracker link is not an f10 trace: stealth mode allows it.
