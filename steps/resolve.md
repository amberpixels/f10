# Step · resolve - a verdict for each finding, fixes verified

Role: a **senior engineer** triaging review findings.
Input: the latest findings file under `<storage root>/reviews/<TASK-ID>/` that still has unsettled findings (its shape is in `steps/review.md`), plus the task text and the plan.
Output: a verdict under every finding, with verify green. The fixes are committed and pushed when the branch has an open PR or MR, and left in the working tree otherwise.
Resolve never edits the plan. It writes no new file, except the findings file the **CI review** section reads in.
Context: the loader's `verify:` line, and `project.md → Review` for who answers an `ask`.
Badge: none. Inside a ship pipeline the leaf stays `review`.

Every findings file is settled the same way, whoever wrote it, because each block says the same three things: what is wrong, where, and its fix.
A block's **source** is the reviewer that wrote it.
It is the block's `source:` line, or, when the block has none, the header's single `reviewer:` line (`local`, or a remote identity).
A file that both halves of a review wrote (`review:local+ci`) is settled in one pass.
Its rules for remote findings apply only to the blocks whose source is remote.

## CI review

With `ci`, resolve first reads the latest remote review into a findings file, then settles the findings as the next section says, and at the end marks the review settled.
Resolve never waits for a review to finish, because `/f10:review ci` does the waiting.
Inside a ship run, a `review:ci` or `review:local+ci` entry already did points 1 to 4 below.
So after such an entry, resolve runs only point 5, on the review that entry acknowledged.

1. **Latest.**
   Run `f10 review pick --latest --json`.
   If there is no review, or the newest one is not done yet, that is a normal end, and the report names `/f10:review ci`.
   If the remote facts are missing or the host refuses them, the step fails, the same way the review step's remote half fails.
2. **Settled.**
   If the pick says `settled: true`, report "nothing to resolve" and end normally.
   Nothing changes.
3. **Stale.**
   If the pick names a sha other than HEAD, print one warning line naming that sha and the number of commits since it (`git rev-list --count <sha>..HEAD`).
   Then go on, because point 3 of **Settle the findings** already treats the review's line numbers as advisory.
4. **Held.**
   A findings file holds the picked review when three things match.
   Its `reviewer:` names a remote identity, alone or beside `local`.
   Its `review:` is the picked url.
   Its `revision:` is the picked revision.
   A file with no `revision:` line holds no review, because a rewritten sticky comment keeps its sha, so only the revision tells two versions apart.
   If a file holds the review, it is the file that point 1 of **Settle the findings** takes.
   If none does, run points 3 and 4 of the review step's **remote review**: shape the findings file, write its lore entries, and acknowledge the review with the picked id.
   They run under the review step's **Rounds** rules, and a round those rules decline is a normal end here.
   The new file is then the one point 1 of **Settle the findings** takes.
5. **Settle**, after point 10 of **Settle the findings**.
   Run `f10 review settle --id <id> --revision <revision>`, with both values from point 1's pick.
   Do not settle while any verdict is an `ask` still waiting for an answer, whether parked per point 7 or a driven ask still pending.
   Do not settle while fixes exist that point 9 did not push.
   In either case the report says which one held it back.
   If every block in the file that holds the review already has a verdict, skip points 2 to 10 and settle the review the same way.
   If `settle` refuses because the review changed since it was read (a re-review rewrote it), that is a normal end: say so in one line, and note that the new review is unread.
   If the project declares no `settled` mark, set none, and say so in one line, because the findings file is then the only record.

## Settle the findings

1. **Find the file.**
   If the argument is a task id or a PR whose branch is not checked out here, say in one line which worktree holds it (`git worktree list`), or that none does, and end normally.
   Otherwise run `f10 review round [<task-id>]`, which prints the latest findings file on its `latest:` line.
   For a PR, take the task id from the `task:` line that `f10 pr view <PR>` prints.
   Its exit 3 means a new round is declined, which does not stop this resolve of the current round.
   With no `latest:` line, there is nothing to resolve, and the step ends normally.
   A block that already has a `verdict:` line is done.
   If no block is left to settle (a clean file counts), the step ends normally.
   Before it ends, it still runs points 10 and 11 when their conditions hold and the sha that point 10 would mark has no `review (local)` status yet.
2. **Read what was asked**: the task and the plan.
   A block with a `tasks:` line came from the coherence lens, so also read those tasks' plans and `f10 lore show <id>` for each.
   When the user stated a choice, it beats the reviewer.
