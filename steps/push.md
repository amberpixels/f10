# Step · push — verified code → committed and pushed directly (no PR)

The direct-flow alternative to `pr.md`: one or a few clean commits pushed straight to the
current (usually default) branch. For tiny, low-risk changes — chores, annotations, typos.

1. **Authorization is stricter than for a PR.** Run this step only as part of a pipeline the
   **user explicitly selected** (by name, or by asking for the direct flow in their own
   words). Never self-select a pipeline that pushes to a protected/default branch — if the
   task looks tiny enough for direct flow but the user didn't choose it, you may *suggest* it
   (AskUserQuestion) and proceed only on their pick.
2. **Verify first.** The implement step's checks must have passed — never push red.
3. **Commit clean.** Small, conventional commits in the repo's style; no AI attribution
   lines; in stealth mode, no f10 traces in messages (see `context.md`).
4. **Push** to the branch the user intended (default branch unless they said otherwise), and
   **output** the commit sha(s) and a one-line summary.
