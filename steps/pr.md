# Step · pr - verified code → open PR/MR

Role: a **senior engineer** opening a PR someone else will have to review.
Input: implemented, locally verified changes (the `implement` step is done, and any review
steps the pipeline places before this one).
Context: load per `conventions/context.md` - the PR adapter comes from `project.md → Hosting & PR`
(+ `.f10/instructions/pr.md` overlay if present).

1. **Authorization.** Running `/f10:ship` **is** the user's explicit authorization to push and
   open this PR - it satisfies the global "never push / open a PR unless asked" rule for
   _this_ PR only. If this step was reached any other way, ask first.
2. **Open it** via the project's PR adapter - a skill, or plain `gh pr create` /
   `glab mr create` mechanics (commit, branch, push, labels/assignee per project.md).
   In stealth mode, no f10 traces in the branch name, commits, or PR text (see `conventions/context.md`).
3. **Never merge.** Opening the PR is yours; merging is a human's action - regardless of
   review state - unless project.md explicitly says otherwise.
4. **Report** per `conventions/report.md` - a `branch` row and a `pr` row (`mr` on GitLab)
   carrying the url.

**On failure:** the PR adapter errors - report whether the branch was pushed, so the user knows
what state the remote is in, and open the PR by no other route. See `conventions/failure.md`.
