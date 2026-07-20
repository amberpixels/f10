# Step · review — get the changes reviewed, findings resolved

A ship-pipeline step that can appear **before** the PR (local review of the working diff) or
**after** it (external/CI/human review of the PR) — even both, as separate pipeline entries.
The pipeline entry in `project.md` names the variant; mechanics live in the
`.f10/instructions/review.md` overlay.

**Local review** (pre-PR): run the declared reviewer — a review skill, or a fresh subagent —
against the working diff. Triage findings like a senior dev: fix what's real, push back on
what's not (don't blindly agree). Re-verify after fixes.

**External / CI / human review** (post-PR): follow the overlay's protocol — how the review
arrives, how to wait (poll with `ScheduleWakeup`, foreground `sleep` is disabled, bounded
re-checks), and what resolves it. If triggering another review requires a manual action
(a comment, a button), **never perform it yourself**: the user does it, or you ask the user
first.

Bound every review loop: at most **two** rounds per review step, then stop and summarize —
never loop indefinitely. And never merge the PR (see `pr.md`).
