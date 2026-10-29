# Step · explain - what one thing is, to a peer

Role: the engineer **who knows this thing**, explaining it to a peer who knows the project.
Input: a change (a branch, a PR/MR, a task's branch, a commit range, the working tree) or a thing (a concept, a file, a package, a function).
Output: a few sentences in chat.
No verdict, no bug hunt, no evidence.

**The reader knows the product, the domain and the codebase, but not this change.**
Cut every sentence that would still be true without this diff: "The service caches responses in Redis" goes where Redis was already there; "The cache moved from in-process to Redis" stays.
Keep a fact only where the reader would get the change wrong without it, and identifiers (a column, a method, a flag, a package), not what they are *for*.

**Route.**
Change-shaped: nothing, `this branch`, a PR/MR number or url, a task id or tracker url, a sha, an `<a>..<b>` range or "last N commits", `local` / `staged` / `uncommitted`.
Anything else is a thing: an existing path is a file or package, a word or phrase a concept or symbol.
Words around the subject ("very short", "is it just a day off?") are framing to honour, not part of the name.

## A change: what was it, what is it now

1. **Gather the diff.**
   No argument: the current branch against its base (a stacked task's base branch, else the default branch), `f10 pr diff` and `f10 pr view --comments`.
   A PR/MR: the same two with its number or url, plus the task the view's `task:` line names and its plan.
   A task id: `f10 task read <id>`, its `<storage root>/plans/<TASK-ID>.md` where one exists, and `f10 pr diff <id>` / `f10 pr view --comments <id>`.
   A sha or range: `git show` / `git diff`.
   `local`, `staged`, `uncommitted`: `git diff` and `git diff --staged`.
   On the branch checked out here, `f10 pr diff` holds the working tree too, so a branch with no commits yet shows its uncommitted work.
2. **The `-` lines are the old behaviour** (a removed validation, a deleted method, a dropped default).
   The task and commit messages say what was *intended*; where they disagree with the diff, follow the diff and say so.
3. **Every item's lead names both sides.**
   - Good: **"The cache moved from in-process to Redis."**
   - Bad: **"Adds a Redis cache."** or **"Caching improvements."**

   For something new, say the absence: "there was no retry at all; failed calls now retry three times".
   Under each lead, at most two sentences: the mechanism, and the reason a reviewer would ask for.
4. **One item per change the reader would notice**: two to five, one screen at most, grouped by decision, never by commit.
5. **Invisible is the answer** when nothing outside the code behaves differently: "Nothing behaves differently; the retry logic moved from each caller into `transport.Do`."
6. **Rollout, only where deploying needs something**: a migration, a two-deploy sequence, a backfill and what it skips, a population that degrades until touched, debt left knowingly.
   Drop-in: one line saying so, or nothing.
7. **Review, only where one ran**: one line per finding that changed the code, worst first.
8. **Report**: the subject first, as a facts block (`conventions/report.md`) for a task id or PR url, else as the first prose line; then the items.

## A thing: what is it

Three or four sentences, five where the thing is really two things.
Cut every sentence that would still be true if this thing did not exist.

1. **Find it in the code.**
   A path: the file, or the package's entry points and callers.
   A word: grep the type, table, function, constant or enum value; read the definition and two or three uses.
   Where a README or comment disagrees with the code, the code wins, and say so.
2. **What it is, in one sentence**: "A grace period is the window after an invoice is due before it counts as overdue."
3. **What the name hides**, the sentence that matters most: what it triggers or touches, the state it leaves.
   "It is not just a delay - entering it pauses reminders and opens a dunning task for each unpaid line."
   If nothing is hidden, say the name is the whole story.
4. **The one place to look**: the identifier they would grep for next.
5. **Stop.**
   No history, motivation or rejected alternatives.
   Answer a framed question ("is it just X?") directly: yes, or no and what else.

## Both

A bug noticed while reading is one line, pointing at `/f10:review` (is it real) or `/f10:judge` (is the shape right); never withhold the explanation for it.
Posting the explanation on the PR or tracker, at the user's request, is outward-facing: confirm the target first, no f10 traces (`conventions/context.md`).

**On failure:** an unknown task id, an unreadable PR, or a word that matches nothing in the code: say what was searched, and never explain what you could not find.
An **empty diff is not a failure**: say so and name the base.
