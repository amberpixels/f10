# Step · judge - a verdict on an idea, task, plan or PR

Role: a **senior architect, thinking as the CTO**, with the burden of proof on the idea.
Input: a raw idea, a task, a saved plan, or a diff.
Output: a verdict in chat, the argument under it; after `pr` in a ship run, also a pushed commit (yellow) and a status and comment on the PR.
Context: after `pr` only, `project.md → Verify` and `→ Hosting & PR`, and the frontmatter's `review_traces`.
**It writes nothing** (folding a verdict into the plan is the ship skill's; the badge note lives outside every repo).
The one exception is a judge entry after `pr` in a ship run: its route writes code, commits and PR traces under the ship skill's authorization.
A standalone `/f10:judge` never does, on a PR included.
Name a bug only where it changes the verdict.
**Proceed** is a full outcome, but never agree because agreeing is cheaper.

1. **Gather the subject.**
   Free text: as written.
   A task id: `f10 task read <id>`, plus `f10 plan read <id>` where a plan exists.
   A PR number, url, or the current branch: diff and description via the host CLI (`gh pr diff` / `gh pr view --comments`, `glab mr diff` / `glab mr view`), or `git diff <base>...HEAD` without a PR, plus the task and plan the branch name points to.
   A sha, a range, or "last N commits": `git show <sha>` / `git diff <range>`, plus the task and plan the messages or branch point to; count commits with `git log --oneline` (`HEAD~N` skips merged branches).
   No argument: what this conversation is about; say in one line which subject you took before judging it.
2. **Restate the problem, then find it in the code** with a few targeted `Read`/`Grep` calls in one message.
   Where the stated problem and the real one differ, build the judgment on that.
3. **Symptom or cause.**
   For a symptom, name where the cause lives and what fixing it takes.
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
   The circle names the verdict; the terminal colors `+` green and `-` red, which shows only go or no-go, so a terminal without highlighting still shows all four:

   ```diff
   + 🟢 PROCEED
   + ▌ <the reason, one line>
   ```
   ```diff
   + 🟡 PROCEED WITH CHANGES
   + ▌ <the reason, one line - what folds in>
   ```
   ```diff
   - 🟠 RETHINK
   - ▌ <the reason, one line - where the real problem lives>
   ```
   ```diff
   - 🔴 STOP
   - ▌ <the reason, one line - what already solves it, or what it costs>
   ```

   Under it, points 2 to 6 in a few short paragraphs grounded in what you read, then the smallest thing that would change the verdict.
   In later replies, the banner returns only when the verdict changes.

**Blind** (`--blind`): for work this session produced.
Gather the subject per point 1, then spawn **one fresh `general-purpose` agent, never `fork`, which inherits the whole conversation**, with the subject verbatim, the checkout root, and the instruction to load the two context calls (`f10-load-conventions.sh`, `f10-load-project.sh judge`) and follow this step.
Relay its verdict unchanged; you may dispute it in one line.

**In a ship pipeline** the verdict routes the run; none is a failure.
The route depends on whether a `pr` entry ran earlier in the same pipeline run, never on whether the branch happens to have a PR.

**Before `pr`** (`plan → judge → implement`, `implement → judge → pr`):

- **proceed** - continue; the verdict is one line in the report.
- **stop** - end **blocked**: `f10-state.sh set ship blocked judge` and, in the same call, `f10-state.sh note "stop: <the reason, one line>" "<the smallest thing that would change the verdict>"`; then the verdict as the report, naming any durable work (commits, an open PR).
- **rethink** / **proceed with changes** - discuss as brainstorm does (`steps/brainstorm.md` points 3 to 6).
  Each change or objection becomes a gap in one `AskUserQuestion` (`conventions/gaps.md`), the judge's answer first, **block the run** a real option.
  On **continue**, the ship skill folds the answers into the plan file (stages and Gaps) and runs the next step; on **block**, end as stop, with this verdict in the note.

**Driven** (`modes/driven.md`): proceed and proceed with changes continue, the ship skill folding the changes in without asking; the verdict and the changes are one line each in the report.
Rethink becomes one ask (objections numbered, the judge's answer first, **block the run** among the options), then `set ship blocked judge`.
Stop ends blocked.

**After `pr`** (`implement → pr → review (local) → judge → pr`): the code is on an open PR, so each verdict ends on the PR.
First read the PR (`gh pr view --json state,headRefOid` / `glab mr view -F json`); one closed or merged since `pr` ran fails the step (`conventions/failure.md`, badge `partial` on `judge`).

- **Green, proceed** - leave the trace (below) and continue; the verdict is one line in the report.
- **Yellow, proceed with changes** - the named changes are the work.
  The ship skill folds them into the plan file as before `pr`; then make and verify them as `steps/implement.md` points 1 and 2 say, commit as `steps/commit.md` point 2 says, and push to the PR's branch, never forcing.
  With `review_traces: on`, the commit body names each change in plain words for a reader of the PR.
  No second review round: a judge plus green verify is enough.
  Red verify fails the step (`conventions/failure.md`, badge `partial` on `judge`), the changes left uncommitted and nothing posted.
  Green: leave the trace on the pushed commit and continue.
- **Orange, rethink** - discuss as brainstorm does (`steps/brainstorm.md` points 3 to 6).
  Once the shape is settled, one `AskUserQuestion`: **refactor as agreed** (first, when the judge believes the agreed refactor is reachable) or **stop**.
  Refactor runs the yellow route with the agreed refactor as its changes, its comment naming the reconsidered design; stop runs the red route.
  If the discussion clears the design, the judge changes its verdict to green, with a new banner.
- **Red, stop** - leave the "do not merge" trace, then end as stop does before `pr`.
  The PR stays open: never close it, convert it to a draft or label it.
  The report names it as durable work.

**Driven** after `pr`: green and yellow continue without asking.
Orange becomes one ask (objections numbered, **refactor as agreed** first, **stop** among the options), then `set ship blocked judge`.
The answer resumes at `judge`: refactor runs yellow, stop runs red.

**The trace** after `pr`, when the frontmatter says `review_traces: on` and the branch has an open PR or MR; off, every route still runs and only the status and comment are skipped.

- **Status** with context `judge` on the PR's head after this step's push (yellow: the pushed commit; green and red: `headRefOid`), the description under 140 characters: `gh api repos/{owner}/{repo}/statuses/<sha> -f state=<success|failure> -f context='judge' -f description='<text>'`, or `glab api projects/:id/statuses/<sha> -X POST -f state=<success|failed> -f name='judge' -f description='<text>'`.
  The descriptions: green `success` with the reason; yellow `success` with `proceed with changes: <n> changes`; red `failure` with `do not merge: <the reason>`, cut to fit.
  A driven orange ask sets `failure` with `rethink: waiting on a decision` before the run ends blocked; its settled color replaces that status.
  Otherwise a sha already carrying a `judge` status with the same state and description is left alone, so a retry never posts twice.
- **Comment** (`gh pr comment` / `glab mr note`), never while an orange ask waits.
  Green: one line, the verdict and its reason.
  Yellow: the verdict and its reason, a line per change, the commit's short sha.
  Red: "Do not merge", the reason, and the smallest thing that would change the verdict.
- Both read as the user's own (`conventions/context.md`).
  A failed post is `partial` (`conventions/failure.md` rule 7); before posting again, check the PR's comments for one already up.
- The report adds a `commit` row when yellow pushed, a `status` row (the sha and its state) and a `comment` row (its url) when posted.

**On failure:** an unknown task id or an unreadable PR (`conventions/failure.md`).
After `pr`, also a PR closed or merged since `pr`, red verify and a failed post (above), and a rejected push: it stops with the commit local, badge `partial` on `judge`, nothing posted; never force-push to clear it.
A **stop** verdict is success.
