# Step · pr - verified code to an open PR/MR, opened or refreshed

Role: a **senior engineer** opening a PR someone else will have to review.
Input: verified changes (`implement` and any earlier review steps done), and the branch's PR if one is open.
Context: `project.md → Hosting & PR` (the PR adapter).

The **PR adapter** is how the project opens and edits PRs.
It is a skill when `project.md → Hosting & PR` names one.
Otherwise it is the **plain commands** this step names: `git`, `f10 pr create` and `f10 pr edit`.
Any commit this step makes follows `conventions/context.md` → Commits, with `--step implement`, because it carries implemented code.

1. **Authorization.**
   Pushing and opening or refreshing the PR are outward-facing actions.
   The run's authorization covers them for _this_ PR only.
   A run that does not grant that authorization asks first.
2. **The branch carries the task id**, when the run has one: `ABC-1042/dark-mode`, `feat/GH-22-lookaround`, or whatever shape the project uses.
   The project chooses the shape.
   The id must still be in it, because `f10 task read` with no argument reads the id back out of the branch name.
   A run with no task id (free-text planless work) names its branch the way the project does.
   When this step creates a branch for a task id, it links the branch to the task with `git config branch.<name>.f10-task <id>`.
   That link lets `f10 start`, `f10 finish`, `--after` and `f10 drive` find the branch whatever its shape.
3. **Open or refresh.**
   Run `f10 pr view` on the current branch.
   Only an open PR counts.
   `no open PR` means there is none, or the last one was merged or closed, so open a new one (point 6).
   An open PR means refresh it (point 7).
   `no PR read` means the host did not answer, so the step fails (`conventions/failure.md`) and never opens a second PR.
   This step never integrates the base branch.
   In a ship flow, the catchup guard (`steps/catchup.md` → **As a guard**) has already integrated it before this step, unless the project sets `catchup: off`.
4. **The base the PR targets.**
   `f10 pr base --origin` prints it: the branch `f10 start --after` recorded, else the default branch.
   `f10 pr create` opens the PR against that same base.
   So a recorded base stacks the PR on the base task's branch, and the base task's `f10 finish` later retargets the PR to the default branch.
   A non-zero exit means the base is not on origin yet.
   Stop and report it (`conventions/failure.md`), naming the branch, with `next: push the base task's branch`.
   There is one exception.
   A `found: f10-after-branch gone` line under the branch means the base task finished and its branch was deleted, so its code is already in the default branch.
   In that case, run `git config --unset` on both `f10-after` and `f10-after-branch`, and open against the default branch the command printed.
5. **Draft the title and body.**
   Opening and refreshing draft them the same way.
   - Write the draft to a temp file outside the repo.
   - In stealth mode, the branch name, the commits and the PR text carry no f10 traces.
   - Another task named in the PR text is a link carrying its title, from `f10 task ref <id>... --md` (`conventions/context.md` → Task references).
   - A **bundle** is several tasks shipped in one PR, and the `task:` line of `f10 pr view` names it as `<lead>+<riders>`.
     A bundle's title is the lead's, and its body links every member with its title.
   - On a host that closes issues on merge, the body carries one closing keyword per member, each on a line of its own (`Closes #12`).
     `f10 finish` later rewrites a rider's line, so the rider's issue stays open.
   - The `## Catchup` section lists the choices a catchup made on this branch, one line per file: `<path>: kept <side>, <why>`.
     Render it from the change's lore decision entries whose body opens with `catchup:` (`f10 lore show <id> --json`, `docs/lore.md`).
     With no such entry, leave the section out.
     On a refresh, the old body's section is copied instead (point 7).
6. **Open it** through the PR adapter.
   A skill adapter opens the PR itself.
   With the plain commands, commit, run `git push -u origin <branch>`, then run `f10 pr create --title … --body-file <file>`.
   `--label` and `--assignee` follow `project.md`.
   `f10 pr create` prints the new PR's url.
   It refuses to open a PR when origin does not hold the branch at `HEAD`.
   If a PR is already open, it prints that PR's url instead of opening a second one.
7. **Refresh it.**
   A later `pr` entry in the flow refreshes the PR, so its description covers what review, resolve or judge changed since it opened.
   - **Push first.**
     Commit what the tree holds through the adapter, then push, never forcing.
     The push comes first so the description never covers code the PR lacks.
   - **Someone else's PR** is one whose `mine:` line in `f10 pr view` says anything but `yes`, `unknown` included.
     Stop after the push, and leave its title and body as they are.
   - **Rewrite from scratch.**
     Draft the title and body as point 5 says, and never patch the old body ("also adds…").
     Draft from the task, the plan and `f10 pr diff`, which shows the change against the PR's own base as just pushed.
     The title keeps its task id.
     Links to other tasks come from `f10 task ref <id>... --fresh --md`, so the PR merges with their current titles and urls.
   - **Owned sections** are sections the overlay names as maintained elsewhere, such as a screenshots section a script regenerates.
     Copy each one verbatim from the old body into its place in the new one.
     An owned section the old body lacks stays out.
     `## Catchup` is owned by the **catchup** step in the same way, with one difference: when the old body has no `## Catchup`, render it from lore as point 5 says.
     Read the old body for owned sections only.
   - **Write it** through the PR adapter.
     A skill adapter refreshes the PR through that skill.
     A skill with no way to refresh fails the step (`conventions/failure.md` rule 4).
     With the plain commands, run `f10 pr edit --title … --body-file <file>`, with the draft in a temp file outside the repo.
     The base, labels and assignee stay as they are.
8. **The hidden state block** is a block in the PR body that `f10 trace state --step pr` writes (`docs/traces.md`).
   When it runs depends on how the body was written:
   - After opening a PR, run `f10 trace state --step pr` once the PR is open.
   - On a refresh with the plain commands, run `f10 trace state --step pr --body-file <file>` on the draft, before point 7 writes it.
     The block and its counts come over from the old body.
   - On a refresh with a skill adapter, there is no draft to run it on.
     The block is then an owned section, copied verbatim, and `f10 trace state --step pr` runs after the skill writes the body.

   With `traces: off`, no block is written.
   On a refresh, a block the old body already holds comes over as it is.
   A non-zero exit from `f10 trace state` marks the run `partial` (`conventions/failure.md` rule 7), and is never a failed step.
9. **Never merge** unless project.md explicitly says otherwise.
10. **Report** per `conventions/report.md`: a `branch` row and a `pr` row (`mr` on GitLab) carrying the url, then one line saying whether the PR was opened, refreshed, or only pushed to.

**On failure:** the PR adapter errors.
Report whether the branch was pushed (`conventions/failure.md`).
A refresh that fails after its push leaves the new commits on the PR under a stale description, and the report says so.
