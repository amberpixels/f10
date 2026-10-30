# Step · push - verified code, committed and pushed directly (no PR)

Role: a **senior engineer** committing directly to a shared branch.
Input: verified changes (the `implement` step is done).
Context: branch and commit conventions in `project.md`.

This step commits the change and pushes it straight to the current branch, usually the default branch, with no PR.
It is the direct-flow alternative to `steps/pr.md`, meant for tiny, low-risk changes such as chores, annotations and typos.

1. **Authorization is stricter than for a PR**, because the change reaches a shared branch with nobody reviewing it.
   Run this step only in a flow the **user explicitly selected**: with `--flow`, with `--local`, by the flow's name, or in their own words.
   For a tiny task you may *suggest* such a flow, but proceed only if the user picks it.
2. **Verify first.**
   Commit only once the implement step's verify has passed.
3. **Commit** as `conventions/context.md` → Commits says, with `--step implement`, in one or a few clean commits.
   A task named in a commit message carries its title, as `conventions/context.md` → Task references says.
4. **Push** to the branch the user intended.
   That is the default branch, unless the user named another.
5. **Report** as `conventions/report.md` says.
   The facts block holds a `branch` row and a `commit` row with the sha of each commit.
   One line under it says what changed.

**On failure:** the push is rejected, for example by a protected branch or a stale ref.
Stop and report, and leave the commits local.
Never force-push to clear the rejection (`conventions/failure.md`), because a force-push to a shared branch can overwrite other people's commits.
