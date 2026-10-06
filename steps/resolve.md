# Step · resolve - findings → a verdict each, fixes verified

Role: a **senior engineer** triaging review findings on a change - adopt the roles from
`project.md → Roles`, including any conditional ones the plan recorded.
Input: the latest findings file under `<storage root>/reviews/<TASK-ID>/` that still holds
findings without a verdict (shape in `steps/review.md`), plus the task text and the plan for
what was asked.
Output: a verdict under every finding, the fixes in the working tree, verify green. Nothing
else on disk: no plan edit, no new file.
Context: per `conventions/context.md` - verify commands from `project.md → Verify`, and who
answers an `ask` from `project.md → Review` (+ same-named overlay).
Badge (`conventions/report.md`): none standalone. Inside a ship pipeline the leaf stays
`review`, as the pipeline names the entry.

This is the settling half. It never knows which reviewer wrote the file, and never guesses what
a finding meant: the block says what is wrong, where, and the fix, and that is what is settled.

1. **Find the file.** The change settled is the one checked out here - the fixes land in this
   working tree - so a task id or a PR whose branch is not the current one ends in one line
   naming the worktree that holds it (`git worktree list`), or that none does, before any
   lookup. The directory is named exactly as `steps/review.md` names it: the task id
   - an explicit one, else `f10 task read` with no argument reads it from the branch, else a PR
   number's head branch via the host CLI and the same lookup - and where no task id comes
   back, the branch name with `/` as `-`. The file is the highest `<round>.md` present. Every
   block there already carrying a `verdict:` is done; a file with none left to settle is a
   normal end, said in one line.
2. **Read what was asked.** The task text and the plan, so a finding that would undo something
   the task asked for is recognised. The user's stated choice beats the reviewer.
3. **Re-find each finding in the code**, in file order, by file, title and summary. A `reviewed:`
   sha that is not HEAD, or a `dirty` mark, means line numbers are advisory. A finding whose
   code no longer exists is settled as `skip` with that as the reason.
4. **One verdict per finding**, like a senior engineer who neither blindly agrees nor reflexively
   pushes back:
   - **`fix`** - make the change, in the project's house style, the way the implement step
     would. The finding's concrete fix is a proposal, not an order: fix the problem it names.
   - **`skip`** - with the reason on the `reason:` line. A finding that would undo something
     the task asked for is a skip citing the ask. The reason is written for the reviewer who
     will read it next round, so it argues, in a line or two.
   - **`ask`** - the call is the user's: a real fork, a finding that disputes the task itself,
     or a fix whose scope is not yours to decide. Collected, not answered now (point 6).

   Two attachments, written on the verdict line - `verdict: skip +reply`, `verdict: fix +note`:
   - **`+note`** leaves a comment in the code at the spot the finding names: "someone could say
     X here, we do Y because". It states the domain reason, in the code's own words, and never
     that a reviewer asked - stealth mode (`conventions/context.md`) binds it like any comment.
   - **`+reply`** sends the `reason:` line to the reviewer. The file's `reviewer:` line says
     whether there is one: `local` means nowhere to send it, so the line stays in the file,
     which is where the next local round reads it. A remote reviewer's reply is a post on the
     PR or MR (`gh pr comment` / `glab mr note`), which is outward-facing: it posts under the
     authorization a ship run carries, and a standalone `/f10:resolve` asks once before the
     first post. Its text reads as the user's own, bound by stealth mode like a commit message:
     no f10, no round, no findings file named. Where the file carries a `review:` url, the reply
     goes on that review - a reply in its thread for an inline review, a PR/MR comment quoting
     the finding's title otherwise. **A CI round's skip replies by default**: a re-review replaces
     its sticky comment and knows round one only through the thread its prompt reads, so a
     `skip` without `+reply` is re-raised next round. In a round whose `reviewer:` is a remote
     bot, write `skip +reply` unless the user said `skip` alone; a human reviewer's round takes
     `+reply` only where the verdict names it.
5. **Round two's prior section.** Each prior block in a round-two file is settled like a
   finding: `fixed` and `explanation accepted` need nothing; `still open`, `fix introduced a
   problem` and `explanation disputed` take a verdict. After round two these three cannot go to
   a third reviewer, so each is an `ask` unless the code plainly settles it.
6. **Verify once after the fixes**, with the project's Verify commands and the implement step's
   one skip rule (a tree that did not move since this session's last green). Red verify is this
   step failing: the finding keeps its `fix` verdict with the red output on its `reason:` line,
   so nothing is lost, and the run stops here.
7. **The questionnaire.** An `ask` has an addressee only when the file's `reviewer:` is not
   `local` and `project.md → Review` names who answers there; then the question goes to them on
   the review, and the finding waits: the entry ends there as a normal end, the badge
   `partial` on `review` with a `note` naming the finding (`conventions/failure.md` rule 7, the
   PR exists) naming `/f10:resolve` as the next step, and no later pipeline step runs until the
   user brings the answer back through it. That run rewrites the parked block's `verdict:` to
   the answer as below; the review step never reads a reply. Otherwise every `ask` goes to the user in **one
   `AskUserQuestion`**, one question per finding, the reviewer's fix as the first option unless
   the task text contradicts it, in which case skip comes first with the ask cited. It fires at
   the end of the entry's **last** resolve: standalone, that is this one; inside a pipeline, a
   resolve that recorded a `fix` knows a second round follows and holds its asks for the
   round-two resolve, which asks for both rounds together, while a resolve with no `fix` is the
   last and asks now. Back-to-back calls when the tool's per-call limit forces them are still
   one checkpoint; never one question per turn. Answers are applied as `fix` or `skip` above,
   verify re-run where they fixed anything, and the answered block's `verdict:` line is
   rewritten to the answer with the user's words on `reason:`. A fix chosen in that final
   questionnaire is verified, not re-reviewed, and opens no round.
8. **Write the verdicts.** Append `verdict:` and, where there is one, `reason:` under each block,
   one `verdict:` line per block - an `ask` answered later is rewritten, never joined by a
   second. The reviewer's text above those lines is never edited, so the file stays an honest
   record of both sides.
9. **Report** per `conventions/report.md` - a `findings` row with the file's path. Then, as
   prose: the verdict counts, what the fixes changed, and which asks the user answered.

**On failure:** verify red after a fix (point 6), a findings file that names a task this checkout
does not have, or a malformed file - report and stop, the verdicts written so far left in the
file. See `conventions/failure.md`.
