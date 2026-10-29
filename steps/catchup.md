# Step · catchup - a branch brought up to date with its base, conflicts settled

Role: a **senior engineer and git expert** integrating someone else's changes into your branch, keeping both sides' intent.
Input: the current branch, in this checkout.
Output: the branch on top of (or merged with) its base, every conflict settled, verify green.
Nothing pushed, no task, no plan, no tracker comment.
Context: the `catchup` and `catchup_stacked` frontmatter keys (strategy), `project.md → Verify`.
Badge: none, and it never retires or overwrites a badge a ship run left; `f10-state.sh done catchup` (point 5) is the end signal `f10 drive` reads, not a badge.

No conflict, or nothing to bring in, is a **correct run**.

1. **Preconditions.**
   **Already in progress**: the loader's `in progress:` line above this step is the user's hand merge, settled in place.
   Without the line, `MERGE_HEAD` under `git rev-parse --git-dir` is a merge, `rebase-merge/` or `rebase-apply/rebasing` a rebase.
   Its other side is the base; ours and theirs are point 4's for the operation.
   Skip the fetch, the base lookup (point 2), the integration (point 3) and the dirty-tree stop below: name the operation and its other side in one line, then go to point 4 and point 5, whose marks rule reads the other side.
   Under a rebase, `git rebase --continue` carries on through the remaining commits as point 4 says.
   **No operation in progress**, a dirty tree: stop, name the dirty files one per line, touch nothing.
   **Never stash**, never `--autostash` (the stash is shared across worktrees).

