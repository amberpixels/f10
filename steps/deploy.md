# Step · deploy - shipped code to a running environment

Role: a **senior engineer** putting a change in front of real users.
Input: code that passed every earlier pipeline step.
Context: the pipeline entry and the `deploy` overlay (Heroku, Hetzner, …) give the target and
exact commands. There is no inferred default deploy.

Run this step **only** when the project's ship pipeline declares it.

1. **Confirm first.** Deploying is outward-facing and hard to reverse: unless project.md
   **explicitly** grants auto-deploy for this target, confirm with the user immediately before
   deploying.
2. **Deploy only what is green**: never with verify failing or a blocking review unresolved.
3. **Verify the way the project prescribes** (health check, smoke test, an `e2e` step if the
   pipeline has one), then **report** per `conventions/report.md`: a row for the target
   environment and a `url` row for the deployed environment, then the deploy status and what
   verification found as prose.

**On failure:** the deploy errors, or its verification fails: report the environment's state
first. Never retry blindly, and never roll back without the user's go-ahead
(`conventions/failure.md`).