3. **Re-find each finding** in the code, in file order, by its file, title and summary.
   If the file's `reviewed:` sha is not HEAD, or it says `dirty`, the line numbers are only advisory.
   If the code a finding names no longer exists, its verdict is `skip`, and the reason says so.
4. **Give each finding one verdict.**
   Neither agree blindly nor push back by reflex.
   - **`fix`**: fix the problem the finding names, in the house style.
     The finding's proposed fix is only a suggestion.
   - **`skip`**: give an argued `reason:` in a line or two, because the next round's reviewer reads it.
     A finding whose fix would undo what the task asked for is a `skip`, and the reason cites the ask.
     A block marked `size: own task` is a `skip` with the reason `follow-up: <its title>`.
     The exception is when the code shows the fix is small: then it is a `fix`.
   - **`ask`**: the call is the user's.
     That covers a real fork, a finding that disputes the task, and a fix whose scope is not yours.
     Every `ask` is collected for point 7.

   A verdict line can carry attachments, such as `verdict: skip +reply` or `verdict: fix +note`.
   - **`+note`** adds a code comment at the finding's spot, in the shape "someone could say X here, we do Y because".
     The comment gives the domain reason, and never says that a reviewer asked, because stealth mode keeps the review out of the code (`conventions/context.md`).
   - **`+reply`** sends the `reason:` to the reviewer.
     For a local block, the reply stays in the findings file.
     For a remote block, post it on the PR or MR with `f10 pr comment --reply-to <the block's review: url> --quote <the finding's title>`, the reply on stdin.
     The reply lands in that review's thread when the review has one, and otherwise as a top-level comment headed by the quoted title.
     Posting is outward-facing, and the run's authorization covers it.
     The reply reads as the user's own words, so it names no f10, no round and no findings file.
     If it names another task, that task is a link with its title (`conventions/context.md` → Task references).
     A skip on a remote bot's block gets `+reply` by default, because a CI re-review sees only the thread.
     This applies to the bot's blocks only, and not when the user said `skip` alone.
     A human reviewer gets a reply only where the verdict names `+reply`.
5. **Round two's prior section** lists how each round-one finding ended.
   An entry marked `fixed` or `explanation accepted` needs nothing.
   An entry marked `still open`, `fix introduced a problem` or `explanation disputed` takes a verdict.
   That verdict is an `ask` unless the code plainly settles it, because there is no third reviewer to break the tie.
6. **Verify once after the fixes**, with the commands on the load's `verify:` line.
   Implement's rule applies: if this session already ran them green and the tree has not moved since, skip them.
   Red verify fails the step.
   Keep the `fix` verdict, put the red output on its `reason:` line, and stop.
7. **The questionnaire.**
   When the block is remote and `project.md → Review` names who answers on the review, the `ask` goes to that person on the review, and the finding waits.
   That is a normal end, with the badge `partial` on `review` (`conventions/failure.md` rule 7) and a `note` naming the finding and `/f10:resolve`.
   No later pipeline step runs until a later `/f10:resolve` rewrites the parked verdict with the answer, because the review step never reads a reply.
   Every other `ask` goes to the user in **one `AskUserQuestion`**, with a question per finding.
   The reviewer's fix is the first option, unless the task contradicts it.
   In that case skip is first, and it cites the ask.
   The questionnaire fires at the end of the entry's **last** resolve.
   Run standalone, that is this resolve.
   In a pipeline, a resolve that opens round two (`steps/review.md` → Rounds) holds its asks for round two, and round two asks the questions of both rounds.
   If the tool's per-call limit splits the questions across several calls, they still count as one checkpoint.
   Apply each answer as a `fix` or a `skip`, and re-verify after the fixes.
   Rewrite each answered `verdict:`, with the user's words on its `reason:` line.
   A fix chosen here is verified, but not reviewed again.
8. **Write the verdicts.**
   Under each block, write one `verdict:` line, plus a `reason:` line where the verdict has one.
   Then settle the lore entry of each block that has a `lore:` line:
   - For a `fix`: `f10 lore add finding --change <task> --supersedes <id> --status fixed --resolution <sha, or "working tree"> -m <what the fix changed>`.
   - For a `skip`: the same command with `--status wontfix --resolution <the reason> -m <the reason>`.
   - For an `ask`: nothing, until it is answered.

   The new entry carries over the severity, source and anchors of the entry it supersedes.
   The block's `lore:` line then takes the id that the settle printed, which is the head of the chain.
   This matters because a round two that reopens the finding must supersede that latest entry.
   The `lore:` line is the only reviewer line resolve rewrites.
   A block with no `lore:` line comes from a round written before lore existed, so it writes nothing.
   A failing `lore add` is one line in the report, never a failed step.
   Apart from the `lore:` line, never edit the reviewer's text.
