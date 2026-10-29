# Step · pr - verified code to an open PR/MR, opened or refreshed

Role: a **senior engineer** opening a PR someone else will have to review.
Input: verified changes (`implement` and any earlier review steps done), and the branch's PR if one is open.
Context: `project.md → Hosting & PR` (the PR adapter).

1. **Authorization.**
   Pushing and opening or refreshing the PR are outward-facing, under the run's authorization, for _this_ PR only.
   A run that does not grant it asks first.
2. **The branch carries the task id**, where the run has one: `ABC-1042/dark-mode`, `feat/GH-22-lookaround`, whatever shape the project uses.
   The shape is the project's; the id in it is not, since `f10 task read` with no argument reads it back out of the branch.
   A run with no task id (free-text planless work) names its branch however the project does.
   A branch this step creates for a task id is linked to it, `git config branch.<name>.f10-task <id>`, so `f10 start`, `f10 finish`, `--after` and `f10 drive` find it whatever its shape.
3. **Open or refresh.**
   `f10 pr view` on the current branch: only an open PR counts, so `no open PR` (none, or one merged or closed) means open a new one (points 4 and 5), and an open one means refresh it (point 6).
   `no PR read` is a host that did not answer: the step fails (`conventions/failure.md`), never opening a second PR.
   In a ship flow the catchup guard (`steps/catchup.md` → **As a guard**) integrates the base before this step, unless `catchup: off`; this step never integrates it.
4. **A recorded dependency sets the base.**
   `f10 pr base --origin` prints the branch the PR targets: the one `f10 start --after` recorded, else the default branch.
   A recorded one is passed on (`gh pr create --base <branch>` / `glab mr create --target-branch <branch>`): the PR is stacked, and the base's `f10 finish` retargets it to the default branch.
   A non-zero exit is a base not on origin yet: stop and report (`conventions/failure.md`) with the branch named and `next: push the base task's branch`.
   One exception: when it says the recorded base is gone (finished and deleted, so its code is in the default branch), `git config --unset` both keys and open against the default branch it printed.
5. **Open it** via the project's PR adapter: a skill, or plain `gh pr create` / `glab mr create` mechanics (commit, branch, push, labels/assignee per project.md).
   A commit made here follows `conventions/context.md` → Commits, `--step implement`; in stealth mode, no f10 traces in the branch name, commits, or PR text.
   Another task named in the PR text is a link with its title, from `f10 task ref <id>... --md` (`conventions/context.md` → Task references).
   A **bundle** (riders in `git config --get branch."$(git branch --show-current)".f10-tasks`): the title is the lead's, and the body links every member with its title.
   A `## Catchup` section lists the choices a catchup made on this branch, one line per file (`<path>: kept <side>, <why>`), rendered from the change's lore decision entries whose body opens `catchup:` (`f10 lore show <id> --json`, `docs/lore.md`); no such entry, no section.
   On a host that closes issues on merge it carries one closing keyword per member, each on a line of its own (`Closes #12`): `f10 finish` rewrites a rider's line to keep it open.
   Once it is open, `f10 trace state --step pr` writes the hidden state block into its body (`docs/traces.md`): nothing with `traces: off`, and a non-zero exit is `partial` (`conventions/failure.md` rule 7), never a failed step.
6. **Refresh it**, so a later `pr` entry brings the description up to what review, resolve or judge changed.
   - **Push first**: commit what the tree holds per the adapter (`conventions/context.md` → Commits, `--step implement`), then push, never forcing, so the description never covers code the PR lacks.
   - **Someone else's PR** (view's `mine:` anything but `yes`, `unknown` included): stop after the push and leave the title and body as they are.
   - **Rewrite from scratch**: `git fetch origin <base>` for the PR's own base (view's `target` line), then draft title and body as point 5 would for a new PR, from `git diff origin/<base>...HEAD`, the task and the plan, never patching the old body ("also adds…").
     The title keeps its task id.
     Other tasks it names resolve through `f10 task ref <id>... --fresh --md`, so the PR merges with their current titles and urls.
   - **Owned sections**: a section the overlay names as maintained elsewhere (a screenshots section a script regenerates) is copied verbatim from the old body into its place in the new one; one the old body lacks stays out.
     `## Catchup` is owned by the **catchup** step the same way, except that where the old body has none it is rendered from lore as point 5 says.
     The old body is read for these sections only.
   - **The state block** (`docs/traces.md`): with plain mechanics, run `f10 trace state --step pr --body-file <file>` on the draft before writing it, and the block and its counts come over from the old body.
     A skill adapter takes no draft: there the block is an owned section, copied verbatim, and `f10 trace state --step pr` runs after the skill's write.
     With `traces: off` no block is written, and one the old body holds comes over as it is; a non-zero exit is `partial` (`conventions/failure.md` rule 7), never a failed step.
   - **Write it** through the project's PR adapter: a skill adapter refreshes through that skill, and one with no way to refresh fails the step (`conventions/failure.md` rule 4).
     Plain mechanics: `f10 pr edit --title … --body-file <file>`, the draft in a temp file outside the repo; the base, labels and assignee stay as they are.
7. **Never merge** unless project.md explicitly says otherwise.
8. **Report** per `conventions/report.md`: a `branch` row and a `pr` row (`mr` on GitLab) carrying the url, then one line saying whether the PR was opened, refreshed, or only pushed to.

**On failure:** the PR adapter errors: report whether the branch was pushed (`conventions/failure.md`); a refresh that fails after its push leaves the commits on the PR under a stale description, and the report says so.
