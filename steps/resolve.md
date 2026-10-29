# Step · resolve - a verdict for each finding, fixes verified

Role: a **senior engineer** triaging review findings.
Input: the latest findings file under `<storage root>/reviews/<TASK-ID>/` with unsettled findings (shape in `steps/review.md`), plus the task text and the plan.
Output: a verdict under every finding, verify green, and the fixes committed and pushed when the branch has an open PR or MR, else left in the working tree.
No plan edit, and no new file but the one the **CI review** section reads in.
Context: `project.md → Verify`; `project.md → Review` for who answers an `ask`.
Badge: none; inside a ship pipeline the leaf stays `review`.

Settle every file the same way, whoever wrote it: what the block says is wrong, where, and its fix.
A block's **source** is its `source:` line, else the header's one `reviewer:` (`local`, or remote); a file both halves wrote (`review:local+ci`) is settled in one pass, its remote rules applying to the remote blocks only.

## CI review

With `ci`, resolve reads the latest remote review in before point 1 and marks it settled after point 10.
It never waits for one: `/f10:review ci` does.
Inside a ship run, after a `review:ci` or `review:local+ci` entry, point 5 alone runs, on the review that entry acked: points 1 to 4 are the entry's.

1. **Latest.**
   `f10 review pick --latest --json`.
   No review, or the newest not done: a normal end naming `/f10:review ci`.
   Remote facts missing or refused: fail, as the review step's remote half does.
2. **Settled** (`settled: true`): "nothing to resolve", a normal end; nothing changes.
3. **Stale**: a picked sha other than HEAD prints one warning line naming the sha and the commits since (`git rev-list --count <sha>..HEAD`), then the run goes on; point 3 already reads its lines as advisory.
4. **Held?**
   A findings file holds the review when its `reviewer:` names a remote identity (alone, or beside `local`), its `review:` is the picked url and its `revision:` the picked revision; a file with no `revision:` line holds none, since a rewritten sticky comment keeps its sha.
   One that does is the file point 1 takes.
   None does: run the review step's **remote review** points 3 and 4 (shape the file, write its lore entries, ack with the picked id), under its **Rounds** rules, whose declines are normal ends here; the new file is the one point 1 takes.
5. **Settle**, after point 10: `f10 review settle --id <id> --revision <revision>`, both from point 1's pick.
   Withheld while a verdict is a waiting `ask` (parked per point 7, or a driven ask pending), or while fixes exist that point 9 did not push; the report says which.
   A held file whose every block already has a verdict skips points 2-10 and is settled the same way.
   `settle` refusing a review that changed since it was read (a re-review rewrote it) is a normal end: one line, the new review is unread.
   No `settled` declared: no mark, and one line saying so; the findings file is the record.

