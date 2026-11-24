# Concepts

These are the terms the skills and the other docs use.
The README keeps a line or two for each, and this page holds the rest.

## Pipeline

### Step

A step is the unit of work, one file in `steps/*.md`.
Skills are thin routers over steps.
[Skills and steps](skills-and-steps.md) says which lines belong in which.

### Flow

A flow is the list of steps a task runs through, in order.
A project declares its flows under `flows:` in `project.md`, and `f10 flow` lists them.
With none declared, the flow is `plan implement pr finish`.

`plan` leads a flow, and it runs only for a task with no plan file.
`/f10:ship` runs the entries after it.
A flow with no leading `plan` skips the plan file, and for free text it skips the ticket too.

An entry can take an argument after a colon, such as `review:local`, `review:ci`, `cleanup:comments,prose` or `deploy:staging`.

- `review:host` runs the host's own code review, shaped into the same findings.
- `review:local+ci` runs both reviews, with one resolve.
- `local+ci?` runs the CI review only where a remote reviewer is declared.
- A `judge` entry before `implement` judges the plan, and one after `implement` judges the change.
- A name with no generic step, such as `e2e`, is project-defined, because its `.f10/instructions/<name>.md` *is* the step.

Brainstorm, debug, judge, demo, explain, handoff, catchup and audit also run on their own, whenever you ask for them.

