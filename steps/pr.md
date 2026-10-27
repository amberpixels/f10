# Step · pr - verified code to an open PR/MR, opened or refreshed

Role: a **senior engineer** opening a PR someone else will have to review.
Input: verified changes (`implement` and any earlier review steps done), and the branch's PR if one is open.
Context: `project.md → Hosting & PR` (the PR adapter).

1. **Authorization.**
   Pushing and opening or refreshing the PR are outward-facing, under the run's authorization, for _this_ PR only.
   A run that does not grant it asks first.
2. **The branch carries the task id**, where the run has one: `ABC-1042/dark-mode`, `feat/GH-22-lookaround`, whatever shape the project uses.
   The shape is the project's; the id being in it is not.
   `f10 task read` with no argument reads the id back out of the branch, so a branch without it breaks the chain later, silently.
   A run with no task id (free-text planless work) names its branch however the project does.
   A branch this step creates for a task id is linked to it, `git config branch.<name>.f10-task <id>`, so `f10 start`, `f10 finish`, `--after` and `f10 drive` find it whatever its shape.
3. **Open or refresh.**
   `gh pr view --json state,number,url,title,body,author,baseRefName` / `glab mr view -F json` (`state`, `iid`, `web_url`, `title`, `description`, `author.username`, `target_branch`) on the current branch.
   Only an open PR counts (`OPEN`, `opened` on GitLab): one merged or closed, or none, means open a new one (points 4 and 5); an open one means refresh it (point 6).
   In a ship flow the catchup guard (`steps/catchup.md` → **As a guard**) integrates the base before this step, unless `catchup: off`; this step never integrates it.
4. **A recorded dependency sets the base.**
   Where `f10 start --after` recorded one on the branch (`git config --get branch."$(git branch --show-current)".f10-after-branch`), the PR targets that branch (`gh pr create --base <branch>` / `glab mr create --target-branch <branch>`), so it is stacked and the base's `f10 finish` retargets it to the default branch.
   The base must already be on origin; when it is not, stop and report (`conventions/failure.md`) with the branch named and `next: push the base task's branch`.
   One exception: a base that exists neither locally nor on origin was finished and deleted, so its code is in the default branch: `git config --unset` both keys and open against the default branch.
5. **Open it** via the project's PR adapter: a skill, or plain `gh pr create` / `glab mr create` mechanics (commit, branch, push, labels/assignee per project.md).
   A commit made here is made as `steps/commit.md` point 2 says, trailers included (`--step implement`).
   In stealth mode, no f10 traces in the branch name, commits, or PR text (`conventions/context.md`).
   Another task named in the PR text is a link with its title, from `f10 task ref <id>... --md` (`conventions/context.md` → Task references).
   A **bundle** (riders in `git config --get branch."$(git branch --show-current)".f10-tasks`): the title is the lead's, and the body links every member with its title.
   On a host that closes issues on merge it carries one closing keyword per member, each on a line of its own (`Closes #12`): `f10 finish` rewrites a rider's line to keep it open.
   Once it is open, `f10 trace state --step pr` writes the hidden state block into its body (`docs/traces.md`): nothing with `traces: off`, and a non-zero exit is `partial` (`conventions/failure.md` rule 7), never a failed step.
6. **Refresh it**, so a later `pr` entry brings the description up to what review, resolve or judge changed.
   - **Push first**: commit what the tree holds per the adapter (`steps/commit.md` point 2, `--step implement`), then push, never forcing, so the description never covers code the PR lacks.
   - **Someone else's PR** (`author.login` other than `gh api user --jq .login`, `author.username` other than `glab api user`'s `username`, compared case-insensitively): stop after the push and leave the title and body as they are.
   - **Rewrite from scratch**: `git fetch origin <base>` for the PR's own base (`baseRefName` / `target_branch`), then draft title and body as point 5 would for a new PR, from `git diff origin/<base>...HEAD`, the task and the plan, never patching the old body ("also adds…").
     The title keeps its task id.
     Other tasks it names resolve through `f10 task ref <id>... --fresh --md`, so the PR merges with their current titles and urls.
   - **Owned sections**: a section the overlay names as maintained elsewhere (a screenshots section a script regenerates) is copied verbatim from the old body into its place in the new one; one the old body lacks stays out.
     The old body is read for these sections only.
   - **The state block** (`docs/traces.md`): with plain mechanics, run `f10 trace state --step pr --body-file <file>` on the draft before writing it, and the block and its counts come over from the old body.
     A skill adapter takes no draft: there the block is an owned section, copied verbatim whatever the switch, and `f10 trace state --step pr` runs after the skill's write.
     With `traces: off` no block is written, and one the old body holds comes over as it is; a non-zero exit is `partial` (`conventions/failure.md` rule 7), never a failed step.
   - **Write it** through the project's PR adapter: a skill adapter refreshes through that skill, and one with no way to refresh fails the step (`conventions/failure.md` rule 4).
     Plain mechanics: `gh pr edit <n> --title … --body-file <file>` / `glab mr update <n> --title … --description "$(cat <file>)"`, the draft in a temp file outside the repo.
     The base, labels and assignee stay as they are.
7. **Never merge** unless project.md explicitly says otherwise; merging is a human's action.
8. **Report** per `conventions/report.md`: a `branch` row and a `pr` row (`mr` on GitLab) carrying the url, then one line saying whether the PR was opened, refreshed, or only pushed to.

**On failure:** the PR adapter errors: report whether the branch was pushed (`conventions/failure.md`); a refresh that fails after its push leaves the commits on the PR under a stale description, said so.
