# Step · catchup - a branch brought up to date with its base, conflicts settled

Role: a **senior engineer and git expert** integrating someone else's changes into your branch, keeping both sides' intent.
Input: the current branch, in this checkout.
Output: the branch rebased onto its base or merged with it, every conflict settled, and verify green.
This step pushes nothing, and it writes no task, no plan and no tracker comment.
Context: the `catchup` and `catchup_stacked` frontmatter keys, which set the strategy, and the loader's `verify:` line.
Badge: none.
This step never retires or overwrites a badge that a ship run left.
Its `f10-state.sh done catchup` call (point 5) is not a badge: it is the end signal that `f10 drive` reads.

A run that meets no conflict, or finds nothing to bring in, is a **correct run**.

**The branch marks.**
f10 records what a catchup needs as git config keys on the branch, `branch.<branch>.<key>`.

- `f10-after` and `f10-after-branch` are the base task and its branch, which `f10 start --after` records.
- `f10-landed` says the branch must take in the default branch.
  `f10 finish` sets it when the base task merged.
  `f10 drive` sets it, and so does the watcher under `watch_catchup: on`, when the branch's pull request falls behind the default branch or conflicts with it.
  Ship's guard sets it when bringing the default branch in conflicts.
- `f10-base-moved` is set in the same ways, for an `f10-after-branch` base that moved past this branch.
  It names the base commit that the setter checked against origin before the branch is pushed.
- `f10-base-tip` is the base commit the branch was last built on.
  `f10 start --after` and `f10 finish` set it, and clearing the marks refreshes it.

`f10-landed` and `f10-base-moved` stay set until a catchup clears the marks.

To **clear the marks**, `git config --unset` each of `branch."$(git branch --show-current)".f10-landed` and `.f10-base-moved` that is set.
Then set `.f10-base-tip` to the `sha` row when `found` is `f10-after-branch`.
Otherwise unset `.f10-base-tip`, because the base is now the default branch.

1. **Check the preconditions.**
   An **operation already in progress** is a merge or a rebase the user started by hand, and this step settles it in place.
   The loader prints an `in progress:` line above this step when there is one.
   Without that line, look under `git rev-parse --git-dir`: a `MERGE_HEAD` there is a merge, and a `rebase-merge/` directory or a `rebase-apply/rebasing` file is a rebase.
   The operation's other side is the base, and ours and theirs follow point 4's rule for that operation.
   For an operation in progress, skip the fetch, the base lookup (point 2), the integration (point 3) and the dirty-tree stop below.
   Name the operation and its other side in one line, then go to point 4 and point 5.
   Point 5 reads that other side to decide which marks it may clear.
   Under a rebase, `git rebase --continue` carries on through the remaining commits, as point 4 says.

   With no operation in progress, a dirty tree stops the run: name the dirty files one per line and touch nothing.
   **Never stash**, and never pass `--autostash`, because the stash is shared across worktrees.

2. **Find the base and the method** with `f10 pr catchup`.
   It fetches origin and prints these `key: value` lines:
   - `base`, the ref to integrate, and `sha`, its commit.
   - `upstream`, the base's old tip, printed only with `method: onto`.
   - `found` and `how`, what the base was found through and how.
   - `method`, one of `merge`, `rebase`, `onto` or `none`, and `why`, what set it.
   - `mark`, the key and value that a conflict sets.
   - `code`, printed only for an `f10-after-branch` base.
     It is `yes`, or `none` when the base branch has no commit of its own past its own base, merges not counted.

   The base is the first of these that applies:
   - the default branch, when `f10-landed` is set;
   - the `f10-after-branch` branch, at the commit `f10-base-moved` names when that mark is set, and otherwise with its local branch read first;
   - the default branch.

   When `f10-base-moved` is set, `base` and `sha` are the commit it names, so no base commit past it rides in.
   When the branch holds `f10-base-tip` and the base no longer does, because the base was squash-merged or rebased, the method is `onto`.
   `found: f10-after-branch gone` means the base task was finished.
   In that case `git config --unset` both `f10-after` and `f10-after-branch`; the base printed is already the default branch.

   Print the `how` line before anything moves.

3. **Integrate** by the `method` line.
   The `why` line names the frontmatter key that set the method, `catchup:` or, for an `f10-after-branch` base, `catchup_stacked:`, or it says the method is the default.
   `catchup: off` turns off only ship's guard (**As a guard**, below).
   A run asked for by name, or sent by `f10 drive`, still integrates.
   - `none`: the base is already in the branch, so there is nothing to bring in.
     Clear the marks and write the `done catchup` signal as point 5 does, then report and stop.
   - `merge`: run `f10 trace trailers --step catchup` first (`conventions/context.md` → Commits), then `git merge --no-edit <base> -m "<subject>" -m "$trailers"`, so the merge commit carries the trailers.
   - `rebase`: `git rebase <base>`.
   - `onto`: `git rebase --onto <base> <upstream>`, which replays only the branch's own commits and never the base's old ones.

   A clean integration goes on to point 5, and a conflict goes to point 4.
   Never pass `-X ours`, `-X theirs` or `--skip`.

