# Step · resolve - a verdict for each finding, fixes verified

Role: a **senior engineer** triaging review findings.
Input: the latest findings file under `<storage root>/reviews/<TASK-ID>/` with unsettled findings (shape in `steps/review.md`), plus the task text and the plan.
Output: a verdict under every finding, verify green, and the fixes committed and pushed when the branch has an open PR or MR, else left in the working tree.
No plan edit, no new file.
Context: `project.md → Verify`; `project.md → Review` for who answers an `ask`.
Badge: none; inside a ship pipeline the leaf stays `review`.

Settle every file the same way, whoever wrote it: what the block says is wrong, where, and its fix.

1. **Find the file.**
   A task id or PR whose branch is not current: one line naming the worktree that holds it (`git worktree list`), or that none does; a normal end.
   The directory is named as `steps/review.md` names it: the task id (explicit, else `f10 task read` with no argument, else a PR's head branch via the host CLI), or the branch name with `/` as `-`.
   Take the highest `<round>.md`.
   Blocks with a `verdict:` are done; none left (a clean file included) is a normal end, after point 10 when its conditions hold and the sha it would mark carries no `review (local)` status yet.
2. **Read what was asked**: the task and the plan.
   The user's stated choice beats the reviewer.
3. **Re-find each finding**, in file order, by file, title and summary (a `reviewed:` sha other than HEAD, or `dirty`, makes lines advisory).
   Code that no longer exists: `skip`, saying so.
4. **One verdict per finding**, neither agreeing blindly nor pushing back by reflex:
   - **`fix`** - fix the problem the finding names, in house style; its proposed fix is a suggestion.
   - **`skip`** - with an argued `reason:` in a line or two, for the next round's reviewer.
     A finding that would undo what the task asked for is a skip citing the ask.
   - **`ask`** - the user's call: a real fork, a finding disputing the task, a fix whose scope is not yours.
     Collected for point 7.

   Attachments on the verdict line (`verdict: skip +reply`, `verdict: fix +note`):
   - **`+note`** - a code comment at the finding's spot ("someone could say X here, we do Y because"), giving the domain reason, never that a reviewer asked (stealth mode, `conventions/context.md`).
   - **`+reply`** - sends `reason:` to the reviewer.
     With `reviewer: local` it stays in the file.
     A remote reply posts on the PR or MR (`gh pr comment` / `glab mr note`): on the `review:` url's thread for an inline review, else a comment quoting the finding's title.
     It is outward-facing, under the run's authorization.
     It reads as the user's own: no f10, no round, no findings file.
     Another task it names is a link with its title (`conventions/context.md` → Task references).
     **A remote bot's skip replies by default** (`skip +reply` unless the user said `skip` alone), since a CI re-review sees only the thread and re-raises a silent skip.
     A human reviewer gets `+reply` only where the verdict names it.
5. **Round two's prior section**: `fixed` and `explanation accepted` need nothing; `still open`, `fix introduced a problem` and `explanation disputed` take a verdict, and are an `ask` unless the code plainly settles them (there is no third reviewer).
6. **Verify once after the fixes** (`project.md → Verify`, with implement's skip for an unmoved tree).
   Red fails the step: keep the `fix` verdict with the red output on `reason:`, and stop.
7. **The questionnaire.**
   If the file's `reviewer:` is not `local` and `project.md → Review` names who answers there, the `ask` goes to them on the review and the finding waits: a normal end, badge `partial` on `review` (`conventions/failure.md` rule 7), a `note` naming the finding and `/f10:resolve`.
   No later pipeline step runs until a later `/f10:resolve` rewrites the parked verdict with the answer; the review step never reads a reply.
   Otherwise every `ask` goes to the user in **one `AskUserQuestion`**, a question per finding, the reviewer's fix first unless the task contradicts it (then skip first, citing the ask).
   It fires at the end of the entry's **last** resolve: standalone, this one; in a pipeline, a resolve that recorded a `fix` holds its asks for round two, which asks for both rounds.
   Calls split by the tool's per-call limit still count as one checkpoint.
   Apply answers as `fix` or `skip`, re-verify after fixes, and rewrite each answered `verdict:` with the user's words on `reason:`.
   A fix chosen here is verified, not re-reviewed.
8. **Write the verdicts**: `verdict:` and any `reason:` under each block, one `verdict:` line per block.
   Never edit the reviewer's text.
9. **Land the fixes on an open PR or MR.**
   When a `fix` changed code and the branch has an open PR or MR (`gh pr view --json state` / `glab mr view`), commit the fixes as `steps/commit.md` point 2 says and push them to that branch, never forcing.
   The trailers are `f10 trace trailers --step resolve --round <n> --fixed <n> --skipped <n>`: `--round` is this file's round, `--fixed` and `--skipped` count the verdicts in it at the time of the commit, an open `ask` in neither; in round two the counts cover both files, as point 10's comment does.
   Outward-facing, under the run's authorization.
   With `traces: on`, the message's body lists the findings it fixed by title, then the skipped ones, each with its reason written for a reader of the PR; an answered `ask` sits under the verdict it became.
   With no open PR or MR, the fixes stay in the working tree for the pipeline's next step (`pr`, `push`, `commit`) or the user.
10. **Leave the trace** when the frontmatter says `traces: on`, the branch has an open PR or MR, the file's `reviewer:` is `local`, and this is the entry's last resolve (point 7's rule: a pipeline resolve that recorded a `fix` holds its trace for round two, which covers both rounds).
    The trace follows point 9's choice: fixes not pushed, nothing posted.
    In round two the counts and the comment cover both files, each finding at its last verdict, round one's skip reasons included.
    - **Status** on the fix commit when point 9 pushed one directly on top of a clean `reviewed:` sha, else on the `reviewed:` sha when it is clean and is the PR's head (`gh pr view --json headRefOid` / `glab mr view`); any other sha gets no status, and the report says why: `gh api repos/{owner}/{repo}/statuses/<sha> -f state=<success|failure> -f context='review (local)' -f description='<counts>'`, or `glab api projects/:id/statuses/<sha> -X POST -f state=<success|failed> -f name='review (local)' -f description='<counts>'`.
      Failure while a finding's last verdict is `ask` (parked per point 7, or a driven ask pending), else success.
      The description is counts only, under 140 characters: `3 found, 2 fixed, 1 skipped`, `1 waiting` added when one is, `nothing found` for a clean round.
    - **Comment** (`gh pr comment` / `glab mr note`) when the entry found anything, and no `ask` is waiting: a line per finding by title with its verdict in plain words, each skip's reason, and the fix commit's short sha when one was pushed.
      A clean entry gets the status alone.
      A run stopping on a waiting `ask` sets the failure status and posts no comment; the resolve that settles the ask sets the status again and posts the comment.
    - Both read as the user's own: findings by title, never an id, a round or the findings file (`conventions/context.md`).
      A failed post after a pushed fix is `partial` (`conventions/failure.md` rule 7); before posting again, check the PR's comments for one already up.
11. **Report** per `conventions/report.md`: a `findings` row, a `commit` row when point 9 committed, a `status` row (the sha and its state) and a `comment` row (its url) when point 10 posted, then the verdict counts, what the fixes changed, and which asks were answered.

**On failure:** red verify after a fix, a findings file naming a task this checkout lacks, or a malformed file: report and stop, keeping the verdicts written so far.
A rejected push stops with the commits local; never force-push to clear it (`conventions/failure.md`).
