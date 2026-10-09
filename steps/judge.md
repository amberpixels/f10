# Step · judge - a verdict on an idea, task, plan or PR

Role: a **senior architect, thinking as the CTO**, with the burden of proof on the idea.
Input: a raw idea, a task, a saved plan, or a diff.
Output: a verdict in chat, the argument under it. **Nothing on disk, no tracker comment, no
code.** Name a bug only where it changes the verdict. **Proceed** is a full outcome, but never
agree because agreeing is cheaper.

1. **Gather the subject.** Free text: as written. A task id: the task via the fetch adapter,
   plus `<storage root>/plans/<TASK-ID>.md` if it exists. A PR number, url, or the current
   branch: diff and description via the host CLI (`gh pr diff` / `gh pr view --comments`,
   `glab mr diff` / `glab mr view`), or `git diff <base>...HEAD` without a PR, plus the task and
   plan the branch name points to. A sha, a range, or "last N commits": `git show <sha>` /
   `git diff <range>`, plus the task and plan the messages or branch point to; count commits
   with `git log --oneline` (`HEAD~N` skips merged branches). No argument: what this
   conversation is about, named back in one line first. `f10 task read` and `f10 plan read`
   do these lookups where the binary is installed.
2. **Restate the problem, then find it in the code** with a few targeted `Read`/`Grep` calls in
   one message. Where the stated problem and the real one differ, build the judgment on that.
3. **Symptom or cause.** For a symptom, name where the cause lives and what fixing it takes.
4. **What already exists**: in the codebase, a dependency, or an open tracker task.
5. **The longer-lived shape**: the one design you would build instead, if any, grounded in what
   you read, its cost now against reaching it later. A quick hack is a hack only if a better
   shape is reachable now.
6. **For code: keep, reshape, or discard**, and which parts.
7. **Verdict**, one of four:
   - **proceed** - the problem is real, the shape is right, build it as it stands.
   - **proceed with changes** - the shape is right; the named changes fold in.
   - **rethink** - the problem is real, this shape is wrong. For existing code, the refactor
     verdict.
   - **stop** - do not build it: not real, already solved, or not worth the cost.

   Open the reply with its **banner**, a `diff`-tagged block (the terminal colors `+` green, `-`
   red); line two is the reason, under 80 characters:

   ```diff
   + ✔ PROCEED
   + ▌ <the reason, one line>
   ```
   ```diff
   + ✚ PROCEED WITH CHANGES
   + ▌ <the reason, one line - what folds in>
   ```
   ```diff
   - ↻ RETHINK
   - ▌ <the reason, one line - where the real problem lives>
   ```
   ```diff
   - ✘ STOP
   - ▌ <the reason, one line - what already solves it, or what it costs>
   ```

   Under it, points 2 to 6 in a few short paragraphs grounded in what you read, then the
   smallest thing that would change the verdict. Afterwards, talk normally as long as the user
   argues; the banner returns only when the verdict changes.

**Blind** (`--blind`): for work this session produced. Gather the subject per point 1, then
spawn **one fresh `general-purpose` agent, never `fork`, which inherits the whole
conversation**, with the subject verbatim, the checkout root, and the instruction to load the
two context calls (`conventions.sh`, `bundle.sh judge`) and follow this step; project facts
reach it through the bundle, never through the author. Relay its verdict
unchanged; you may dispute it in one line.

**In a ship pipeline** (`plan → judge → implement`, `implement → judge → pr`), the verdict routes
the run; none is a failure:

- **proceed** - continue; the verdict is one line in the report.
- **stop** - end **blocked**: `f10-state.sh set ship blocked judge` and, in the same call,
  `f10-state.sh note "stop: <the reason, one line>" "<the smallest thing that would change the
  verdict>"`; then the verdict as the report, naming any durable work (commits, an open PR).
  Never continue on your own.
- **rethink** / **proceed with changes** - discuss as brainstorm does (`steps/brainstorm.md`
  points 3 to 6). Each change or objection becomes a gap in one `AskUserQuestion`
  (`conventions/gaps.md`), the judge's answer first, **block the run** a real option. On
  **continue**, the ship skill folds the answers into the plan file (stages and Gaps) and runs
  the next step; on **block**, end as stop, with this verdict in the note.

**Driven** (`modes/driven.md`): proceed and proceed with changes continue, the ship skill folding
the changes in without asking; the verdict and the changes are one line each in the report.
Rethink becomes one ask (objections numbered, the judge's answer
first, **block the run** among the options), then `set ship blocked judge`. Stop ends blocked.

**It writes nothing**: no plan edit (folding is the ship skill's), no tracker comment, no code,
no scratch file. The badge note lives in the session's state file, outside every repo.

**On failure:** an unknown task id or an unreadable PR: never judge a subject you could not
fetch. A **stop** verdict is success.