2. **Find the base and the method**: `f10 pr catchup`.
   It fetches origin and prints `key: value` lines: `base` (the ref to integrate), `sha`, `upstream` (with `method: onto` only: the base's old tip), `found`, `how`, `method` (`merge`, `rebase`, `onto` or `none`), `why`, `mark` (the key and value a conflict sets), and for an `f10-after-branch` base, `code` (`yes`, or `none` when the base branch has no commit of its own past its own base, merges not counted).
   The base is `f10-landed`'s default branch, else the `f10-after-branch` branch (the commit `f10-base-moved` names, else local first), else the default branch.
   - `f10-landed` is set by `f10 finish` when the base task merged, or by `f10 drive` when the branch's pull request conflicts with the default branch; it stays set until point 5 clears it.
   - `f10-base-moved` (set by `f10 drive` or the watcher when the `f10-after-branch` base moved past this branch) names the base commit they check against origin before the branch is pushed: `base` and `sha` are that commit, so no base commit past it rides in; point 5 clears it.
   - `f10-base-tip` (set by `f10 start --after` and `f10 finish`, refreshed by point 5) is the base commit the branch was last built on; when the branch holds it and the base no longer does (the base was squash-merged or rebased), the method is `onto`.
   - `found: f10-after-branch gone`: the base task was finished, so `git config --unset` both `f10-after` and `f10-after-branch`; the base printed is already the default branch.

   Print its `how` line, before anything moves.

3. **Integrate** by the `method` line; `why` names the frontmatter key that set it (`catchup:`, or `catchup_stacked:` for an `f10-after-branch` base) or says it is the default.
   `catchup: off` turns off only ship's guard (**As a guard**, below); a run asked for by name, or sent by `f10 drive`, still integrates.
   - `none`: the base is already in the branch, nothing to bring in; clear the marks, set or unset `f10-base-tip` and write the signal as point 5 does, report and stop;
   - `merge`: `git merge --no-edit <base>`; `rebase`: `git rebase <base>`; `onto`: `git rebase --onto <base> <upstream>`, which replays only the branch's own commits, never the base's old ones.

   Clean: point 5.
   Conflict: point 4.
   Never `-X ours`, `-X theirs` or `--skip`.

4. **Settle each conflict, both sides kept**; take one side only where it supersedes the other, and say so.
   - Under a merge, ours is the branch and theirs the base; under a rebase (`onto` included) they are **swapped**.
     Say which once, before touching a file.
   - Triage with `git diff --name-status --diff-filter=U`: `UU` is textual; `AA`, `DU`, `UD`, `AU`, `UA` are structural and get decided.
     Never hand-merge generated files (lock files, schema dumps, compiled assets): settle the source, then regenerate with the project's command, asking first if it is slow or has side effects.
   - Read the whole file, and where the hunk is unclear, each side's history since `git merge-base` (`git log`, `git diff`).
     Rename-and-modify keeps the rename and re-applies the change; both-added becomes one implementation; modify-versus-delete is decided from context, else asked.
   - **Never leave a marker** (`<<<<<<<`, `=======`, `>>>>>>>`), and never invent code beyond merge glue such as a combined import list.
   - **Genuine ambiguity** (both sides changed the same business logic incompatibly): collect every such file into **one** `AskUserQuestion`, per file what ours and theirs wanted, with options keep ours, keep theirs, a named reconciliation.
   - `git add` each file, confirm no conflict or marker remains (`git grep` over the staged files), then conclude.
     Under a merge, `git commit -m "<subject>" -m "$trailers"` with the trailers `f10 trace trailers --step catchup` printed first (`conventions/context.md` → Commits), never `git merge --continue`, so the merge commit carries them.
     Under a rebase, `git rebase --continue`, repeated for every conflicting commit; replayed commits keep their own messages.
   - Once concluded, a hunk settled by taking one side, or by a named reconciliation, is a **decision** on the branch's task (`docs/lore.md`), anchored at the commit that now holds it: `f10 lore add decision --change <the id f10 task current prints> --anchor <path:line[-end]> --chosen <the side or the reconciliation> --rejected '<the other side>: <why it gave way>' -m "catchup: <path> - <the intent both sides had>"`.
     The body opens `catchup: <path>`, so the pr step renders the change's catchup decisions under the PR's `## Catchup` heading.
     Both sides kept mechanically, a branch with no task, or a clean integration writes none; a failing `lore add` is one line in the report, never a failed step.
   - A clean merge takes the same message through `git merge <base> -m "<subject>" -m "$trailers"`.

5. **Verify** with the load's `verify:` line (`conventions/context.md` → Verify).
   Red with no textual conflict is a **semantic conflict** (a renamed function, a dropped column): fix it by the branch's intent, in a fix-up commit under a merge (`conventions/context.md` → Commits, `--step catchup`) or folded into its commit under a rebase, and re-verify.
   Green: `git config --unset` each of `branch."$(git branch --show-current)".f10-landed` and `.f10-base-moved` that is set; set `.f10-base-tip` to the `sha` row when `found` is `f10-after-branch`, else unset it (the base is the default branch now); then `f10-state.sh done catchup`, all before the report.
   For an operation already in progress on entry, a mark is unset only when the operation's other side is the marked base (`f10-landed`: the default branch's tip, local or `origin/`; `f10-base-moved`: the sha it holds, or a commit containing it); otherwise it stays set, since the drive reads a cleared mark as that base brought in, and the report names it.
   The signal is the catchup's end for `f10 drive`: an idle agent without it is halted after a minute, and a signal beside a mark still set halts the task too.
   Every run writes it, by hand included.

