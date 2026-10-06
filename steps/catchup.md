# Step · catchup - a branch → up to date with its base, conflicts settled

Role: a **senior engineer and git expert** integrating someone else's changes into your own
branch, keeping both sides' intent - adopt the roles from `project.md → Roles` on top.
Input: the current branch, in the checkout this runs in.
Output: the branch on top of (or merged with) its base, every conflict settled, verify green.
Nothing pushed, no task, no plan, no tracker comment.
Context: per `conventions/context.md` - the strategy comes from `project.md → Hosting & PR`,
the verify commands from `project.md → Verify` (+ same-named overlay).
Badge (`conventions/report.md`): none. Catchup is not a phase of the chain, and it never
retires or overwrites a badge a ship run left behind in this session.

The step exists for the moment a branch has fallen behind: the base task shipped more commits,
the default branch moved under a long-running branch, or ship's pre-implement integration hit a
conflict and stopped, pointing here. One verb for the intent, strategy-neutral: a merge-flow
project and a rebase-flow project run the same command. A catchup that finds no conflict, or
nothing to bring in at all, is a **correct run**, reported as one - not a no-op to apologise for.

**Authorization.** A merge produces a merge commit and a rebase rewrites the branch's commits.
Invoking `/f10:catchup` is the explicit ask for exactly those commits and nothing else - the
same way running `/f10:ship` authorizes the pr step's push. Nothing here pushes, ever, and
nothing here force-pushes: after a rebase the branch diverges from its remote, and the report
says so and names the push the user can run.

1. **Preconditions.** The working tree is clean. When it is not, stop and name the dirty files,
   one per line, and do not touch them: **never stash**, never `--autostash` - the stash stack
   is shared across worktrees and other sessions. A merge or rebase **already in progress with
   conflicts** is not a refusal: it is the "I merged by hand and now have conflicts" case.
   Detect it (`MERGE_HEAD` → a merge; `rebase-merge` or `rebase-apply` under the git dir → a
   rebase), say which operation it is and whose side ours and theirs are, and go straight to
   point 4 with that operation as the one to finish.

2. **Find the base.** First the mark `f10 finish` leaves on a dependent once its base task
   merged, then the branch `f10 start --after` recorded:

   ```
   git config --get branch."$(git branch --show-current)".f10-landed
   git config --get branch."$(git branch --show-current)".f10-after-branch
   ```

   `f10-landed` recorded: the base task's code is in the default branch and not in this one,
   so the base is the default branch, found as below. Clear the mark with `git config --unset`
   only once verify is green (point 5), so a stopped catchup leaves it for the next one.
   `f10-after-branch` recorded: `git fetch origin`, then local-first - `refs/heads/<branch>`, else
   `origin/<branch>` - exactly as `skills/ship/SKILL.md` does. A base that exists nowhere was
   finished and deleted, so its code is in the default branch: `git config --unset` both
   `f10-after` and `f10-after-branch` and carry on against the default branch. Nothing
   recorded: the default branch, named by
   `git symbolic-ref --quiet --short refs/remotes/origin/HEAD` with `origin/` stripped (else
   `main`), taken as `origin/<default>` after the fetch, or the local branch where the repo has
   no remote. Say in one line which base this run integrates and how it was found - the
   landed mark, the recorded dependency, or the default branch - before anything moves.

3. **Pick the strategy and integrate.** `project.md → Hosting & PR` may carry the phrase
   `catchup: merge` or `catchup: rebase`, in any case. Absent, **rebase**, which is what ship's
   pre-implement integration already does, and the report names the phrase to declare. Then:
   - the base is already an ancestor of HEAD (`git merge-base --is-ancestor <base> HEAD`):
     nothing to bring in. Report it as a correct run with the base named, and stop;
   - merge: `git merge --no-edit <base>` - the stock message, which carries no f10 trace;
   - rebase: `git rebase <base>`.

   A clean integration goes to point 5. A conflict goes to point 4. Never `-X ours`, never
   `-X theirs`, never `--skip`: each of those settles a conflict by dropping a side.

