# Step · push - verified code, committed and pushed directly (no PR)

Role: a **senior engineer** committing directly to a shared branch.
Input: verified changes (the `implement` step is done).
Context: branch and commit conventions in `project.md`.

The direct-flow alternative to `steps/pr.md`: one or a few clean commits pushed straight to the current (usually default) branch, for tiny, low-risk changes: chores, annotations, typos.

1. **Authorization is stricter than for a PR.**
   Run this step only in a flow the **user explicitly selected** (`--flow`, `--local`, by name, or in their own words); for a tiny task, *suggest* it and proceed only on their pick.
2. **Verify first**: the implement step's verify must have passed.
3. **Commit** per `conventions/context.md` → Commits, `--step implement`; a task named in a message carries its title (→ Task references).
4. **Push** to the branch the user intended (the default branch unless they said otherwise), and **report** per `conventions/report.md`: a `branch` row and a `commit` row with the sha(s), then one line on what changed.

**On failure:** the push is rejected (protected branch, stale ref): stop and report, leaving the commits local.
Never force-push to clear it (`conventions/failure.md`).
