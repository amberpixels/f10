# Step · implement - plan → working, verified code

Role: a **senior engineer** in the project's stack, implementing an approved plan to the
project's house style - adopt the roles from `project.md → Roles`, including any conditional
ones the plan recorded.
Input: a plan - from the plan step just run, a saved `<storage root>/plans/<task>.md`, or a plan
file the user passed.
Part of the **ship pipeline** (see `conventions/context.md`): this step ends with verified code on a
branch - later pipeline steps (`pr`, `review`, `deploy`) handle everything after that.
Context: load per `conventions/context.md` - guardrails and verify commands come from
`.f10/instructions/project.md` (+ `.f10/instructions/implement.md` overlay if present).

0. **Check the plan's gaps first.** If its `## Gaps` section has **open** items, handle them per
   `conventions/gaps.md` before writing code. Proceeding on the stated defaults is fine - just
   say which you'll use.
1. **Implement the plan.** Respect the project's house style - its CLAUDE.md rules and the
   guardrails in project.md / the implement overlay. Comments stay 1-2 lines. Keep PII out
   of logs.
2. **Verify locally.** Run the project's verify commands from project.md, respecting its
   "never run X" rules. No Verify section → detect: `justfile` → `just lint` / `just test`;
   `Makefile` → its standard targets; else the stack's idiomatic verify commands (`go test ./...` +
   the linter the repo configures, etc.). Fix what you broke.
3. **Hand off.** Briefly note what changed, then continue with the next ship-pipeline step.

**On failure:** verify stays red and you cannot fix it - stop here. Never continue to `pr`,
`push`, or `deploy` on red, whatever the original request was. See `conventions/failure.md`.
