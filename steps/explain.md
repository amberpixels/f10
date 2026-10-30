# Step · explain - what one thing is, to a peer

Role: the engineer **who knows this thing**, explaining it to a peer who knows the project.
Input: a change or a thing.
A change is a branch, a PR/MR, a task's branch, a commit range or the working tree.
A thing is a concept, a file, a package or a function.
Output: a few sentences in chat.

The step gives no verdict, hunts no bugs and collects no evidence.
The reader already knows the product, the domain and the codebase.

## Which kind of subject

The subject is a change when it is one of these:

- nothing at all, or `this branch`
- a PR/MR number or url
- a task id or a tracker url
- a sha, an `<a>..<b>` range, or "last N commits"
- `local`, `staged` or `uncommitted`

Anything else is a thing.
An existing path is a file or a package.
A word or a phrase is a concept or a symbol.

Words around the subject, such as "very short" or "is it just a day off?", frame the question.
They are not part of the subject's name, and the answer honours them.

## A change: what it was, and what it is now

The reader knows the codebase but not this change.
So cut every sentence that would still be true without this diff.
For example, "The service caches responses in Redis" goes if Redis was already there, but "The cache moved from in-process to Redis" stays.
Keep a fact only if the reader would get the change wrong without it.
Name identifiers, such as a column, a method, a flag or a package, and leave out what they are *for*.

1. **Gather the diff.**
   What to read depends on the subject:
   - **No argument**: the current branch against its base, which `f10 pr base` prints, through `f10 pr diff` and `f10 pr view --comments`.
   - **A PR/MR**: `f10 pr diff` and `f10 pr view --comments` with its number or url.
     Also read the task that the view's `task:` line names, and that task's plan.
   - **A task id**: `f10 task read <id>`, its plan at `<storage root>/plans/<TASK-ID>.md` if one exists, then `f10 pr diff <id>` and `f10 pr view --comments <id>`.
   - **A sha or a range**: `git show` or `git diff`.
   - **`local`, `staged` or `uncommitted`**: `git diff` and `git diff --staged`.

   On the branch checked out here, `f10 pr diff` includes the working tree, so a branch with no commits yet still shows its uncommitted work.
2. **Read the old behaviour from the `-` lines**, such as a removed validation, a deleted method or a dropped default.
   The task and the commit messages say what was *intended*.
   If they disagree with the diff, follow the diff and say that they disagree.
3. **Lead every item with both sides**: what it was, and what it is now.
   - Good: **"The cache moved from in-process to Redis."**
   - Bad: **"Adds a Redis cache."** or **"Caching improvements."**

   For something new, name the absence: "there was no retry at all; failed calls now retry three times".
   Under each lead, write at most two sentences: the mechanism, and the reason a reviewer would ask for.
4. **Write one item per change the reader would notice.**
   Write two to five items, and keep them to one screen at most.
   Group the items by decision, never by commit.
5. **If nothing outside the code behaves differently, say that the change is invisible.**
   For example: "Nothing behaves differently; the retry logic moved from each caller into `transport.Do`."
6. **Mention rollout only if deploying needs something.**
   That is a migration, a sequence of two deploys, a backfill and what it skips, a population that stays degraded until it is touched, or debt left knowingly.
   If the change is drop-in, write one line saying so, or nothing.
7. **Mention review only if one ran**: one line per finding that changed the code, worst first.
8. **Report the subject first, then the items.**
   For a task id or a PR url, the subject is a facts block (`conventions/report.md`).
   For any other subject, it is the first line of prose.

## A thing: what it is

Write three or four sentences, or five if the thing is really two things.
Cut every sentence that would still be true if this thing did not exist.

1. **Find it in the code.**
   For a path, read the file, or the package's entry points and its callers.
   For a word, grep for the type, table, function, constant or enum value it names, then read the definition and two or three of its uses.
   If a README or a comment disagrees with the code, the code wins, and say that they disagree.
2. **Say what it is in one sentence**, such as "A grace period is the window after an invoice is due before it counts as overdue."
3. **Say what the name hides**, because this is the sentence that matters most: what the thing triggers or touches, and the state it leaves.
   For example: "It is not just a delay: entering it pauses reminders and opens a dunning task for each unpaid line."
   If nothing is hidden, say that the name is the whole story.
4. **Point to the one place to look**: the identifier the reader would grep for next.
5. **Stop.**
   Leave out history, motivation and rejected alternatives.
   If the question was framed, such as "is it just X?", answer it directly: yes, or no and what else it is.

## Either kind of subject

If you notice a bug while reading, give it one line.
That line points at `/f10:review` to check whether the bug is real, or at `/f10:judge` to check whether the shape is right.
Never hold back the explanation because of the bug.

The user may ask you to post the explanation on the PR or the tracker.
Posting is outward-facing, so confirm the target first.
The posted text carries no f10 traces (`conventions/context.md`).

**On failure**, such as an unknown task id, an unreadable PR, or a word that matches nothing in the code, say what you searched.
Never explain something you could not find.
**An empty diff is not a failure**: say that the diff is empty and name the base.
