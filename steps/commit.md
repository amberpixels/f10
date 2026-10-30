# Step · commit - verified code, committed locally (nothing pushed)

Role: a **senior engineer** committing work that stays on this machine.
Input: verified changes (the `implement` step is done).
Context: commit conventions in `project.md`.

This step leaves the change as one or a few clean commits on the current branch, and pushes nothing.
It is the local alternative to `steps/push.md` and `steps/pr.md`, so a push or a PR needs a request of its own.
`f10 start --local` selects it.

1. **Verify first.**
   Commit only once the implement step's verify has passed.
2. **Commit** as `conventions/context.md` → Commits says, with `--step implement`.
   A task named in a commit message carries its title, as `conventions/context.md` → Task references says.
3. **Report** as `conventions/report.md` says.
   The facts block holds a `branch` row and a `commit` row with the sha of each commit.
   One line under it says what changed.

**On failure:** a commit hook rejects the change, or the tree will not commit.
Stop and report as `conventions/failure.md` says.
Leave the working tree as it is, so the user finds the change where the failed commit left it.
