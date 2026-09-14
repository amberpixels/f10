# Step · judge - idea, task, plan or PR → a verdict

Role: a **senior architect, thinking as the CTO** - judging the work from above, with the burden
of proof on the idea. Adopt the roles from `project.md → Roles` on top.
Input: a raw idea, a task, a saved plan, or a diff - whichever the skill's routing gathered.
Output: a verdict in chat, one word first, the argument under it. **Nothing on disk, no tracker
comment, no code.** Bugs are the review step's job; they are named here only where they change
the verdict.
Badge (`conventions/report.md`): none standalone - it creates nothing to track. Inside a ship
pipeline, `/f10:ship` reports the step as it does every other.

The step exists for doubt: the moment before a shape hardens, or after it hardened and something
feels off. It is not a denial machine - **proceed** is a full, legitimate outcome - but it does
not agree because agreeing is cheaper.

1. **Gather the artifact.** Free text is the artifact as written. A task id: the task via the
   project's fetch adapter, plus `<storage root>/plans/<TASK-ID>.md` when one exists. A PR
   number, url, or the current branch: the diff and the description via the host CLI
   (`gh pr diff` / `gh pr view --comments`, `glab mr diff` / `glab mr view`), or
   `git diff <base>...HEAD` for a branch with no PR yet, plus the task and plan the branch
   name resolves to. A sha, a range, or "last N commits": `git show <sha>` / `git diff <range>`
   for the diff and the messages, plus the task and plan those messages or their branch resolve
   to - count commits from `git log --oneline`, since `HEAD~N` follows first parents and steps
   over a merged branch. No argument: the thing this conversation is about - name it back in one
   line before judging it, so the user can correct the target. Where the `f10` binary is
   installed, `f10 task read` and `f10 plan read` are the same lookups in one command each.
2. **Restate the problem, then find it in the code.** The problem as the artifact states it,
   in one line, and the problem the code actually shows, grounded in a few targeted
   `Read`/`Grep` calls in one message. Where the two differ, that difference is the spine of
   the judgment - a change that solves the stated problem and not the real one is the case
   this step exists to catch.
3. **Symptom or cause.** Say which one the change addresses. Where it treats a symptom, name
   where the cause lives and what touching it would take.
4. **What already exists.** In the codebase, in a dependency, in the tracker as an open task.
   The cheapest verdict is "this is already here", and it is only available to a judge who
   looked.
5. **The longer-lived shape.** The design you would build instead, if any - one, grounded in
   what you read, not a catalogue - with its cost now set against the cost of getting there
   later from the proposed shape. A quick hack is a hack only if a better shape is reachable
   now; say whether it is.
6. **For an implementation: keep, reshape, or discard.** When the artifact is code, say
   whether what is written survives the verdict, and which parts.
7. **Verdict.** The first line of the reply, one of exactly four:
   - **proceed** - the problem is real, the shape is right, build it as it stands.
   - **proceed with changes** - the shape is right; the named changes fold in before or during
     the build.
   - **rethink** - the problem is real; this shape is not the one to build. For code that
     already exists this is the refactor verdict - there is no separate word for it.
   - **stop** - do not build it: the problem is not real, is already solved, or the cost
     outweighs it.

   The verdict opens the reply as a **banner**, never as a bare word: a terminal is read at a
   glance, and one bold word above a wall of paragraphs is not a glance. Print the block for
   the verdict, fenced and tagged `diff` so the terminal colors it - `+` lines green, `-` lines
   red, the same trick `conventions/report.md` plays with `yaml`. Line one is fixed; line two
   is this judgment's reason in one line, under 80 characters:

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

   Under the banner, points 2 to 6 in a few short paragraphs, each grounded in something read,
   then the smallest thing that would change the verdict. In the discussion that follows, the
   banner returns only when the verdict changes; a reply that holds it leads with the word in
   bold.

**Two modes.** In-context is the default: the judge has the conversation, and the chat
rationale is often where the real reason lives. **Blind** - `--blind` in the argument - is for
judging something this session produced, where a judge sharing the author's context carries the
author's sunk cost. In blind mode the main agent still gathers the artifact per point 1, then
spawns **one fresh `general-purpose` agent - never `fork`, which inherits the whole
conversation** - whose prompt carries the artifact text verbatim, the checkout root, and the
instruction to load the two context calls (`conventions.sh`, `resolve.sh judge`) and follow this
step. Project facts and guardrails reach it through the resolver, not through the author. The
main agent relays the returned verdict as given - it may dispute it in one line, never rewrite
it.

**In a ship pipeline.** A project may name `judge` in its pipeline (`plan → judge → implement`,
`implement → judge → pr`). There the verdict routes the run, and none of the four is a failure
in the `conventions/failure.md` sense - nothing broke, so no FAILED block and no `failed` glyph:

- **proceed** - the pipeline continues to the next step. The verdict is one line in the run's
  report.
- **stop** - the run ends **blocked**: `f10-state.sh set ship blocked judge`, then the verdict as
  the report, in its normal shape. Anything durable the run had already produced - commits, an
  open PR - is named there, but the badge still reads blocked, not partial: the work stopped by
  decision, not by breakage. The pipeline never continues on its own.
- **rethink** and **proceed with changes** - the run pauses in a discussion, in brainstorm's
  shape (`steps/brainstorm.md` points 3 to 6: a position, then converge). Each named change or
  objection becomes a gap in the questionnaire shape of `conventions/gaps.md` - one
  `AskUserQuestion`, the judge's preferred answer first, and **block the run** on the table as
  a real option, not a courtesy. The discussion ends in one of two decisions, both the user's:
  **continue** - the ship skill folds the answers into the plan file, stages and Gaps section
  alike, exactly as it already does for open gaps before implementing, then runs the next step;
  or **block** - as stop above. The judge still writes nothing; the plan edit is the ship
  skill's.

**It writes nothing.** No plan edits, no tracker comment, no code, no scratch file. Stay in the
conversation as long as the user wants to argue - that argument is the point - but every reply
leads with the verdict as it now stands: the banner when it changed, the bold word when it held.

**On failure:** a task id that does not resolve, or a PR the host CLI cannot read, is a failure
per `conventions/failure.md` - never judge an artifact you could not fetch. A **stop** verdict is
the step succeeding; report it plainly.
