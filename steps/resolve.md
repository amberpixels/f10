# Step · resolve - a verdict for each finding, fixes verified

Role: a **senior engineer** triaging review findings.
Input: the latest findings file under `<storage root>/reviews/<TASK-ID>/` with unsettled
findings (shape in `steps/review.md`), plus the task text and the plan.
Output: a verdict under every finding, the fixes in the working tree, verify green. No plan
edit, no new file.
Context: `project.md → Verify`; `project.md → Review` for who answers an `ask`.
Badge: none; inside a ship pipeline the leaf stays `review`.

Settle every file the same way, whoever wrote it, and never guess what a finding meant: settle
what the block says is wrong, where, and its fix.

1. **Find the file.** A task id or PR whose branch is not current: one line naming the worktree
   that holds it (`git worktree list`), or that none does; a normal end. The directory is named
   as `steps/review.md` names it: the task id (explicit, else `f10 task read` with no argument,
   else a PR's head branch via the host CLI), or the branch name with `/` as `-`. Take the
   highest `<round>.md`. Blocks with a `verdict:` are done; none left is a normal end.
2. **Read what was asked**: the task and the plan. The user's stated choice beats the reviewer.
3. **Re-find each finding**, in file order, by file, title and summary (a `reviewed:` sha other
   than HEAD, or `dirty`, makes lines advisory). Code that no longer exists: `skip`, saying so.
4. **One verdict per finding**, neither agreeing blindly nor pushing back by reflex:
   - **`fix`** - fix the problem the finding names, in house style; its proposed fix is a
     suggestion.
   - **`skip`** - with an argued `reason:` in a line or two, for the next round's reviewer. A
     finding that would undo what the task asked for is a skip citing the ask.
   - **`ask`** - the user's call: a real fork, a finding disputing the task, a fix whose scope
     is not yours. Collected for point 7.

   Attachments on the verdict line (`verdict: skip +reply`, `verdict: fix +note`):
   - **`+note`** - a code comment at the finding's spot ("someone could say X here, we do Y
     because"), giving the domain reason, never that a reviewer asked (stealth mode,
     `conventions/context.md`).
   - **`+reply`** - sends `reason:` to the reviewer. With `reviewer: local` it stays in the
     file. A remote reply posts on the PR or MR (`gh pr comment` / `glab mr note`): on the
     `review:` url's thread for an inline review, else a comment quoting the finding's title.
     It is outward-facing: a ship run authorizes it, a standalone `/f10:resolve` asks once
     before the first post. It reads as the user's own: no f10, no round, no findings file.
     **A remote bot's skip replies by default** (`skip +reply` unless the user said `skip`
     alone), since a CI re-review sees only the thread and re-raises a silent skip. A human
     reviewer gets `+reply` only where the verdict names it.
5. **Round two's prior section**: `fixed` and `explanation accepted` need nothing; `still open`,
   `fix introduced a problem` and `explanation disputed` take a verdict, and are an `ask` unless
   the code plainly settles them (there is no third reviewer).
6. **Verify once after the fixes** (`project.md → Verify`, with implement's skip for an unmoved
   tree). Red fails the step: keep the `fix` verdict with the red output on `reason:`, and stop.
7. **The questionnaire.** If the file's `reviewer:` is not `local` and `project.md → Review`
   names who answers there, the `ask` goes to them on the review and the finding waits: a
   normal end, badge `partial` on `review` (`conventions/failure.md` rule 7), a `note` naming
   the finding and `/f10:resolve`. No later pipeline step runs until a later `/f10:resolve`
   rewrites the parked verdict with the answer; the review step never reads a reply.
   Otherwise every `ask` goes to the user in **one `AskUserQuestion`**, a question per finding,
   the reviewer's fix first unless the task contradicts it (then skip first, citing the ask).
   It fires at the end of the entry's **last** resolve: standalone, this one; in a pipeline, a
   resolve that recorded a `fix` holds its asks for round two, which asks for both rounds. Calls
   split by the tool's per-call limit still count as one checkpoint. Apply answers as `fix` or
   `skip`, re-verify after fixes, and rewrite each answered `verdict:` with the user's words on
   `reason:`. A fix chosen here is verified, not re-reviewed.
8. **Write the verdicts**: `verdict:` and any `reason:` under each block, one `verdict:` line per
   block. Never edit the reviewer's text.
9. **Report** per `conventions/report.md`: a `findings` row, then the verdict counts, what the
   fixes changed, and which asks were answered.

**On failure:** red verify after a fix, a findings file naming a task this checkout lacks, or a
malformed file: report and stop, keeping the verdicts written so far.
