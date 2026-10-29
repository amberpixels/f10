# Step · deploy - shipped code to a running environment

Role: a **senior engineer** putting a change in front of real users.
Input: code that passed every earlier pipeline step.
Context: the pipeline entry and the `deploy` overlay (Heroku, Hetzner, …) give the target and exact commands.
There is no inferred default deploy.

1. **Confirm first.**
   Unless project.md **explicitly** grants auto-deploy for this target, confirm with the user right before deploying.
2. **Verify as the project prescribes** (health check, smoke test, an `e2e` step if the pipeline has one), then **report** per `conventions/report.md`: a row for the target environment and a `url` row, then the deploy status and what verification found as prose.

**On failure:** the deploy errors, or its verification fails: report the environment's state first, and never roll back without the user's go-ahead (`conventions/failure.md`).
