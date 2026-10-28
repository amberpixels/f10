# Step · catchup - a branch brought up to date with its base, conflicts settled

Role: a **senior engineer and git expert** integrating someone else's changes into your branch, keeping both sides' intent.
Input: the current branch, in this checkout.
Output: the branch on top of (or merged with) its base, every conflict settled, verify green.
Nothing pushed, no task, no plan, no tracker comment.
Context: the `catchup` frontmatter key (strategy), `project.md → Verify`.
Badge: none, and it never retires or overwrites a badge a ship run left.

No conflict, or nothing to bring in, is a **correct run**.

1. **Preconditions.**
   A dirty tree: stop, name the dirty files one per line, touch nothing.
   **Never stash**, never `--autostash` (the stash is shared across worktrees).
   A merge or rebase **already in progress with conflicts** (`MERGE_HEAD`: merge; `rebase-merge` or `rebase-apply` under the git dir: rebase) is the user's hand merge: name it and go to point 4.

2. **Find the base**, in this order:

   ```
   git config --get branch."$(git branch --show-current)".f10-landed
   git config --get branch."$(git branch --show-current)".f10-after-branch
   ```

   - `f10-landed` (set by `f10 finish` when the base task merged, or by `f10 drive` when the branch's pull request conflicts with the default branch): `git fetch origin`; the base is `origin/<default>`, the default branch found as under **Nothing** below, or the local `<default>` when it contains `origin/<default>` (a finish merged a branch never pushed there).
     It stays set until point 5 clears it.
   - `f10-after-branch`: `git fetch origin`, then `refs/heads/<branch>`, else `origin/<branch>`.
     `f10-base-moved` beside it (set by `f10 drive` when that branch moved past this one) changes nothing here; point 5 clears it.
     If it exists nowhere, the base task was finished: `git config --unset` both `f10-after` and `f10-after-branch` and use the default branch.
   - Nothing: the default branch, from `git symbolic-ref --quiet --short refs/remotes/origin/HEAD` without `origin/` (else `main`), as `origin/<default>` after the fetch, or the local branch without a remote.

   Say in one line which base and how it was found, before anything moves.

3. **Integrate.**
   Strategy: `catchup: merge` or `catchup: rebase` in the frontmatter (or, from an older file, under Hosting & PR); absent, **rebase**.
   `catchup: off` turns off only the guard ship runs (**As a guard**, below); a run asked for by name, or sent by `f10 drive`, uses the default method (rebase, a stacked base merged as below).
   A base found through `f10-after-branch` (another task's branch) is always merged: rebasing onto a branch that later rebases or squash-merges replays its old commits.
   A branch already on origin whose ship stopped (`f10-state.sh show` reads ship `failed` or `partial`) is merged too: ship resumes after it, and its push never forces.
   - base already an ancestor (`git merge-base --is-ancestor <base> HEAD`): nothing to bring in; clear the marks as point 5 does, report and stop;
   - merge: `git merge --no-edit <base>`; rebase: `git rebase <base>`.

   Clean: point 5.
   Conflict: point 4.
   Never `-X ours`, `-X theirs` or `--skip`.

4. **Settle each conflict, both sides kept**; take one side only where it supersedes the other, and say so.
   - Under a merge, ours is the branch and theirs the base; under a rebase they are **swapped**.
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
   - A clean merge takes the same message through `git merge <base> -m "<subject>" -m "$trailers"`.

5. **Verify** with `project.md → Verify` (none declared: a `justfile` means `just lint` / `just test`, a `Makefile` its standard targets, else the stack's idiomatic commands).
   Red with no textual conflict is a **semantic conflict** (a renamed function, a dropped column): fix it by the branch's intent, in a fix-up commit under a merge (`conventions/context.md` → Commits, `--step catchup`) or folded into its commit under a rebase, and re-verify.
   Green: `git config --unset` each of `branch."$(git branch --show-current)".f10-landed` and `.f10-base-moved` that is set, before the report.
   `f10 drive` reads a mark still set as a catchup that did not finish, and halts the task.

6. **Report** per `conventions/report.md`: `branch`, `base`, and `commit` (new HEAD) rows.
   Then: commits brought in, each file's resolution (kept both / merged intent / took one side, because / regenerated / asked), verify green, and after a rebase that the next push needs `--force-with-lease`, not run here.
   If the strategy was the default, name the `catchup: merge | rebase | off` frontmatter key as the way to declare it.

**As a guard**: `/f10:ship` runs this before implement, each `pr` entry and a change judge (the ship skill says when), so the PR carries the moved base and a verdict is on code that merges.
It differs from the run above:

- **Nothing to do** under `catchup: off`, on the default branch itself, or when the base is already an ancestor after `git fetch origin`; that says nothing, and the ancestor case still clears the marks as point 3 does.
- **Base**: point 2's order, the default branch when nothing is recorded.
  Before `pr` and a change judge, every base is read from origin (`origin/<branch>` for an `f10-after-branch` base, `origin/<default>` otherwise), what the PR targets, so unpushed commits of another task never ride into this PR; before implement, point 2's local-first order stays.
- **Method**: point 3's, except that this branch, once on origin (`origin/$(git branch --show-current)` after the fetch), is merged whatever `catchup:` says, so the push after it never forces.
- **Clean tree**: the ship skill commits the tree first where it has to; the guard never stashes.
- **A conflict fails the run**: `git merge --abort` / `git rebase --abort`, so the branch is as it was, any mark kept, and `/f10:catchup` in the `next` row.
  No conflict is settled and nothing is asked.
  Before failing, mark the branch for the base that conflicted: `git config branch.<branch>.f10-landed <default>` for the default branch, `git config branch.<branch>.f10-base-moved <the base ref's sha>` for an `f10-after-branch` base, so point 2 finds the same base.
  `f10 drive` reads the mark, never the note, to send a catchup.
- **Verify**, when commits came in, except before implement, whose own verify follows.
  Red is a semantic conflict the guard leaves to `/f10:catchup`: `git reset --hard` to the sha read before integrating (the tree was clean), then mark and fail as a conflict does.
- **Marks**: `f10-landed` and `f10-base-moved` are unset once the integration is clean and verify, where it ran, is green.
- **Report**: one line in the ship run naming the base, the commits brought in and the method; a merge commit carries `--step catchup` trailers as point 4 says.

**Driven** (`modes/driven.md`; `f10 drive` sends it when a dependent's base finished or its base branch moved, when the branch conflicts with the default branch at finish, or when ship's guard marked it): on entry, note the ship status (`f10-state.sh show`).
The ambiguity question is the run's one ask: `f10-state.sh ask "<the questions>"; f10-state.sh set ship blocked catchup`.
A failure: `f10-state.sh set ship failed catchup` with a `note`.
A clean end after either restores the noted status; a clean end that never stopped writes nothing.

**On failure:** no ref for the base, the operation cannot start, or verify stays red.
Abort (`git merge --abort` / `git rebase --abort`) only if nothing is settled yet; otherwise keep the resolutions and report which operation is in progress and which files are settled and staged.