4. **Settle each conflict, keeping both sides.**
   Take one side only where it supersedes the other, and say so.
   - Under a merge, ours is the branch and theirs is the base.
     Under a rebase, `onto` included, the two are **swapped**.
     Say which applies once, before touching a file.
   - Triage with `git diff --name-status --diff-filter=U`.
     `UU` is a textual conflict.
     `AA`, `DU`, `UD`, `AU` and `UA` are structural conflicts, and each gets a decision.
   - Never hand-merge a generated file, such as a lock file, a schema dump or a compiled asset.
     Settle its source, then regenerate it with the project's command, and ask first if that command is slow or has side effects.
   - Read the whole file.
     Where a hunk is unclear, read each side's history since `git merge-base`, with `git log` and `git diff`.
   - A rename on one side and a change on the other keeps the rename and re-applies the change.
     A file both sides added becomes one implementation.
     A change on one side and a delete on the other is decided from context, and asked when context does not settle it.
   - **Never leave a marker** (`<<<<<<<`, `=======`, `>>>>>>>`).
     Never invent code beyond merge glue, such as a combined import list.
   - **Genuine ambiguity** is both sides changing the same business logic in incompatible ways.
     Collect every such file into **one** `AskUserQuestion`.
     For each file, say what ours and theirs wanted, and offer keep ours, keep theirs, or a named reconciliation.
   - `git add` each settled file, then confirm with `git grep` over the staged files that no conflict or marker remains.
   - Then conclude the operation.
     Under a merge, commit with `git commit -m "<subject>" -m "$trailers"`, the trailers printed first as in point 3.
     Never use `git merge --continue`, because the commit must carry the trailers.
     Under a rebase, run `git rebase --continue` once for every conflicting commit; replayed commits keep their own messages.
   - Once the operation is concluded, each hunk settled by taking one side, or by a named reconciliation, is a **decision** on the branch's task (`docs/lore.md`).
     Anchor it at the commit that now holds it: `f10 lore add decision --change <the id f10 lore change prints> --anchor <path:line[-end]> --chosen <the side or the reconciliation> --rejected '<the other side>: <why it gave way>' -m "catchup: <path> - <the intent both sides had>"`.
     The body opens with `catchup: <path>` because the pr step renders the change's catchup decisions from it, under the PR's `## Catchup` heading.
     No decision is written for both sides kept mechanically, for a branch with no task, or for a clean integration.
     A failing `lore add` is one line in the report, never a failed step.

5. **Verify** with the load's `verify:` line (`conventions/context.md` → Verify).
   Red verify with no textual conflict is a **semantic conflict**, such as a renamed function or a dropped column.
   Fix it by the branch's intent, then verify again.
   Under a merge, the fix goes in a fix-up commit (`conventions/context.md` → Commits, `--step catchup`).
   Under a rebase, fold it into the commit it fixes.

   Once verify is green, clear the marks, then run `f10-state.sh done catchup`, all before the report.
   An operation already in progress on entry is the exception: unset a mark only when the operation's other side is the base that mark names.
   For `f10-landed`, that base is the default branch's tip, local or `origin/`.
   For `f10-base-moved`, it is the sha the mark holds, or a commit containing it.
   Any other mark stays set, because the drive reads a cleared mark as that base brought in, and the report names it.

   The `done catchup` signal is how `f10 drive` knows a catchup ended.
   The drive halts an idle agent without the signal after a minute, and it also halts a task whose signal sits beside a mark still set.
   Every run writes the signal, a run by hand included.

6. **Report** per `conventions/report.md`, with `branch`, `base` and `commit` (the new HEAD) rows.
   For an operation that was already in progress, `base` is the other side the loader named.
   Under the block, list:
   - the commits brought in, which for an operation already in progress are the ones the user's merge or rebase brings;
   - each file's resolution: kept both, merged intent, took one side (and why), regenerated, or asked;
   - verify green;
   - after a rebase or `onto`, that the next push needs `--force-with-lease`, which this step does not run.

   When `why` says the method was the default, name the frontmatter key that declares it: `catchup: merge | rebase | off`, or `catchup_stacked: merge | rebase` for a base found through `f10-after-branch`.

**As a guard**: `/f10:ship` runs this step before implement, before each `pr` entry and before a change judge, as the ship skill says.
It does so because the PR should carry the moved base, and a verdict should be on code that merges.
The guard differs from the run above in these ways:

- **Base and method** come from `f10 pr catchup --guard <entry>`, where the entry is `implement`, `pr` or `judge`.
  Before `pr` and before a change judge, it reads every base from origin, because origin holds what the PR targets.
  That way unpushed commits of another task never ride into this PR, and `f10-base-moved` changes nothing.
  Before implement, point 2's order stays, and a branch with no recorded base has nothing to do.
  Once this branch is on origin, the guard merges, whatever `catchup:` or `catchup_stacked:` says, so the push after it never forces.
  `found: f10-after-branch gone` unsets both keys, as point 2 says.
