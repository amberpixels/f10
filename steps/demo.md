# Step · demo - a shipped change → what it does, and evidence of it

Role: a **senior engineer** showing a change to someone who did not write it - adopt the roles
from `project.md → Roles`, including any conditional ones the plan recorded.
Input: a shipped change - the task, its plan, and the diff - usually behind an open PR.
Output: **what the change does**, stated in plain words, and a **demo script** that confirms it -
plus, where the run executes the script, the evidence: screenshots and a `report.html` under
`<storage root>/demo/<TASK-ID>/`.
Context: per `conventions/context.md` - how to launch, seed and drive the app comes from the
`demo` overlay (`.f10/instructions/demo.md`). There is no inferred default executor.
Badge (`conventions/report.md`): none standalone - the three glyphs are the capture → plan → ship
chain, and this inspects a finished one rather than extending it. Inside a ship pipeline,
`/f10:ship` reports it as its leaf like every other step.

`review` and `judge` answer *how* a change was built. This step answers **what it does** - the
question left standing in front of an open PR whose code you did not write. It hunts no bugs and
reaches no verdict: it produces the answer and the evidence, and the merge stays the user's call.

Answering it means **saying** what the change does, first and in plain words. A script is how a
reader confirms that; it is never the answer by itself. A run that hands back a list of things to
click has left the reader exactly where they started, which is the one outcome this step exists to
prevent.

**One derivation, two executions.** Everything up to the execution is identical in both modes.
**Static** is the default: the step runs the script itself and captures what it sees.
**Hands-on** - `--hands-on` in the argument, or a pipeline entry naming that variant - seeds the
state, leaves the app running, and hands the script over. That shared half is why a static report
carries the script and the live url as well: taking the wheel afterwards costs nothing.

1. **Gather the change.** The same routing `steps/judge.md` point 1 specifies, for the same
   targets - a PR number or url, the current branch, a task id, no argument at all - reaching the
   diff through the host CLI or `git diff <base>...HEAD`, plus the task and the plan the branch
   resolves to. Read it there rather than restating it here, so the two cannot drift.
2. **Say what changed, in the user's words.** The headline of the step and the first thing the
   report prints: what the app did before, what it does now, and what a person using it will
   notice. Derived from the diff, written in the vocabulary of someone using the feature and never
   of the code behind it - no handler names, no component names, no file paths. Two or three
   sentences for an ordinary change. Where the change is invisible from the outside, say so
   plainly rather than dressing an internal refactor up as a user-facing effect.
3. **Derive the demo script.** How a reader confirms the statement above: the entry point, the
   state it needs, the ordered steps, and what to look for at each. Derive it from what the task
   **claims** and what the diff **actually changed** - the claims say which effects matter, the
   diff says which ones exist. Where the two disagree the script follows the diff, and the report
   says where they parted. A script derived from the task alone demonstrates the plan, not the
   code.
4. **Resolve the executor - and offer to declare it when there is none.** The facts come from the
   `demo` overlay, never inferred: how to launch the app, how to seed baseline state, the base
   url, any test credentials. Where the project already has a skill that launches and drives its
   app, the overlay declaring that skill *is* the executor - f10 specifies the capability and the
   project binds it, as with every other adapter.

   **A missing overlay is a gap to fill, not a reason to give up** (`conventions/gaps.md`), and
   **the questionnaire proposes, it does not interrogate.** Look before asking - a project that
   already runs browsers answers most of this itself, in a few targeted `Read`/`Grep` calls in one
   message:

   - **what drives it** - a Playwright, Cypress, Selenium or Capybara config, an existing e2e
     suite and the command that runs it, a browser MCP server this session already has, or a
     project skill that launches and drives the app.
   - **how to launch it** - the dev-server recipe in the `justfile`, `Makefile`, `package.json`
     scripts or `Procfile`, and the port it binds.
   - **how to create what a script needs** - the console, runner or factory the test suite
     already uses, the app's own forms, or a seed task. Reusing a record that already fits is
     tried first whatever the answer, so it is **not** one of the options: this asks what to do
     when nothing fits. Nor does it ask about cleanup, which is settled below and is not a
     property of any one mechanism.
   - **how to authenticate** - the test user, the dev-login route, or the header or marker the
     project's CLAUDE.md documents.

   Then **one** `AskUserQuestion`: a question per fact, each carrying what you found as its first
   option, plus **where the report should go** (point 10). A fact nothing turned up is still asked,
   with the conservative choice first - an empty question is better than a fabricated default. On
   answers, write `.f10/instructions/demo.md` and carry straight on with the run that prompted it;
   no later `/f10:demo` in this project asks again. Never invent an answer the user declined to
   give, and never write the overlay without showing what goes in it.

   Only when the user declines does the run **degrade to hands-on**, naming the fact that is
   missing. That is a normal end, not a failure.
