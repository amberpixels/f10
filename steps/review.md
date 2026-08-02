# Step · review - get the changes reviewed, findings resolved

Role: a **senior engineer** triaging review findings on your own change - adopt the roles from
`project.md → Roles`, including any conditional ones the plan recorded.
Input: the working diff (local review) or the open PR (external review).
Context: per `conventions/context.md` - who reviews and what resolves it comes from
`project.md → Review` (+ same-named overlay).

This step can appear **before** the PR (local review of the working diff) or **after** it
(external/CI/human) - even both, as separate pipeline entries; the entry names the variant.

1. **Local review** (pre-PR): run the declared reviewer - a review skill, or a fresh subagent -
   against the working diff. Triage findings like a senior engineer: fix what's real, push back
   on what's not (don't blindly agree). Re-verify after fixes.
2. **External / CI / human review** (post-PR): follow the overlay's protocol - how the review
   arrives, how to wait (poll with `ScheduleWakeup`, foreground `sleep` is disabled, bounded
   re-checks), and what resolves it. If triggering another review requires a manual action
   (a comment, a button), **never perform it yourself**: the user does it, or you ask the user
   first.
3. **Bound the loop.** At most **two** rounds per review step, then stop and summarize - never
   loop indefinitely. And never merge the PR (see `steps/pr.md`).

**On failure:** the declared reviewer is unavailable or never returns - report it and stop.
Never record the step as passed, and never skip it silently. See `conventions/failure.md`.
