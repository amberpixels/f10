# Step · deploy - shipped code → running environment

Role: a **senior engineer** putting a change in front of real users.
Input: code that passed every earlier pipeline step.
Context: load per `conventions/context.md` - the target and the exact commands come from the
pipeline entry and the `.f10/instructions/deploy.md` overlay (Heroku, Hetzner, …). There is no
inferred default deploy.

Run this step **only** when the project's ship pipeline declares it.

1. **Confirm first.** Deploying is outward-facing and hard to reverse: unless project.md
   **explicitly** grants auto-deploy for this target, confirm with the user immediately before
   deploying.
2. **Deploy only what is green** - never with verify failing or an unresolved blocking review.
3. **Verify the way the project prescribes** (health check, smoke test, an `e2e` step if the
   pipeline has one) and report the deploy status and url.

**On failure:** the deploy errors, or its verification fails - report the environment's state
first, before anything else. Never retry blindly, and never roll back without the user's
go-ahead. See `conventions/failure.md`.
