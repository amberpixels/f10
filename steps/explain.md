# Step · explain - one thing → what it is, to a peer

Role: the engineer **who knows this thing**, explaining it to a peer who knows the project.
Adopt the roles from `project.md → Roles` on top.
Input: a change - a branch, a PR/MR, a task's branch, a commit range, or the working tree - or a
thing: a concept, a file, a package, a function.
Output: a few sentences in chat. **Nothing on disk, no tracker comment, no code.**
Badge (`conventions/report.md`): none - it creates nothing to track.

The step exists for the moment you meet something you did not watch happen or never learned: an
agent shipped a branch while you were elsewhere, you checked out someone's PR and the name tells
you nothing, or a term keeps coming up in the code and nobody has said what it is. It answers the
one question and stops. For a change: **what was it, what is it now**. For a thing: **what is it**.
No verdict (`steps/judge.md`), no bugs (`steps/review.md`), no evidence (`steps/demo.md`).

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

## Route

The argument decides which of the two halves below runs. **Change-shaped** arguments are all
recognisable by form: nothing, `this branch`, a PR/MR number or url, the project's task-id format
or a tracker url, a sha or `<a>..<b>` range, `local` / `staged` / `uncommitted`. **Everything else
is a thing**: a path that exists on disk is a file or a package; a bare word or phrase is a concept
or a symbol. Prose around the subject - "very short", "is it just a day off?" - is how the asker
framed the question. Honour it as framing; never treat it as part of the name.

## A change

1. **Gather the diff.** No argument: the current branch against its base, and resolve that base
   rather than assuming `main` -
   `git symbolic-ref --quiet --short refs/remotes/origin/HEAD 2>/dev/null | sed 's|origin/||'`.
   A PR/MR number or url: the diff and description via the host CLI (`gh pr diff` /
   `gh pr view --comments`, `glab mr diff` / `glab mr view`). A task id: the task via the project's
   fetch adapter, plus `<storage root>/plans/<TASK-ID>.md` where one exists, and the branch or PR it
   resolves to. A sha or range: `git show` / `git diff`. `local`, `staged` or `uncommitted`: the
   working tree, `git diff` and `git diff --staged`. A branch with nothing committed against its
   base falls through to the working tree rather than reporting an empty subject.

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

## A thing

This is the hallway answer. Senior dev A asks senior dev B what a grace period is, and B, who wrote
it, uses three or four sentences to make it clear - not a presentation, not every detail, just
enough that A can go on with what they were doing. The reader is the same peer as above; the only
difference is that there is no base commit to hold a sentence against. Hold it against the
project instead: **cut every sentence that would still be true if this thing did not exist.**

1. **Find it in the code.** A path: read the file, or the package's entry points and its callers.
   A word: grep for it - the type, the table, the function, the constant, the enum value - and read
   the definition and the two or three places that use it. A README or a comment says what it was
   meant to be; only the code says what it is, and where they disagree the code wins and the gap
   gets said. A word that resolves to nothing in the code is a failure per `conventions/failure.md`:
   say what was searched, never define it from general knowledge.

2. **Say what it is, in one sentence.** The sentence a peer would say first: the noun, and the one
   fact that places it. "A grace period is the window after an invoice is due before it counts as overdue."

3. **Say what the name hides.** The part they would get wrong knowing only the word: what it
   triggers, what else it touches, the second table it writes, the state it leaves behind. "It is
   not just a delay - entering it pauses reminders and opens a dunning task for each unpaid line."
   This is the sentence the whole answer exists for; where there is nothing hidden, say that the
   name is the whole story.

4. **Name the one place to look.** The identifier they would grep for next: the type, the file,
   the handler. One, not a tour.

5. **Stop.** Three or four sentences, five where the thing is genuinely two things. Prose, no
   headings, no list, no "let me walk you through". No history, no motivation, no alternatives that
   were rejected - those are answers to questions the peer has not asked. Where the asker framed
   the question ("is it just X?"), answer that framing directly: yes, or no and what else.

## Both

**A bug noticed while reading is one line**, named plainly, with the skill that settles it -
`/f10:review` for whether it is real, `/f10:judge` for whether the shape is right. Never a section,
never a verdict, and never a reason to withhold the explanation the user asked for.

**It writes nothing and sends nothing.** The explanation is printed in chat. Where the user asks for
it on the PR or the tracker, that is outward-facing: confirm the target before posting, and carry no
f10 traces whatever the project's visibility (`conventions/context.md`).

**On failure:** a task id that does not resolve, a PR the host CLI cannot read, or a word that
matches nothing in the code, is a failure per `conventions/failure.md` - never explain a subject you
could not fetch or find. An **empty diff is not a failure**: say the branch carries no changes
against its base, and name the base you compared it to.
