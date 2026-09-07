# Step · pr - verified code → open PR/MR

Role: a **senior engineer** opening a PR someone else will have to review.
Input: implemented, locally verified changes (the `implement` step is done, and any review
steps the pipeline places before this one).
Context: per `conventions/context.md` - the PR adapter comes from `project.md → Hosting & PR`
(+ same-named overlay).

1. **Authorization.** Running `/f10:ship` **is** the user's explicit authorization to push and
   open this PR - it satisfies the global "never push / open a PR unless asked" rule for
   _this_ PR only. If this step was reached any other way, ask first.
2. **The branch carries the task id.** Where the run has one, it appears in the branch name -
   `WS-2703/postponed-signatures`, `feat/GH-22-lookaround`, whatever shape the project uses. The
   shape is the project's; the id being in it is not. Every other artifact f10 produces is named
   by that id - the plan file, the report's rows - and `f10 task read` with no argument reads it
   back out of the branch, so a branch without it breaks the chain silently, later, for whoever
   did not open the PR. A run with no task id (a planless pipeline, free-text work) names its
   branch however the project does.
3. **Open it** via the project's PR adapter - a skill, or plain `gh pr create` /
   `glab mr create` mechanics (commit, branch, push, labels/assignee per project.md).
   An adapter's numbered procedure is a **spec, not a turn budget**: run every step it declares,
   in its order, but collapse consecutive mechanical ones into a single call
   (`conventions/latency.md`), splitting only where an output decides what comes next. Never
   skip, reorder, or substitute a different tool (`conventions/failure.md` rule 4).
   In stealth mode, no f10 traces in the branch name, commits, or PR text (see `conventions/context.md`).
4. **Never merge.** Opening the PR is yours; merging is a human's action - regardless of
   review state - unless project.md explicitly says otherwise.
5. **Report** per `conventions/report.md` - a `branch` row and a `pr` row (`mr` on GitLab)
   carrying the url.

**On failure:** the PR adapter errors - report whether the branch was pushed, so the user knows
what state the remote is in, and open the PR by no other route. See `conventions/failure.md`.