4. **Settle each conflict, both sides kept.** A conflict means both sides changed the same
   thing; the right resolution almost always keeps the meaning of *both* changes. Blindly
   taking one side loses work, and is right only where one side genuinely supersedes the other.
   - **Know which side is which.** Under a merge, ours is the branch and theirs is the base.
     Under a rebase they are **swapped**: ours is the base being rebased onto, theirs is the
     branch's own commit being replayed. Say this once before touching a file.
   - **Triage** by conflict type (`git diff --name-status --diff-filter=U`): `UU` is a textual
     conflict; `AA`, `DU`, `UD`, `AU`, `UA` are structural and get decided, never silently
     dropped. Generated files - lock files, schema dumps, compiled assets - are never
     hand-merged: settle their source, then regenerate them with the project's own command,
     and where that command is slow or has side effects, say so and ask before running it.
   - **Read before writing.** The whole file, not the marked hunk, and both sides' history
     since the merge base (`git log` and `git diff` of each side against
     `git merge-base`) where the hunk alone does not say what each side wanted.
   - **Reconcile**: keep both where the changes are independent; merge them where they touch
     the same logic with compatible intent (a rename on one side applied to the other side's
     new line); take one side only where the other is superseded, and say so in the report.
     Modified-on-one-side, deleted-on-the-other is a judgment call: decide it from context
     where context decides it, else ask. Both-added is two implementations of one thing,
     reconciled into one. Rename-and-modify keeps the rename and re-applies the change.
   - **Never leave a marker** (`<<<<<<<`, `=======`, `>>>>>>>`), and never invent code that was
     on neither side beyond the glue a merge needs, such as a combined import list.
   - **Genuine ambiguity goes to the user, once.** Two sides that changed the same business
     logic in incompatible ways cannot be settled from the diff. Collect every such file and
     put them to the user in **one** `AskUserQuestion` - per file: what ours wanted, what
     theirs wanted, and the options (keep ours, keep theirs, a named reconciliation). Only the
     truly ambiguous ones; the clear majority you settle yourself.
   - **Finish the operation.** `git add` each settled file, confirm nothing conflicted is left
     and no marker remains (`git grep` over the staged files), then `git merge --continue` or
     `git rebase --continue`. A rebase replays one commit at a time: repeat this point for
     every commit that conflicts until the rebase ends. Nothing is pushed.

5. **Verify.** The project's verify commands (`project.md → Verify`; none declared → detect as
   `steps/implement.md` does). Red after an integration that raised no textual conflict is a
   **semantic conflict** - the base renamed a function the branch calls, dropped a column the
   branch reads - and it is settled the same way: by the branch's intent against the incoming
   change, in a fix-up commit on the branch under a merge, or folded into the commit it belongs
   to under a rebase. Re-verify. Red that cannot be settled is the failure below.

6. **Report** per `conventions/report.md` - a `branch` row, a `base` row naming the branch
   integrated, and a `commit` row with the new HEAD sha. Then, as prose: how many commits came
   in, the per-file resolution list (kept both / merged intent / took one side, because /
   regenerated / asked), that verify is green, and - after a rebase - that the branch now
   diverges from its remote, so the next push needs `--force-with-lease`, which this step did
   not run. A run that found no conflict says so in one line; a run with nothing to bring in
   says that, with the base named. Where the strategy was the default, one line names
   `catchup: merge | rebase` under Hosting & PR as the way to declare it.

**Driven** (`modes/driven.md`, the run was prompted from another session, which `f10 drive`
does once a dependent's base is finished): catchup still owns no phase, but its stops go
through run state so the driving session can see them. On entry, note the ship status the run
holds (`f10-state.sh show`). A genuine ambiguity in point 4 is the run's one ask:
`f10-state.sh ask "<the questions>"; f10-state.sh set ship blocked catchup`. A failure is
`f10-state.sh set ship failed catchup` with a `note`. A clean end that followed one of those
puts the noted ship status back, so neither the badge nor a driver's position reading stays on
`catchup`. A clean end that never stopped writes nothing: the driver reads success from git,
from the mark gone, no merge or rebase in progress, and a clean tree.

**On failure:** the base does not resolve to any ref, the operation cannot start, or verify
stays red after the settling. Abort the operation (`git merge --abort` / `git rebase --abort`)
only when nothing has been settled yet, so the tree returns to where it was; once resolutions
exist, preserve them, and say exactly what state the tree is in - which operation is in
progress, which files are settled and staged, which are not. Format per
`conventions/failure.md`.