5. **Set the state up, out loud.** Reuse first: a record that already satisfies the script is the
   cheapest state there is, and finding one is why point 3 names the state it needs rather than
   just the steps. Only where nothing fits does the run **create** the delta, through the
   mechanism the overlay declares - never by improvising a route into a database, and never
   against one the overlay did not name. **Say what you will create before creating it.**

   **What a demo creates stays.** It is dev data, not a fixture to tear down, and the report names
   every record it made, with its id, so removing it later is a decision the user gets to make
   rather than one the step takes for them. Automatic cleanup would also be flatly wrong in
   hands-on mode, where it would delete the state the user is still clicking through.
6. **Execute, or hand over.** Static: run the script, capture evidence as you go, and note
   anything that did not behave as the script predicted. Hands-on: seed, leave the app running,
   and print the script with its urls resolved, so the first step is a link the user clicks.
7. **Before and after, only when it says something.** A second capture at the base commit is
   worth its cost in one case: the change modifies something visible that **already existed**.
   Otherwise skip it and say why in one line.
   - A feature that adds a new surface has no meaningful before - it is an empty page or a 404,
     and pairing it with the after implies a comparison that was never made.
   - A branch carrying a migration cannot hold the same state on both sides, so a pair captured
     across it compares two different worlds. Say that instead of faking the pair.
   Where it is worth it, both sides run the same script against the same seeded state, and the
   report says which commit each side is.
8. **Budget evidence by claim, not by count.** One artifact per user-visible claim the change
   makes. A button that grew and changed colour is one claim and one shot; a feature spanning five
   screens earns more. Never target a number: a count is a quota, and a quota gets filled with
   screenshots of the navigation between the interesting ones. The hands-on scenario obeys the
   same rule - the shortest path that touches every claim, and if walking it yourself would take
   more than a few minutes, it is demonstrating too much.
9. **Claim only what you captured.** Every sentence about behaviour points at an artifact this run
   produced, and anything the run could not demonstrate is listed separately, with the reason it
   could not. Never describe behaviour read out of the diff as though it was observed - a report
   written from the code and illustrated with a screenshot that does not show the thing is worse
   than no demo at all, because it is what gets merged on. Point 2 is the one statement derived
   from the diff by design, and it is written as what the change *does*, never as what the run
   *saw*.
10. **Report, in two places.** In chat, per `conventions/report.md`: the facts block first - the
    task, the PR url where there is one, and **only the artifacts this run actually wrote**. A run
    that captured nothing has no `evidence` row; a run that wrote no page has no `report` row.
    Rule 3 forbids the placeholder, and a path to a file that was never written is worse than a
    missing row, because the reader goes looking for it. Then, as prose and in this order: what
    changed (point 2), what was demonstrated, what was not and why, anything the run created and
    left behind (point 5), and the script. Where the report is a file, close with
    `f10 demo open <TASK-ID>` - the way back to it after the terminal has scrolled.

    On disk, `report.html` in the evidence directory - written in **both** modes, because the page
    is not a frame for screenshots. Static: what changed at the top, then each shot under the
    claim it evidences, before and after side by side where point 7 produced a pair. Hands-on: the
    same opening, then the script as a checklist to tick through in the browser while walking it,
    which beats scrolling back through a terminal and is what survives the session. One
    self-contained page either way: styles inline, images by relative path, legible in a light and
    a dark browser. No build step and no dependency - a file that needs a server to open is not an
    artifact anyone keeps.

    **Where that page goes is the project's declared choice**, settled by point 4's questionnaire
    and recorded in the overlay: the local file alone, a published page as well, or a published
    page only. `--publish` and `--local` in the argument override it for one run.

    Publishing makes it a **private page on the user's own account**, reachable by a url that is
    theirs to share or not - the choice is between a file they open and a link they can send, not
    between private and public. It still leaves the machine, so two rules bind it. Nothing is
    published that the overlay does not declare or the user did not ask for in as many words. And
    the page carries **no f10 traces** whatever the project's visibility (`conventions/context.md`)
    - no storage-root paths, no plan references, nothing naming the pipeline - so a page that gets
    shared later is already clean. Screenshots referenced by relative path do not survive
    publishing: upload them with the page and reference the urls the host gives back.

    Nothing else travels. The report does not go to the PR, the tracker, or any hosted page beyond
    the one the user chose.

**On failure:** the app will not start, or the state the script needs cannot be reached - stop and
report per `conventions/failure.md`, saying whether anything was created during setup. A script
that ran to the end while the feature failed to appear is **not** a failure: the step did its job,
the finding goes in the report as the thing that was not demonstrated, and what to do about it is
the user's call, not this step's.
