# Step · commit - verified code, committed locally (nothing pushed)

Role: a **senior engineer** committing work that stays on this machine.
Input: verified changes (the `implement` step is done).
Context: commit conventions in `project.md`.

The local alternative to `steps/push.md` and `steps/pr.md`: one or a few clean commits on the
current branch, and nothing leaves the machine. `f10 start --local` selects it; it replaces
freeform "do not push" steering as the way to keep a shipment local.

1. **Verify first.** The implement step's verify must have passed; never commit red.
2. **Commit clean.** Small, conventional commits in the repo's style; no AI attribution
   lines; in stealth mode, no f10 traces in messages (`conventions/context.md`).
3. **Never push, never open a PR.** Either needs a new, explicit request; this step's shipment
   is the commits.
4. **Report** per `conventions/report.md`: a `branch` row and a `commit` row with the sha(s),
   then a one-line summary of what changed as prose.

**On failure:** a commit hook rejects the change, or the tree will not commit: stop and report,
leaving the working tree as it is (`conventions/failure.md`).
