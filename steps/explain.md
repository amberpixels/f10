# Step · explain - a diff → what it was, what it is

Role: the engineer **answerable for this change**, explaining it to a peer who knows the project.
Adopt the roles from `project.md → Roles` on top.
Input: a diff - a branch, a PR/MR, a task's branch, a commit range, or the working tree.
Output: a few paired sentences in chat. **Nothing on disk, no tracker comment, no code.**
Badge (`conventions/report.md`): none - it creates nothing to track.

The step exists for the moment you meet a change you did not watch happen: an agent shipped it
while you were elsewhere, or you checked out someone's branch and the name tells you nothing. It
answers one question - **what was it, what is it now** - and stops. No verdict (`steps/judge.md`),
no bugs (`steps/review.md`), no evidence (`steps/demo.md`).

## The reader

**They know the product, the domain and the codebase. They have not seen this change.** That one
fact decides every cut. Never explain what the project does, why the area exists, what a term of
art means, or how a pattern the repo already uses works. They are not catching up on the project,
they are catching up on one change to it.

**Cut every sentence that would still be true if this diff did not exist.** `conventions/voice.md`
cuts the clause that survives a change of topic; this cuts the sentence that survives a change of
diff. "The service caches responses in Redis" goes, where Redis was already there. "The cache moved
from in-process to Redis" stays. The test is mechanical - hold the sentence against the base commit
- which is the only reason it gets obeyed.

**A fact earns its place only where the reader would get the change wrong without it.** Named
identifiers stay: a column, a method, a flag, a package is how they find it in the code. What those
identifiers are *for* goes. They can read.

## Steps

1. **Gather the diff.** No argument: the current branch against its base, and resolve that base
   rather than assuming `main` -
   `git symbolic-ref --quiet --short refs/remotes/origin/HEAD 2>/dev/null | sed 's|origin/||'`.
   A PR/MR number or url: the diff and description via the host CLI (`gh pr diff` /
   `gh pr view --comments`, `glab mr diff` / `glab mr view`). A task id: the task via the project's
   fetch adapter, plus `<storage root>/plans/<TASK-ID>.md` where one exists, and the branch or PR it
   resolves to. A sha or range: `git show` / `git diff`. `local`, `staged` or `uncommitted`: the
   working tree, `git diff` and `git diff --staged`. A branch with nothing committed against its
   base falls through to the working tree rather than reporting an empty subject.

   **A path is not a subject.** `/f10:explain internal/steps/plan.go` is a request to read code, not
   to explain a change. Say so in one line and name the changes on offer - this branch, an open PR -
   instead of explaining the file.

2. **Read the deleted lines for the old behaviour.** The `-` side of the diff is what was true: a
   removed validation, a removed column read, a deleted method, a dropped default. The task and the
   commit messages say what was *intended*; only the `-` lines say what the code did. Where the two
   disagree, follow the diff and say where they parted.

3. **Every item names both sides.** The lead sentence carries the pair, because a reader who gets
   only the new state cannot tell what it replaced or whether it replaced anything:

   - Good: **"The cache moved from in-process to Redis."**
   - Bad: **"Adds a Redis cache."** or **"Caching improvements."**

   Where the change adds something that did not exist, the absence is the before and it gets said:
   "there was no retry at all; failed calls now retry three times". Where it removes something, the
   removal is the after. Under each lead, at most two sentences: the mechanism, and the reason a
   reviewer would ask for.

4. **One item per change the reader would notice.** Two to five for an ordinary branch. Past what
   one screen holds it has become a changelog, and a changelog is what they would have read instead
   of asking. Group by decision, never by commit - the commit boundaries are an artifact of how the
   work went, and nobody asked how the work went.

5. **Say what is invisible, as invisible.** Where nothing outside the code behaves differently - a
   refactor, a dependency bump, a CI change - that is the answer, not an apology for it. "Nothing
   behaves differently; the retry logic moved from each caller into `transport.Do`" is complete, and
   it is what stops the reader looking for a behaviour change that is not there.

6. **Rollout, only where deploying it needs something.** A migration, a two-deploy sequence, a
   backfill and what it skips, a population that degrades until touched, debt this change knowingly
   leaves. Where it is drop-in, one line saying so, or nothing.

7. **Review, only where one ran.** One line per finding that changed the code, worst first. Where no
   review ran, there is no section and no sentence noting its absence.

8. **Report.** Open by naming the subject: where it has addressable values - a task id, a PR url -
   that is a facts block per `conventions/report.md`; where it is just a branch, it is the first
   line of the prose. Then the items. No file is written, so there is no artifact row and no path to
   print.

**A bug noticed while reading is one line**, named plainly, with the skill that settles it -
`/f10:review` for whether it is real, `/f10:judge` for whether the shape is right. Never a section,
never a verdict, and never a reason to withhold the explanation the user asked for.

**It writes nothing and sends nothing.** The explanation is printed in chat. Where the user asks for
it on the PR or the tracker, that is outward-facing: confirm the target before posting, and carry no
f10 traces whatever the project's visibility (`conventions/context.md`).

**On failure:** a task id that does not resolve, or a PR the host CLI cannot read, is a failure per
`conventions/failure.md` - never explain a subject you could not fetch. An **empty diff is not a
failure**: say the branch carries no changes against its base, and name the base you compared it to.
