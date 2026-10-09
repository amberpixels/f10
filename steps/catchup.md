# Step · catchup - a branch brought up to date with its base, conflicts settled

Role: a **senior engineer and git expert** integrating someone else's changes into your branch,
keeping both sides' intent.
Input: the current branch, in this checkout.
Output: the branch on top of (or merged with) its base, every conflict settled, verify green.
Nothing pushed, no task, no plan, no tracker comment.
Context: `project.md → Hosting & PR` (strategy), `project.md → Verify`.
Badge: none, and it never retires or overwrites a badge a ship run left.

Invoking `/f10:catchup` authorizes the merge commit or rebased commits it produces, nothing else.
Never push or force-push. No conflict, or nothing to bring in, is a **correct run**.

1. **Preconditions.** A dirty tree: stop, name the dirty files one per line, touch nothing.
   **Never stash**, never `--autostash` (the stash is shared across worktrees). A merge or rebase
   **already in progress with conflicts** (`MERGE_HEAD`: merge; `rebase-merge` or
   `rebase-apply` under the git dir: rebase) is the user's hand merge: name it and go to
   point 4.

2. **Find the base**, in this order:

   ```
   git config --get branch."$(git branch --show-current)".f10-landed
   git config --get branch."$(git branch --show-current)".f10-after-branch
   ```

   - `f10-landed` (set by `f10 finish` when the base task merged): the base is the default
     branch. `git config --unset` it only after verify is green (point 5).
   - `f10-after-branch`: `git fetch origin`, then `refs/heads/<branch>`, else `origin/<branch>`.
     If it exists nowhere, the base task was finished: `git config --unset` both `f10-after`
     and `f10-after-branch` and use the default branch.
   - Nothing: the default branch, from
     `git symbolic-ref --quiet --short refs/remotes/origin/HEAD` without `origin/` (else `main`),
     as `origin/<default>` after the fetch, or the local branch without a remote.

   Say in one line which base and how it was found, before anything moves.

3. **Integrate.** Strategy: `catchup: merge` or `catchup: rebase` under
   `project.md → Hosting & PR`, any case; absent, **rebase**.
   - base already an ancestor (`git merge-base --is-ancestor <base> HEAD`): nothing to bring in;
     report and stop;
   - merge: `git merge --no-edit <base>`; rebase: `git rebase <base>`.

   Clean: point 5. Conflict: point 4. Never `-X ours`, `-X theirs` or `--skip`.

4. **Settle each conflict, both sides kept**; take one side only where it supersedes the other,
   and say so.
   - Under a merge, ours is the branch and theirs the base; under a rebase they are **swapped**.
     Say which once, before touching a file.
   - Triage with `git diff --name-status --diff-filter=U`: `UU` is textual; `AA`, `DU`, `UD`,
     `AU`, `UA` are structural and get decided, never silently dropped. Never hand-merge
     generated files (lock files, schema dumps, compiled assets): settle the source, then
     regenerate with the project's command, asking first if it is slow or has side effects.
   - Read the whole file, and where the hunk is unclear, each side's history since
     `git merge-base` (`git log`, `git diff`). Rename-and-modify keeps the rename and re-applies
     the change; both-added becomes one implementation; modify-versus-delete is decided from
     context, else asked.
   - **Never leave a marker** (`<<<<<<<`, `=======`, `>>>>>>>`), and never invent code beyond
     merge glue such as a combined import list.
   - **Genuine ambiguity** (both sides changed the same business logic incompatibly): collect
     every such file into **one** `AskUserQuestion`, per file what ours and theirs wanted, with
     options keep ours, keep theirs, a named reconciliation.
   - `git add` each file, confirm no conflict or marker remains (`git grep` over the staged
     files), then `git merge --continue` or `git rebase --continue`; repeat for every
     conflicting commit of a rebase.

5. **Verify** with `project.md → Verify` (none declared: detect as `steps/implement.md` does).
   Red with no textual conflict is a **semantic conflict** (a renamed function, a dropped
   column): fix it by the branch's intent, in a fix-up commit under a merge or folded into its
   commit under a rebase, and re-verify.

6. **Report** per `conventions/report.md`: `branch`, `base`, and `commit` (new HEAD) rows. Then:
   commits brought in, each file's resolution (kept both / merged intent / took one side,
   because / regenerated / asked), verify green, and after a rebase that the next push needs
   `--force-with-lease`, not run here. If the strategy was the default, name
   `catchup: merge | rebase` under Hosting & PR as the way to declare it.

**Driven** (`modes/driven.md`; `f10 drive` sends it when a dependent's base finished): on entry,
note the ship status (`f10-state.sh show`). The ambiguity question is the run's one ask:
`f10-state.sh ask "<the questions>"; f10-state.sh set ship blocked catchup`. A failure:
`f10-state.sh set ship failed catchup` with a `note`. A clean end after either restores the
noted status; a clean end that never stopped writes nothing.

**On failure:** no ref for the base, the operation cannot start, or verify stays red. Abort
(`git merge --abort` / `git rebase --abort`) only if nothing is settled yet; otherwise keep the
resolutions and report which operation is in progress and which files are settled and staged.