1. **Find the file.**
   A task id or PR whose branch is not current: one line naming the worktree that holds it (`git worktree list`), or that none does; a normal end.
   `f10 review round [<task-id>]` (a PR's task id from its head branch via the host CLI) prints the file as `latest:`; its exit 3 declines a new round, not this one.
   No `latest:` line: nothing to resolve, a normal end.
   Blocks with a `verdict:` are done; none left (a clean file included) is a normal end, after points 10 and 11 when their conditions hold and the sha point 10 would mark carries no `review (local)` status yet.
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
     On a local block it stays in the file.
     A remote reply posts on the PR or MR (`gh pr comment` / `glab mr note`): on the `review:` url's thread for an inline review, else a comment quoting the finding's title.
     Outward-facing, under the run's authorization, and read as the user's own: no f10, no round, no findings file.
     Another task it names is a link with its title (`conventions/context.md` → Task references).
     **A remote bot's skip replies by default**, its blocks only (`skip +reply` unless the user said `skip` alone), since a CI re-review sees only the thread.
     A human reviewer gets `+reply` only where the verdict names it.
5. **Round two's prior section**: `fixed` and `explanation accepted` need nothing; `still open`, `fix introduced a problem` and `explanation disputed` take a verdict, and are an `ask` unless the code plainly settles them (there is no third reviewer).
6. **Verify once after the fixes** (`project.md → Verify`, with implement's skip for an unmoved tree).
   Red fails the step: keep the `fix` verdict with the red output on `reason:`, and stop.
7. **The questionnaire.**
   If the block is remote and `project.md → Review` names who answers there, the `ask` goes to them on the review and the finding waits: a normal end, badge `partial` on `review` (`conventions/failure.md` rule 7), a `note` naming the finding and `/f10:resolve`.
   No later pipeline step runs until a later `/f10:resolve` rewrites the parked verdict with the answer; the review step never reads a reply.
   Otherwise every `ask` goes to the user in **one `AskUserQuestion`**, a question per finding, the reviewer's fix first unless the task contradicts it (then skip first, citing the ask).
   It fires at the end of the entry's **last** resolve: standalone, this one; in a pipeline, a resolve that opens round two (`steps/review.md` → Rounds) holds its asks for it, and round two asks for both rounds.
   Calls split by the tool's per-call limit still count as one checkpoint.
   Apply answers as `fix` or `skip`, re-verify after fixes, and rewrite each answered `verdict:` with the user's words on `reason:`.
   A fix chosen here is verified, not re-reviewed.
8. **Write the verdicts**: `verdict:` and any `reason:` under each block, one `verdict:` line per block.
   Then, for each block with a `lore:` line, settle its entry: `f10 lore add finding --change <task> --supersedes <id> --status fixed --resolution <sha, or "working tree"> -m <what the fix changed>` for a `fix`, `--status wontfix --resolution <the reason> -m <the reason>` for a `skip`; severity, source and anchors carry over from the entry it supersedes, and an `ask` writes nothing until answered.
   The block's `lore:` line then takes the id the settle printed, the chain's head, so a round two that reopens the finding supersedes the right entry; it is the one reviewer line resolve rewrites.
   A block with no `lore:` line (a round from before lore) writes nothing; a failing `lore add` is one line in the report, never a failed step.
   Never edit the reviewer's text, the `lore:` line above aside.
9. **Land the fixes on an open PR or MR.**
   When a `fix` changed code and the branch has an open PR or MR (`gh pr view --json state` / `glab mr view`), commit the fixes per `conventions/context.md` → Commits and push them to that branch, never forcing.
   The trailers are `f10 trace trailers --step resolve --round <n> --fixed <n> --skipped <n>`: `--round` is this file's round, `--fixed` and `--skipped` count the verdicts in it at the time of the commit, an open `ask` in neither; in round two the counts cover both files, as point 10's comment does.
   Outward-facing, under the run's authorization.
   With `traces: on`, the message's body lists the findings it fixed by title, then the skipped ones, each with its reason written for a reader of the PR; an answered `ask` sits under the verdict it became.
   With no open PR or MR, the fixes stay in the working tree for the pipeline's next step (`pr`, `push`, `commit`) or the user.
10. **Leave the trace** when the frontmatter says `traces: on`, the branch has an open PR or MR, the file holds local blocks (its `reviewer:` is or lists `local`), and this is the entry's last resolve (point 7's rule: a pipeline resolve that opens round two holds its trace for it, which covers both rounds).
    The trace follows point 9's choice: fixes not pushed, nothing posted.
    In a file both halves wrote, the status's counts and the comment cover the local blocks; the remote ones are answered on their own thread by `+reply` and the settled mark.
    In round two the counts and the comment cover both files, each finding at its last verdict, round one's skip reasons included.
    - **Status** on the fix commit when point 9 pushed one directly on top of a clean `reviewed:` sha, else on the `reviewed:` sha when it is clean and is the PR's head (`gh pr view --json headRefOid` / `glab mr view`); any other sha gets no status, and the report says why: `gh api repos/{owner}/{repo}/statuses/<sha> -f state=<success|failure> -f context='review (local)' -f description='<counts>'`, or `glab api projects/:id/statuses/<sha> -X POST -f state=<success|failed> -f name='review (local)' -f description='<counts>'`.
      Failure while a finding's last verdict is `ask` (parked per point 7, or a driven ask pending), else success.
      The description is counts only, under 140 characters: `3 found, 2 fixed, 1 skipped`, `1 waiting` added when one is, `nothing found` for a clean round.
    - **Comment** (`gh pr comment` / `glab mr note`) when the entry found anything, and no `ask` is waiting: a line per finding by title with its verdict in plain words, each skip's reason, and the fix commit's short sha when one was pushed.
      A clean entry gets the status alone.
      A run stopping on a waiting `ask` sets the failure status and posts no comment; the resolve that settles the ask sets the status again and posts the comment.
    - Both read as the user's own: findings by title, never an id, a round or the findings file (`conventions/context.md`).
      A failed post after a pushed fix is `partial` (`conventions/failure.md` rule 7); before posting again, check the PR's comments for one already up.
11. **Rewrite the state block** (`docs/traces.md`), every resolve, with the verdicts written and any fix pushed: `f10 trace state --step resolve --round <n> --open <n> --asks <n>`.
    `--open` counts the findings whose last verdict is missing or `ask`, both files in round two.
    `--asks` counts the questions waiting on a person: findings parked for a remote answer (point 7) and a driven ask raised this run; an ask held for round two is open, not yet asked.
    In a driven run the call goes right before `f10-state.sh set ... blocked`, and the resolve that resumes on the answer passes both counts back down.
    Nothing with `traces: off` or without an open PR or MR; a non-zero exit is `partial` (`conventions/failure.md` rule 7), never a failed step.
12. **Report** per `conventions/report.md`: a `findings` row, a `commit` row when point 9 committed, a `status` row (the sha and its state) and a `comment` row (its url) when point 10 posted, a `settled` row (what `settle` printed) when the **CI review** section marked it, then the verdict counts, what the fixes changed, and which asks were answered.

**On failure:** red verify after a fix, a findings file naming a task this checkout lacks, or a malformed file: report and stop, keeping the verdicts written so far.
A rejected push stops with the commits local; never force-push to clear it (`conventions/failure.md`).
