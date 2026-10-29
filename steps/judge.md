# Step · judge - a verdict on an idea, task, plan or PR

Role: a **senior architect, thinking as the CTO**, with the burden of proof on the idea.
Input: a raw idea, a task, a saved plan, or a diff.
Output: a verdict in chat, the argument under it, and the **verdict file** (point 8); after `pr` in a ship run, also a pushed commit (yellow) and a status and comment on the PR.
Context: after `pr` only, `project.md → Verify` and `→ Hosting & PR`, and the frontmatter's `traces`.
**It writes only its verdict file**, except a judge entry after `pr` in a ship run, whose route writes code, commits and PR traces under the ship skill's authorization; a standalone `/f10:judge` never does, on a PR included.
Name a bug only where it changes the verdict.
**Proceed** is a full outcome, but never agree because agreeing is cheaper.

1. **Gather the subject.**
   Free text: as written.
   A task id: `f10 task read <id>`, plus `f10 plan read <id>` where a plan exists.
   A PR number, url, or the current branch: `f10 pr diff [<PR>]` and `f10 pr view --comments [<PR>]` (the branch against its base when it has no PR), plus the task the view's `task:` line names and its plan.
   A sha, a range, or "last N commits": `git show <sha>` / `git diff <range>`, plus the task and plan the messages or branch point to; count commits with `git log --oneline` (`HEAD~N` skips merged branches).
   No argument: what this conversation is about; say in one line which subject you took before judging it.
   Whatever the subject, a plan's `## Consequences` are costs it already accepts: weigh them in point 5; a cost it leaves out, or `- none` on a design with an evident one, is a defect where it changes the verdict.
   A plan saved before the section existed has none: read its costs from the stages, never count the absence against it.
2. **Restate the problem, then find it in the code** with a few targeted `Read`/`Grep` calls in one message.
   Where the stated problem and the real one differ, build the judgment on that.
3. **Symptom or cause.**
   For a symptom, name where the cause lives and what fixing it takes.
   A bug's plan names its root cause under Reproduction: a fix that lands away from that cause is the symptom case.
4. **What already exists**: in the codebase, a dependency, or an open tracker task.
5. **The longer-lived shape**: the one design you would build instead, if any, grounded in what you read, its cost now against reaching it later.
   A quick hack is a hack only if a better shape is reachable now.
6. **For code: keep, reshape, or discard**, and which parts.
7. **Verdict**, one of four, each with its color:
   - **proceed** (green) - the problem is real, the shape is right, build it as it stands.
   - **proceed with changes** (yellow) - the shape is right; the named changes fold in.
   - **rethink** (orange) - the problem is real, this shape is wrong.
     For existing code, the refactor verdict.
   - **stop** (red) - do not build it: not real, already solved, or not worth the cost.

   Open the reply with its **banner**, a `diff`-tagged block; line two is the reason, under 80 characters.
   No emoji; `(!)` marks yellow and `(?)` marks orange:

   ```diff
   + ▌ PROCEED
   + ▌ <the reason, one line>
   ```
   ```diff
   + ▌ PROCEED WITH CHANGES (!)
   + ▌ <the reason, one line - what folds in>
   ```
   ```diff
   - ▌ RETHINK (?)
   - ▌ <the reason, one line - where the real problem lives>
   ```
   ```diff
   - ▌ STOP
   - ▌ <the reason, one line - what already solves it, or what it costs>
   ```

   Under it, only what is wrong.
   Points 2 to 6 are the investigation, not the reply: what holds gets one sentence at most, never a recap of the problem, a list of what to keep, or the evidence that it checks out.
   - **Green**: one or two sentences on why it holds.
   - **Yellow**: one sentence on what holds, then a numbered list, one item per change: the defect in bold (what is wrong and where, `file:line` when there is one), then the fix in one or two lines.
   - **Orange** and **red**: what is wrong with the shape, or why not to build it, in a short paragraph or two, and where the real problem lives.

   Close with the smallest thing that would change the verdict, in one line.
   In later replies, the banner returns only when the verdict changes.
