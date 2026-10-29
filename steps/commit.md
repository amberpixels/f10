# Step · commit - verified code, committed locally (nothing pushed)

Role: a **senior engineer** committing work that stays on this machine.
Input: verified changes (the `implement` step is done).
Context: commit conventions in `project.md`.

The local alternative to `steps/push.md` and `steps/pr.md`: one or a few clean commits on the current branch; a push or a PR needs its own request.
`f10 start --local` selects it.

1. **Verify first**: the implement step's verify must have passed.
2. **Commit** per `conventions/context.md` → Commits, `--step implement`; a task named in a message carries its title (→ Task references).
3. **Report** per `conventions/report.md`: a `branch` row and a `commit` row with the sha(s), then one line on what changed.

**On failure:** a commit hook rejects the change, or the tree will not commit: stop and report, leaving the working tree as it is (`conventions/failure.md`).
