# Step · pr - verified code to an open PR/MR

Role: a **senior engineer** opening a PR someone else will have to review.
Input: verified changes (`implement` and any earlier review steps done).
Context: `project.md → Hosting & PR` (the PR adapter).

1. **Authorization.** Running `/f10:ship` **is** the user's explicit authorization to push and
   open this PR: it satisfies the global "never push / open a PR unless asked" rule for _this_
   PR only. Reached any other way, ask first.
2. **The branch carries the task id**, where the run has one: `ABC-1042/dark-mode`,
   `feat/GH-22-lookaround`, whatever shape the project uses. The shape is the project's; the id
   being in it is not. `f10 task read` with no argument reads the id back out of the branch,
   so a branch without it breaks the chain later, silently. A run with no task id (a planless
   pipeline, free-text work) names its branch however the project does.
3. **A recorded dependency sets the base.** Where `f10 start --after` recorded one on the branch
   (`git config --get branch."$(git branch --show-current)".f10-after-branch`), the PR targets
   that branch (`gh pr create --base <branch>` / `glab mr create --target-branch <branch>`), so
   it is stacked and the host retargets it when the base merges. The base must already be on
   origin; when it is not, stop and report (`conventions/failure.md`) with the branch named and
   `next: push the base task's branch`. Never push another task's branch from this run, and
   never open against the default branch instead. One exception: a base that exists neither
   locally nor on origin was finished and deleted, so its code is in the default branch:
   `git config --unset` both keys and open against the default branch.
4. **Open it** via the project's PR adapter: a skill, or plain `gh pr create` /
   `glab mr create` mechanics (commit, branch, push, labels/assignee per project.md). Run the
   adapter's numbered procedure as a spec, not a turn budget (`conventions/latency.md`). In
   stealth mode, no f10 traces in the branch name, commits, or PR text
   (`conventions/context.md`).
5. **Never merge**, whatever the review state, unless project.md explicitly says otherwise.
   Merging is a human's action.
6. **Report** per `conventions/report.md`: a `branch` row and a `pr` row (`mr` on GitLab)
   carrying the url.

**On failure:** the PR adapter errors: report whether the branch was pushed, and open the PR by
no other route (`conventions/failure.md`).