8. **Write the verdict file**, every run, whatever the verdict: `<storage root>/verdicts/<key>/<n>.md`, root verbatim from `storage root:`.
   `<key>` is the task id when the subject resolves to one (point 1), else a short kebab slug of the subject; `<n>` is the next integer in that directory.
   The main agent writes it, `--blind` included; a spawned blind judge skips this point.
   The header, one `key: value` per line, in this order; the hook parses it:

   ```
   task: FT-76
   subject: plan FT-76
   verdict: proceed with changes
   reason: <the banner's line two, verbatim>
   run: ship before pr
   blind: yes
   decision: continue
   ```

   - `task:` the task id; empty for an idea.
   - `subject:` the kind and the reference: `idea <one line>`, `task <id>`, `plan <id>`, `pr <number or url>`, `branch <name>`, `range <a>..<b>`, `commit <sha>`.
   - `verdict:` exactly one of `proceed`, `proceed with changes`, `rethink`, `stop`, lowercase; anything else is recorded as a parse error.
   - `run:` `standalone`, `before plan`, `ship before pr` or `ship after pr`.
   - `blind:` `yes` on a `--blind` run; omitted otherwise.
   - `decision:` ship runs only, what the run did with the verdict (the routes below name it); omitted standalone.

   Then `## Changes` (yellow: one line per change, numbered as in the reply) and `## Argument` (the reply under the banner), neither parsed.
   Write it once the verdict is final for this run: standalone, after the reply; in a ship run, once the route is chosen.
   A verdict that changes later in the same discussion rewrites the same file.
   The file stays under the storage root: never name it or its path in a commit, PR comment, status or tracker text.
   Then, with a task id, the lore entry beside it: `f10 lore add verdict --change <task> --outcome <verdict> --gate judge --commit <HEAD's sha> --judged <task|plan|change> -m <reason>`, `task` when the subject is `task <id>`, `plan` when it is `plan <id>`; an idea writes none, and a failing `lore add` is one line in the report, never a failed step.

**Blind** (`--blind`): for work this session produced.
Gather the subject per point 1, then spawn **one fresh `general-purpose` agent, never `fork`, which inherits the whole conversation**, with the subject verbatim, the checkout root, and the instruction to load the two context calls (`f10-load-conventions.sh`, `f10-load-project.sh judge`), follow this step except point 8, write no file, and return the verdict and its argument.
The Agent tool call carries the `model` and `effort` the load's `judge` profile line names (`conventions/context.md` → Agent profiles).
Relay its verdict unchanged; you may dispute it in one line.
The agent writes nothing: you write the verdict file with `blind: yes`, your dispute, if any, under `## Argument`.

**Discussion**, of a yellow or orange verdict: open with the shape you would build and the objection you expect, keep "don't change it" live with its own cost, and offer at least one shape the author did not.
Close each turn with what is now settled and the one question still open.

**Before planning**: the fetch step runs this on a task older than the frontmatter's `stale_after`, with the subject `task <id>`.
The question is whether the task is still worth doing as written against today's code: already solved, overtaken by another task, or its premise gone.
Only **proceed** lets the run go on; every other verdict stops it blocked (the fetch step routes it), since there is no plan yet to fold changes into.
The file says `run: before plan` and `decision: continue` or `block`.

**In a ship flow** the verdict routes the run; none is a failure.
A `judge` entry before `implement` is the **plan judge**: its subject is `plan <id>`, it routes as before `pr` below, and a yellow verdict's changes fold into the plan file in place, with no code written.
After `implement` it is the **change judge**, whose route depends on whether a `pr` entry ran earlier in the same run, never on whether the branch happens to have a PR.
On a clean tree (always the case right after a `pr` entry) it judges the branch after the catchup guard (`steps/catchup.md` → **As a guard**), so its verdict is on code that merges with the base; with uncommitted work the guard is skipped, and the verdict is on the old base.

**Before `pr`** (`plan → judge → implement`, `implement → judge → pr`):

- **proceed** - continue (`decision: continue`); the verdict is one line in the report.
- **stop** - end **blocked** (`decision: block`): `f10-state.sh set ship blocked judge` and, in the same call, `f10-state.sh note "stop: <the reason, one line>" "<the smallest thing that would change the verdict>"`; then the verdict as the report, naming any durable work (commits, an open PR).
- **rethink** / **proceed with changes** - discuss it (**Discussion**, above).
  Each change or objection becomes a gap in one `AskUserQuestion` (`conventions/gaps.md`), the judge's answer first, **block the run** a real option.
  On **continue**, the ship skill folds the answers into the plan file (stages, Consequences and Gaps) and runs the next step; on **block**, end as stop, with this verdict in the note.
  `decision:` records the answer, `continue` or `block`.

