# Step · demo - a shipped change → evidence of what it does

Role: a **senior engineer** showing a change to someone who did not write it - adopt the roles
from `project.md → Roles`, including any conditional ones the plan recorded.
Input: a shipped change - the task, its plan, and the diff - usually behind an open PR.
Output: a **demo script**, and where the run executes it, the evidence: screenshots and a
`report.html` under `<storage root>/demo/<TASK-ID>/`.
Context: per `conventions/context.md` - how to launch, seed and drive the app comes from the
`demo` overlay (`.f10/instructions/demo.md`). There is no inferred default executor.
Badge (`conventions/report.md`): none standalone - the three glyphs are the capture → plan → ship
chain, and this inspects a finished one rather than extending it. Inside a ship pipeline,
`/f10:ship` reports it as its leaf like every other step.

`review` and `judge` answer *how* a change was built. This step answers **what it does** - the
question left standing in front of an open PR whose code you did not write. It hunts no bugs and
reaches no verdict: it produces the evidence, and the merge stays the user's call.

**One derivation, two executions.** Everything up to the execution is identical in both modes.
**Static** is the default: the step runs the script itself and captures what it sees.
**Hands-on** - `--hands-on` in the argument, or a pipeline entry naming that variant - seeds the
state, leaves the app running, and hands the script over. That shared half is why a static report
carries the script and the live url as well: taking the wheel afterwards costs nothing.

1. **Gather the change.** The same routing `steps/judge.md` point 1 specifies, for the same
   targets - a PR number or url, the current branch, a task id, no argument at all - reaching the
   diff through the host CLI or `git diff <base>...HEAD`, plus the task and the plan the branch
   resolves to. Read it there rather than restating it here, so the two cannot drift.
2. **Derive the demo script.** The artifact both modes share: the entry point, the state it needs,
   the ordered steps, and what to look for at each. Derive it from what the task **claims** and
   what the diff **actually changed** - the claims say which effects matter, the diff says which
   ones exist. Where the two disagree the script follows the diff, and the report says where they
   parted. A script derived from the task alone demonstrates the plan, not the code.
3. **Resolve the executor.** From the `demo` overlay, never inferred: how to launch the app, how
   to seed baseline state, the base url, any test credentials. Where the project already has a
   skill that launches and drives its app, the overlay declaring that skill *is* the executor -
   f10 specifies the capability and the project binds it, as with every other adapter. No overlay,
   or an overlay that declares no way to drive the app, and the run **degrades to hands-on** and
   says which fact was missing. That is a normal end, not a failure.
4. **Set the state up, out loud.** Baseline state comes from the overlay's declared seeding
   command. Derive only the delta this feature needs on top of it - the entity in the particular
   state the script walks through - and **say what you will create before creating it**. Create it
   through the app's own affordances or the declared command, never by improvising a route into a
   database, and never against one the overlay did not name.
5. **Execute, or hand over.** Static: run the script, capture evidence as you go, and note
   anything that did not behave as the script predicted. Hands-on: seed, leave the app running,
   and print the script with its urls resolved, so the first step is a link the user clicks.
6. **Before and after, only when it says something.** A second capture at the base commit is
   worth its cost in one case: the change modifies something visible that **already existed**.
   Otherwise skip it and say why in one line.
   - A feature that adds a new surface has no meaningful before - it is an empty page or a 404,
     and pairing it with the after implies a comparison that was never made.
   - A branch carrying a migration cannot hold the same state on both sides, so a pair captured
     across it compares two different worlds. Say that instead of faking the pair.
   Where it is worth it, both sides run the same script against the same seeded state, and the
   report says which commit each side is.
7. **Budget evidence by claim, not by count.** One artifact per user-visible claim the change
   makes. A button that grew and changed colour is one claim and one shot; a feature spanning five
   screens earns more. Never target a number: a count is a quota, and a quota gets filled with
   screenshots of the navigation between the interesting ones. The hands-on scenario obeys the
   same rule - the shortest path that touches every claim, and if walking it yourself would take
   more than a few minutes, it is demonstrating too much.
8. **Claim only what you captured.** Every sentence in the report points at an artifact this run
   produced, and anything the run could not demonstrate is listed separately, with the reason it
   could not. Never describe behaviour read out of the diff as though it was observed - a report
   written from the code and illustrated with a screenshot that does not show the thing is worse
   than no demo at all, because it is what gets merged on.
9. **Report, in two places.** In chat, per `conventions/report.md`: a facts block with the task,
   the PR url where there is one, the evidence directory, and the `report.html` path, then what
   was demonstrated, what was not and why, and the script, as prose.

   On disk, `report.html` in the evidence directory - where screenshots actually become readable.
   One self-contained page: each shot under the claim it evidences, before and after side by side
   where step 6 produced a pair, the script at the end, styles inline, images referenced by
   relative path, legible in a light and a dark browser. No build step and no dependency: a file
   that needs a server to open is not an artifact the user can keep.

   Write it locally and **publish nothing**. The evidence does not go to the PR, the tracker, or
   any hosted page unless the user asks for it in as many words - under stealth
   (`conventions/context.md`) it would announce the pipeline as loudly as a commit message could.

**On failure:** the app will not start, or the state the script needs cannot be reached - stop and
report per `conventions/failure.md`, saying whether anything was created during setup. A script
that ran to the end while the feature failed to appear is **not** a failure: the step did its job,
the finding goes in the report as the thing that was not demonstrated, and what to do about it is
the user's call, not this step's.
