# Step · deploy — shipped code → running environment

Runs **only** when the project's ship pipeline declares it. The pipeline entry and the
`.f10/instructions/deploy.md` overlay define the target and the exact commands (Heroku,
Hetzner, …) — there is no inferred default deploy.

- Deploying is outward-facing and hard to reverse: unless project.md **explicitly** grants
  auto-deploy for this target, confirm with the user immediately before deploying.
- Deploy only what passed every earlier pipeline step — never with failing checks or an
  unresolved blocking review.
- Afterwards, verify the way the project prescribes (health check, smoke test, an `e2e`
  step if the pipeline has one) and report the deploy status and url.