**Driven** (`modes/driven.md`): proceed and proceed with changes continue, the ship skill folding the changes in without asking; the verdict and the changes are one line each in the report.
Rethink becomes one ask (objections numbered, the judge's answer first, **block the run** among the options), with `decision: asked` in the file, then `set ship blocked judge`; the answer rewrites that line.
Stop ends blocked.

**After `pr`** (`implement → pr → review (local) → judge → pr`): the code is on an open PR, so each verdict ends on the PR.
First read the PR (`f10 pr view`); `no open PR` (closed or merged since `pr` ran) or `no PR read` fails the step (`conventions/failure.md`, badge `partial` on `judge`).

- **Green, proceed** - leave the trace (below) and continue (`decision: continue`); the verdict is one line in the report.
- **Yellow, proceed with changes** - the named changes are the work.
  The ship skill folds them into the plan file as before `pr`; then make them in the project's house style, its CLAUDE.md rules and `project.md` guardrails, and run the load's `verify:` line (`conventions/context.md` → Verify); commit per `conventions/context.md` → Commits with `--step judge`, and push to the PR's branch, never forcing.
  With `traces: on`, the commit body names each change in plain words for a reader of the PR.
  No second review round.
  Red verify fails the step (`conventions/failure.md`, badge `partial` on `judge`), the changes left uncommitted and nothing posted.
  Green: leave the trace on the pushed commit and continue (`decision: continue`).
- **Orange, rethink** - discuss it (**Discussion**, above).
  Once the shape is settled, one `AskUserQuestion`: **refactor as agreed** (first, when the judge believes the agreed refactor is reachable) or **stop**.
  Refactor runs the yellow route with the agreed refactor as its changes, its comment naming the reconsidered design (`decision: refactor as agreed`); stop runs the red route.
  If the discussion clears the design, the judge changes its verdict to green, with a new banner.
- **Red, stop** - leave the "do not merge" trace, then end as stop does before `pr` (`decision: stop`).
  The PR stays open: never close it, convert it to a draft or label it.
  The report names it as durable work.

**Driven** after `pr`: green and yellow continue without asking.
Orange becomes one ask (objections numbered, **refactor as agreed** first, **stop** among the options), with `decision: asked` in the file, then `set ship blocked judge`.
The answer resumes at `judge`: refactor runs yellow, stop runs red.

**The trace** after `pr`, when the frontmatter says `traces: on` and the branch has an open PR or MR; off, every route still runs and only the status and comment are skipped.

- **Status** named `judge` on the PR's head after this step's push: `f10 pr status --name judge --state <success|failure> -m '<text>'`, with `--sha <the pushed commit>` after a yellow push; the command cuts the text to 140 characters.
  The descriptions: green `success` with the reason; yellow `success` with `proceed with changes: <n> changes`; red `failure` with `do not merge: <the reason>`, cut to fit.
  A driven orange ask sets `failure` with `rethink: waiting on a decision` before the run ends blocked; its settled color replaces that status.
- **Comment** (`f10 pr comment`, the text on stdin), never while an orange ask waits.
  Green: one line, the verdict and its reason.
  Yellow: the verdict and its reason, a line per change, the commit's short sha.
  Red: "Do not merge", the reason, and the smallest thing that would change the verdict.
- **State block** (`docs/traces.md`): `f10 trace state --step judge` after the status.
  A driven orange ask passes `--asks 1` beside its failure status, and the judge resuming on the answer passes `--asks 0` before its settled color's status.
  Nothing with `traces: off`; a non-zero exit is `partial`, never a failed step.
- Both status and comment read as the user's own (`conventions/context.md`).
  A failed post is `partial` (`conventions/failure.md` rule 7); a retry is safe, since `f10 pr comment` and `f10 pr status` never post the same thing twice.
- The report adds a `commit` row when yellow pushed, a `status` row (the sha and its state) and a `comment` row (its url) when posted.

**On failure:** an unknown task id or an unreadable PR (`conventions/failure.md`).
A verdict file that cannot be written is a failure too; the verdict stays in chat and the report carries the file's content and its path.
After `pr`, also a PR closed or merged since `pr`, red verify and a failed post (above), and a rejected push: it stops with the commit local, badge `partial` on `judge`, nothing posted; never force-push to clear it.
A **stop** verdict is success.
