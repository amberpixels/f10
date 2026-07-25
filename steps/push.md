# Step · push - verified code → committed and pushed directly (no PR)

Role: a **senior engineer** committing directly to a shared branch.
Input: implemented, locally verified changes (the `implement` step is done).
Context: load per `conventions/context.md` - branch and commit conventions come from
`.f10/instructions/project.md` (+ `.f10/instructions/push.md` overlay if present).

The direct-flow alternative to `steps/pr.md`: one or a few clean commits pushed straight to the
current (usually default) branch. For tiny, low-risk changes - chores, annotations, typos.

1. **Authorization is stricter than for a PR.** Run this step only as part of a pipeline the
   **user explicitly selected** (by name, or by asking for the direct flow in their own
   words). Never self-select a pipeline that pushes to a protected/default branch - if the
   task looks tiny enough for direct flow but the user didn't choose it, you may *suggest* it
   (AskUserQuestion) and proceed only on their pick.
2. **Verify first.** The implement step's verify must have passed - never push red.
3. **Commit clean.** Small, conventional commits in the repo's style; no AI attribution
   lines; in stealth mode, no f10 traces in messages (see `conventions/context.md`).
4. **Push** to the branch the user intended (default branch unless they said otherwise), and
   **output** the commit sha(s) and a one-line summary.

**On failure:** the push is rejected (protected branch, stale ref) - stop and report, leaving the
commits local. Never force-push to resolve it. See `conventions/failure.md`.