- **Nothing to do** puts nothing in the report.
  `method: skip` covers `catchup: off`, the default branch itself, no recorded base before implement, and a base not yet on origin before `pr`.
  `method: none` means the base is already an ancestor, and the guard still clears the marks.
- **A clean tree** is the ship skill's job: it commits the tree first where it has to, and the guard never stashes.
- **An operation already in progress** on entry belongs to the user, not to the guard.
  Fail the run before anything moves, and never abort the operation.
  Put the operation and its other side in the `state` row, and `/f10:catchup` in `next`.
- **A conflict fails the run.**
  The guard settles no conflict and asks nothing.
  Before failing, mark the branch with the `mark` line, `git config branch.<branch>.<key> <value>`, so point 2 finds the same base later.
  Then run `git merge --abort` or `git rebase --abort`, so the branch is as it was, with any mark kept.
  Put `/f10:catchup` in the `next` row.
  `f10 drive` reads the mark, never the note, to decide to send a catchup.
- **Verify** runs when commits came in, except before implement, because implement runs its own verify next.
  Red verify is a semantic conflict that the guard leaves to `/f10:catchup`.
  Run `git reset --hard` to the sha read before integrating, which is safe because the tree was clean, then mark and fail as for a conflict.
- **Marks** are cleared once the integration is clean and verify, where it ran, is green.
  The guard writes no `done catchup` signal, because ship's phase reports for it.
- **The report** is one line in the ship run, naming the base, the commits brought in and the method.
  A merge commit carries the `--step catchup` trailers, as point 3 says.

**Driven** (`modes/driven.md`): a driven run is one sent by `f10 drive`, or by the watcher under `watch_catchup: on`.
On entry, note the ship status with `f10-state.sh show`.
The method is point 3's.
A drive and the watcher start from a clean tree, so a driven run never meets an operation already in progress.
The sender pushes the branch itself once this run ends clean.
The run never asks: no `AskUserQuestion` and no `f10-state.sh ask`.
Verify runs in the foreground, or is waited on in the same turn, and the turn ends only at the report (`modes/driven.md`).
`f10 drive` reads point 5's `done catchup` signal as the run's end, while the watcher reads only the mark and the tree.

Every question point 4 would put to the user is decided instead by the task's intent.
Read that intent once, from the plan file (`<storage root>/plans/<id>.md`, the id from `f10 task current`) and from the task body (`f10 task read <id>`).

- **Genuine ambiguity**: reconcile the two sides where they can be reconciled, as point 4 says.
  Where they truly clash, keep the side that serves the task, and name it in the report as `decided by the task's intent`.
- **A change against a delete** is decided the same way, and never asked.
- **A generated file**: settle its source and regenerate it with the project's command, without asking.
  A regenerate that fails is a failure, as below.

Each choice is written down, one per file, so the reviewer and the change judge see what was chosen:

- Point 4 writes a lore `decision` for a hunk settled by taking one side.
  A driven run writes one for every file decided by the task's intent, a change against a delete and a regenerate included.
- When the branch has an open PR, add a line to its body under a `## Catchup` heading: `<path>: kept <side>, <why>`.
  Read the body with `f10 pr view`.
  Add the line to the heading's existing lines, or under a new heading at the end of the body.
  Write it with `f10 pr edit --body-file <file>`, with the draft in a temp file outside the repo.
  With no PR, no line is written: lore and the report carry the choice, and the pr step renders the heading from lore when it opens the PR.

Any failure is `f10-state.sh set ship failed catchup` with a `note`, never an ask.
That covers the **On failure** cases below and a conflict the step cannot settle at all, such as a binary file both sides changed or a regenerate that fails.

**Sent by the watcher** (the prompt says so): note HEAD's sha before point 3 moves anything.
A failure then also puts the branch back at that sha, with its marks kept, because nobody follows up in this pane.
For an operation still in progress, run `git merge --abort` or `git rebase --abort`, whatever was settled.
Otherwise, when verify is red after the integration concluded, run `git reset --hard <that sha>`.
The report says which of the two ran.
The watcher reads a mark that is still set as a catchup that did not end, and it reports it once.

A clean end after a failure restores the noted ship status beside the signal, in either order.
A stop status keeps the signal.
A clean end that never stopped writes the signal alone.

Run by hand, without `--driven`, point 4 asks exactly as written.

**On failure:** the base has no ref, the operation cannot start, or verify stays red.
Abort with `git merge --abort` or `git rebase --abort` only if nothing is settled yet.
Otherwise keep the resolutions, and report which operation is in progress and which files are settled and staged.
An operation already in progress on entry is the user's, so never abort it.
It stays in progress, what was settled stays staged, and the report names it.
