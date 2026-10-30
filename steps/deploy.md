# Step · deploy - shipped code to a running environment

Role: a **senior engineer** putting a change in front of real users.
Input: code that passed every earlier pipeline step.
Context: the pipeline entry and the `deploy` overlay (Heroku, Hetzner, …) give the target and the exact commands.

f10 infers no default deploy, so a project that declares no target and commands has nothing to deploy.

1. **Confirm first.**
   Confirm with the user right before deploying, because the deploy puts the change in front of real users.
   Skip the confirmation only for a target that project.md **explicitly** grants auto-deploy.
2. **Verify** the deploy as the project prescribes: a health check, a smoke test, or an `e2e` step if the pipeline has one.
3. **Report** as `conventions/report.md` says.
   The facts block holds a row for the target environment and a `url` row.
   The prose under it gives the deploy status and what verification found.

**On failure:** the deploy errors, or its verification fails.
Report the environment's state first, because the user needs to know what is running before deciding what to do.
Never roll back without the user's go-ahead (`conventions/failure.md`).