9. **Land the fixes on an open PR or MR.**
   When a `fix` changed code and the branch has an open PR or MR (`f10 pr view`), commit the fixes per `conventions/context.md` → Commits.
   Then push them to that branch, never forcing.
   Pushing is outward-facing, and the run's authorization covers it.
   The trailers come from `f10 trace trailers --step resolve --round <n> --fixed <n> --skipped <n>`.
   `--round` is this file's round.
   `--fixed` and `--skipped` count the verdicts in the file at the time of the commit, and an open `ask` counts in neither.
   In round two the counts cover both files, as point 10's comment does.
   With `traces: on`, the commit message's body lists the findings it fixed by title, then the skipped ones.
   Each skip carries its reason, written for someone reading the PR.
   An answered `ask` is listed under the verdict it became.
   With no open PR or MR, the fixes stay in the working tree, for the pipeline's next step (`pr`, `push`, `commit`) or for the user.
10. **Leave the trace** on the PR or MR when all four conditions hold:
    - the frontmatter says `traces: on`;
    - the branch has an open PR or MR;
    - the file holds local blocks, meaning its `reviewer:` is `local` or lists it;
    - this is the entry's last resolve, by point 7's rule: in a pipeline, a resolve that opens round two holds its trace for round two, whose trace covers both rounds.

    The trace follows point 9's choice: if the fixes were not pushed, nothing is posted.
    In a file both halves wrote, the status's counts and the comment cover only the local blocks.
    The remote blocks are answered on their own thread, by `+reply` and by the settled mark.
    In round two, the counts and the comment cover both files, each finding at its last verdict, with round one's skip reasons included.
    - **Status.**
      Set it with `f10 pr status --name 'review (local)' --state <success|failure> --sha <sha> -m '<counts>'`.
      The sha is the fix commit when point 9 pushed one directly on top of a clean `reviewed:` sha.
      Otherwise it is the `reviewed:` sha, when that sha is clean.
      `f10 pr status` refuses a sha that is neither the PR's head nor a push on top of it.
      A refused or unclean sha gets no status, and the report says why.
      The state is `failure` while any finding's last verdict is an `ask` (parked per point 7, or a driven ask still pending), and `success` otherwise.
      The description holds counts only, in under 140 characters: `3 found, 2 fixed, 1 skipped`, with `1 waiting` added when a finding waits, or `nothing found` for a clean round.
    - **Comment.**
      Post it with `f10 pr comment`, the text on stdin, when the entry found anything and no `ask` is waiting.
      It has a line per finding, by title, with its verdict in plain words, each skip's reason, and the fix commit's short sha when one was pushed.
      A clean entry gets the status alone, with no comment.
      A run that stops on a waiting `ask` sets the failure status and posts no comment.
      The resolve that later settles the ask sets the status again and posts the comment.
    - The status and the comment both read as the user's own words.
      They name findings by title, never by id, and they mention no round and no findings file (`conventions/context.md`).
      A post that fails after a fix was pushed makes the step `partial` (`conventions/failure.md` rule 7).
      Retrying it is safe, because `f10 pr comment` and `f10 pr status` never post the same thing twice.
11. **Rewrite the state block** (`docs/traces.md`) on every resolve, once the verdicts are written and any fix is pushed.
    Run `f10 trace state --step resolve --round <n> --open <n> --asks <n>`.
    `--open` counts the findings whose last verdict is missing or an `ask`, across both files in round two.
    `--asks` counts the questions waiting on a person: findings parked for a remote answer (point 7), and a driven ask raised in this run.
    An ask held for round two counts as open, but not yet as asked.
    In a driven run, this call goes right before `f10-state.sh set ... blocked`.
    The resolve that resumes on the answer passes both counts back down.
    With `traces: off`, or without an open PR or MR, nothing is written.
    A non-zero exit makes the step `partial` (`conventions/failure.md` rule 7), never failed.
12. **Report** per `conventions/report.md`.
    The facts block has a `findings` row, and a `commit` row when point 9 committed.
    It has a `status` row (the sha and its state) and a `comment` row (its url) when point 10 posted them.
    It has a `settled` row, holding what `settle` printed, when the **CI review** section marked the review.
    Beneath the block, give the verdict counts, what the fixes changed, which asks were answered, and each `follow-up:` title, for the user to capture.

**On failure:** red verify after a fix, a findings file that names a task this checkout lacks, or a malformed file.
Report and stop, keeping the verdicts written so far.
A rejected push stops with the commits local.
Never force-push to clear it (`conventions/failure.md`).
