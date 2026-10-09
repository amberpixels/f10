# Step · push - verified code, committed and pushed directly (no PR)

Role: a **senior engineer** committing directly to a shared branch.
Input: verified changes (the `implement` step is done).
Context: branch and commit conventions in `project.md`.

The direct-flow alternative to `steps/pr.md`: one or a few clean commits pushed straight to the
current (usually default) branch. For tiny, low-risk changes: chores, annotations, typos.

1. **Authorization is stricter than for a PR.** Run this step only in a pipeline the **user
   explicitly selected** (by name, or in their own words). Never self-select a pipeline that
   pushes to a protected/default branch; if the task looks tiny enough, *suggest* it
   (AskUserQuestion) and proceed only on the user's pick.
2. **Verify first.** The implement step's verify must have passed; never push red.
3. **Commit clean.** Small, conventional commits in the repo's style; no AI attribution
   lines; in stealth mode, no f10 traces in messages (`conventions/context.md`).
4. **Push** to the branch the user intended (default branch unless they said otherwise), and
   **report** per `conventions/report.md`: a `branch` row and a `commit` row with the sha(s),
   then a one-line summary of what changed as prose.

**On failure:** the push is rejected (protected branch, stale ref): stop and report, leaving the
commits local. Never force-push to clear it (`conventions/failure.md`).