`f10 start` runs the `default` flow.
`--local` runs `local`, and `--flow <name>` runs any other.
[Flows](project-setup.md#flows) and [Paces](project-setup.md#paces) cover declaring them.

### Brainstorm

Brainstorm is the optional conversation before capture.
It is about what to build, whether to build it, and in which shape.
It writes nothing and draws no glyph in the status line.
It ends in a shape you hand to `/f10:capture`, or in the decision not to build.

### Judge

Judge gives a verdict on an idea, a task, a plan, a PR or a commit range.
It weighs whether the problem is real or only stated, and whether the change treats a symptom or the cause.
It also weighs what already exists, and what shape would last longer.
`--blind` runs it in a fresh agent that never saw the conversation.

Inside a flow, proceed continues the run and stop ends it blocked.
The other two verdicts, proceed with changes and rethink, open a discussion that ends in your call.
With `stale_after: 3w` in the frontmatter, a task older than three weeks is judged before it is planned.
Only a proceed verdict then lets the plan go on.
The judge creates nothing.

### Review

Review is the bug hunt, in two halves: review and resolve.

`review` gives the full diff to a **blind** reviewer, one that never saw your session.
That reviewer is the default when the project declares nothing.
It checks four core categories: correctness and logic, architecture and guidelines, security and performance, and tests and docs.
It reports at most three findings per category.
It skips style unless style affects correctness.
The findings go to a **findings file**.
A project adds categories under `project.md → Review`, each with a source-of-truth file, and lists what never to flag.

`resolve` gives each finding a verdict: `fix`, `skip` or `ask`.
`+note` adds a code comment, and `+reply` answers the reviewer.
Resolve verifies the fixes again and puts every `ask` to you in one questionnaire.
Your stated choice beats the reviewer.

One round is the default.
A second round runs when round one fixed six or more findings, when the run's pace says `review: { rounds: 2 }`, or when you ask for it.
The second round reports on every prior finding first.
There is no third round, so whatever is still open goes to you.

A review is local (before the PR) or CI or human (after it), and each flow places it or leaves it out.
A `review` entry in a flow runs both halves.
With no `review` entry, ship stops at the PR.

### Demo

Demo shows *what was built*, so you know what code you did not write does before you merge it.
It says what changed in plain words.
Then it runs a **demo script** that takes screenshots and writes a local `report.html`.
With `--hands-on`, it seeds the state and hands you the running app instead.

The executor, which runs the app for the demo, is declared and never guessed.
For a project with no `demo` overlay, demo proposes one from the project's e2e config, dev-server recipe, seed task and test user.
It writes that proposal to `.f10/instructions/demo.md`.
The overlay also says where reports go: a local file, a private published page, or both.
`--publish` or `--local` overrides that for one run.

A demo takes one shot per claim.
It takes a before and after only where something visible already existed.
Its report has rows only for the artifacts it wrote.
A demo hunts no bugs and gives no verdict.

### Explain

Explain answers *what is this*, for a reader who knows the codebase.
A change becomes two to five **pairs**, each saying what something was and what it is now.
A concept, file, package or function gets three or four sentences from the code.
It runs nothing and writes nothing.
It costs less than a demo, so run it first.

### Catchup

Catchup brings a branch up to date with its base.
The base is the branch `f10 start --after` recorded, else the default branch.
It merges or rebases, as the `catchup: merge | rebase | off` frontmatter key says, and it rebases by default.

Catchup keeps both sides' intent.
Lock files are regenerated.
Only the files that are truly ambiguous are put to you, in one questionnaire.
A red verify after a clean integration is a semantic conflict, and catchup settles it the same way.
A run with nothing to settle is a correct run.
Ship also catches up on its own when the base moved.
It does so before implement, before each `pr` entry, and before a `judge` entry that comes after implement.
Ship hands off to catchup when it meets a conflict.
Catchup pushes nothing.

### Convention

A convention is a rule every step obeys, kept in `conventions/`:

- `context.md`: loading the config, the core of `project.md`, stealth, and task references
- `latency.md`: a run's cost is its turns, not its commands
- `gaps.md`: open decisions
- `failure.md`: a step that cannot complete
- `report.md`: what a successful step prints
- `voice.md`: how a run talks and how f10 writes

A hook tells the model to load the conventions when a skill starts, and again after compaction.
The project's instructions load again on every run.

### Mode

A mode is a way of running a skill that changes how it executes, kept in `modes/`:

- `help.md`: `--help`
- `dry-run.md`: `--dry-run`
- `driven.md`: `--driven`, a run prompted from another session
- `forward.md`: a task id that another worktree holds
- `binary.md`: a skill that only runs the f10 binary and relays its output (board, finish, inbox, read, start and status)

## Start vocabulary

`f10 start` runs tasks, and these words name what it runs.
Each word has one meaning, in the commands, the messages and the docs.

- **entry**: a task or a bundle, which is one branch, one agent and one PR.
  A unit of a flow is always written "flow entry", so "entry" alone never means that.
- **bundle**: several tasks in one entry, typed `A+B`.
  The first task is the lead, and the others are its riders.
  The members ship in one PR, and nothing orders the work inside it.
- **base**: what an entry comes after, which is another entry or a branch.
- **stack**: an entry on one base, typed `B@A`.
  It is opened at the base's first commit, and its finish waits for the base's.
- **chain**: a line of stacks, each one's base the entry before it, typed `A,B,C`.
- **join**: an entry with several bases, typed `C@A@B`.
  It waits until every base has merged, then branches from the default branch.
- **proposal**: what `f10 start <ids> --propose` prints, which is the bundles, the order and the one command to run.
  It starts nothing.
- **level**: one row group of a proposal, holding the entries whose bases all sit in earlier levels.
  It is not a stage, which is a unit of a task's plan.
- **batch**: the tasks one `f10 start` started, kept as a record so that it can be stopped and resumed.
  Any task id of a batch names it to `f10 stop`, `f10 resume` and `f10 daemon logs`.
- **driven**: the mode of an agent working through its flow without the user, prompted by the daemon skill by skill (`modes/driven.md`).

The daemon is the per-user process that prompts a batch's agents.
With `--foreground`, `f10 start` does that itself.

## Artifacts

### Task

A task is the tracker item.
Its **task id** names every artifact downstream.
Its **kind** is `feature`, `bug`, `refactor` or `spike`, and the plan records it.
f10 reads the kind from the steering, a label or the description.
Only the steering or a label can make a task a spike, because a spike skips the PR.

- A bug's cause is confirmed before it is planned, by a reproduction or by evidence such as logs, records or history.
  Its fix starts with a test that fails first.
- A refactor proves that behavior did not change, with tests green before and after.
- A spike is a research task that answers a question.
  Its run ends at a spike report, and its code is never committed.

### Stage

A stage is one ordered unit inside a plan file.
It is not a step and not a skill.

### Plan file

The plan file is `<storage root>/plans/<TASK-ID>.md`, which hands the work from plan to ship.
The storage root (see [Storage](#storage)) is anchored to the checkout root, whatever directory you launched from.
A plan that exists only in chat is a failed run.
A superseded plan is archived, never edited.

The plan closes with **Consequences**, which says what the chosen design costs and when to revisit it, and then **Gaps**.
Its `Size:` line says small, normal or large, by how much the implementer still has to design.
A small plan is one stage, and its Consequences and Gaps are one-liners ending in `- none`.

### Findings file

The findings file is `<storage root>/reviews/<TASK-ID>/<round>.md`, which carries a review to resolve.
It holds the reviewed sha, then one block per finding.
Each block has a title, a summary, the file and line, the category and a concrete fix.
Resolve appends its verdicts and never edits the reviewer's text.
A task has two rounds at most.
The findings files stay in the checkout's `.f10/`, because `f10 finish` archives only the plan.

### Verdict file

The verdict file is `<storage root>/verdicts/<TASK-ID or slug>/<n>.md`, one per judge run.
It opens with a header that holds the subject, the verdict and its reason.
In a ship run, the header also records what the run decided.
The argument follows the header.
An idea with no task files its verdicts under a slug of the idea.

### Spike report

The spike report is `<storage root>/spikes/<TASK-ID>.md`, what a spike's run ships.
It holds the question, the answer, its evidence, the recommendation and the follow-up tasks.

### Audit report

The audit report is `<storage root>/audits/<YYYY-MM-DD-HHMM>-<sha7>.md`, one per `/f10:audit`.
Its header records the sha the audit ran at.
The next audit with no window starts from that sha.

### Gap

A gap is a decision only you can make.
The plan records each gap with a default, so the plan can always ship.
Filling the gaps is one optional questionnaire.

### Shipment

The shipment is what a ship run leaves, named by the flow's last step:

- `implement`: verified code
- `commit`: local commits
- `push`: pushed commits
- `pr`: an open PR/MR
- `deploy`: a running environment

A spike's shipment is its spike report.
A project whose flow has no git step still ships.

## Project binding

### Project instructions

The project instructions are `<checkout root>/.f10/instructions/`, which holds `project.md` and optional per-step **overlays**.
`project.md` keeps typed facts in its frontmatter and prose below it.
A linked worktree's instructions layer on top of main's, unless they declare `layering: replaces`.

### Layers

A project's instructions extend the generic steps in layers, and a later layer wins over an earlier one.
The layers come in this order:

1. the generic step
2. `main/project.md`
3. `main/<step>.md`
4. `worktree/project.md`
5. `worktree/<step>.md`

Inferred defaults fill in only what no layer declares.
`conventions/context.md` holds the full contract.
The loader prints all of it in one bundle, the generic step files included, so a run never reads `steps/` by hand.

A worktree with no instructions of its own takes main's whole.
A worktree that declares `layering: replaces` shuts main's out.
Plans never layer.
They go to the storage root, which by default is the main checkout's `.f10/`, so every worktree shares them.

### Role

Each step runs under a base role.
A project can replace the base role, always or by area (`= senior marketing analyst` on report work).
It can also add roles, always or by area (`+ senior UI/UX engineer` when the task touches user-facing UI).
The investigate step settles the areas and the base role, and the plan file records the roles for ship ([Roles](project-setup.md#roles)).

### Adapter

An adapter is how a capability, such as "fetch a task", "open a PR" or "verify", binds in a project.
It is a skill or a CLI command.
f10 names the capability, and the project supplies the adapter.

### Remote review

A remote review is a review that CI or a human posts on the PR after it opens.
`project.md → Review` declares it in four facts: who posts it, where it arrives, what triggers it, and what marks it done and handled.
It also declares the mark a settled review gets.
`f10 init` infers these facts from a Claude Code review workflow, and `f10 review pick|wait|ack|settle|trigger` acts on them.

- `/f10:review <id> ci` reads the latest remote review in, triggers a new one, or hands it to `/f10:resolve <id> ci`.
- `/f10:resolve <id> ci` reads the review in, settles it and marks it.

A declared human reviewer answers the asks on a remote review.
A local round's asks come to you.

### Visibility

Visibility is `stealth` (the default) or `public`.
Stealth work reads as if f10 never existed: commits, PRs, tickets and code comments never mention it.
Under stealth, `.f10/` stays untracked through `.git/info/exclude`.

### Storage

Storage is where the plans, findings and other files live.
`in-repo` is the default: the main checkout's `.f10/`, shared by every worktree.
`out-of-tree` is `~/.f10/<project>/`, which holds the instructions *and* the plans for every worktree.
It puts nothing inside the project directory.
f10 uses it when the repo has no `.f10/instructions/`.