6. **Report** per `conventions/report.md`: `branch`, `base`, and `commit` (new HEAD) rows; for an operation that was already in progress, `base` is the other side the loader named.
   Then: commits brought in (by the user's merge or rebase, for an operation already in progress), each file's resolution (kept both / merged intent / took one side, because / regenerated / asked), verify green, and after a rebase or `onto` that the next push needs `--force-with-lease`, not run here.
   If `why` says the method was the default, name the `catchup: merge | rebase | off` frontmatter key as the way to declare it, `catchup_stacked: merge | rebase` for a base found through `f10-after-branch`.

**As a guard**: `/f10:ship` runs this before implement, each `pr` entry and a change judge (the ship skill says when), so the PR carries the moved base and a verdict is on code that merges.
It differs from the run above:

- **Base and method**: `f10 pr catchup --guard <entry>` (`implement`, `pr` or `judge`).
  Before `pr` and a change judge it reads every base from origin, what the PR targets, so unpushed commits of another task never ride into this PR, and `f10-base-moved` changes nothing; before implement, point 2's order stays, and nothing recorded is nothing to do.
  This branch, once on origin, is merged whatever `catchup:` or `catchup_stacked:` says, so the push after it never forces.
  `found: f10-after-branch gone` unsets both keys, as point 2 says.
- **Nothing to do**: `method: skip` (`catchup: off`, the default branch itself, no recorded base before implement, a base not on origin yet before `pr`) says nothing; `method: none` (the base already an ancestor) says nothing and still clears the marks and refreshes `f10-base-tip` as point 3 does.
- **Clean tree**: the ship skill commits the tree first where it has to; the guard never stashes.
- **An operation already in progress** on entry (the loader's `in progress:` line) is the user's, not the guard's: fail the run before anything moves, never aborting it, with the operation and its other side in the `state` row and `/f10:catchup` in `next`.
- **A conflict fails the run**: `git merge --abort` / `git rebase --abort`, so the branch is as it was, any mark kept, and `/f10:catchup` in the `next` row.
  No conflict is settled and nothing is asked.
  Before failing, mark the branch with the `mark` line: `git config branch.<branch>.<key> <value>`, so point 2 finds the same base.
  `f10 drive` reads the mark, never the note, to send a catchup.
- **Verify**, when commits came in, except before implement, whose own verify follows.
  Red is a semantic conflict the guard leaves to `/f10:catchup`: `git reset --hard` to the sha read before integrating (the tree was clean), then mark and fail as a conflict does.
- **Marks**: `f10-landed` and `f10-base-moved` are unset, and `f10-base-tip` set or unset as point 5 says, once the integration is clean and verify, where it ran, is green.
  No `done catchup` signal: ship's phase reports for the guard.
- **Report**: one line in the ship run naming the base, the commits brought in and the method; a merge commit carries `--step catchup` trailers as point 4 says.

**Driven** (`modes/driven.md`; sent by `f10 drive`, or by the watcher under `watch_catchup: on`): on entry, note the ship status (`f10-state.sh show`).
The method is point 3's; a drive and the watcher start from a clean tree, so a driven run never meets an operation already in progress.
The sender pushes the branch itself once this run ends clean.
The run never asks: no `AskUserQuestion`, no `f10-state.sh ask`.
Verify runs in the foreground or is waited on in the same turn, and the turn ends only at the report (`modes/driven.md`).
Point 5's `done catchup` signal is the end `f10 drive` reads; the watcher reads the mark and the tree alone.
Every question point 4 would put to the user is decided by the task's intent, read once from the plan file (`<storage root>/plans/<id>.md`, the id from `f10 task current`) and the task body (`f10 task read <id>`):

- **genuine ambiguity**: reconcile where both sides can be, as point 4 says; where they truly clash, keep the side that serves the task, named in the report as `decided by the task's intent`;
- **modify-versus-delete**: decided the same way, never asked;
- **generated files**: settle the source and regenerate with the project's command, without asking; a regenerate that fails is a failure as below.

Each choice is written down, one per file, so the reviewer and the change judge see what was chosen:

- the lore `decision` point 4 writes for a hunk settled by taking one side, here for every file decided by the task's intent, a modify-versus-delete and a regenerate included;
- a line under a `## Catchup` heading in the PR body, when the branch has an open PR (its body from `f10 pr view`): `<path>: kept <side>, <why>`, added to the heading's lines, or under a new heading at the end of the body.
  Write it with `f10 pr edit --body-file <file>`, the draft in a temp file outside the repo.
  No PR, no line: lore and the report carry it, and the pr step renders the heading from lore when it opens one.

Any failure, the **On failure** cases below included and a conflict the step cannot settle at all (a binary both sides changed, a regenerate that fails), is `f10-state.sh set ship failed catchup` with a `note`, never an ask.
**Sent by the watcher** (the prompt says so): note HEAD's sha before point 3 moves anything, and a failure also puts the branch back there, its marks kept, since nobody follows up in this pane: `git merge --abort` / `git rebase --abort` for an operation still in progress, whatever was settled, else (verify red after the integration concluded) `git reset --hard <that sha>`; the report says which.
The watcher reads the mark still set as a catchup that did not end and reports it once.
A clean end after a failure restores the noted status beside the signal, in either order: a stop status keeps the signal; a clean end that never stopped writes the signal alone.
Run by hand, without `--driven`, point 4 asks exactly as written.

**On failure:** no ref for the base, the operation cannot start, or verify stays red.
Abort (`git merge --abort` / `git rebase --abort`) only if nothing is settled yet; otherwise keep the resolutions and report which operation is in progress and which files are settled and staged.
An operation already in progress on entry is the user's: never abort it; it stays in progress, what was settled stays staged, and the report names it.
