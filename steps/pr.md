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
   A run with no task id (a planless pipeline, free-text work) names its branch however the project does.
3. **Open or refresh.**
   `gh pr view --json state,number,url,title,body,author,baseRefName` / `glab mr view -F json` (`state`, `iid`, `web_url`, `title`, `description`, `author.username`, `target_branch`) on the current branch.
   Only an open PR counts (`OPEN`, `opened` on GitLab): one merged or closed, or none, means open a new one (points 4 and 5); an open one means refresh it (point 6).
4. **A recorded dependency sets the base.**
   Where `f10 start --after` recorded one on the branch (`git config --get branch."$(git branch --show-current)".f10-after-branch`), the PR targets that branch (`gh pr create --base <branch>` / `glab mr create --target-branch <branch>`), so it is stacked and the host retargets it when the base merges.
   The base must already be on origin; when it is not, stop and report (`conventions/failure.md`) with the branch named and `next: push the base task's branch`.
   One exception: a base that exists neither locally nor on origin was finished and deleted, so its code is in the default branch: `git config --unset` both keys and open against the default branch.
5. **Open it** via the project's PR adapter: a skill, or plain `gh pr create` / `glab mr create` mechanics (commit, branch, push, labels/assignee per project.md).
   In stealth mode, no f10 traces in the branch name, commits, or PR text (`conventions/context.md`).
   Another task named in the PR text is a link with its title (`conventions/context.md` → Task references).
6. **Refresh it**, so a later `pr` entry brings the description up to what review, resolve or judge changed.
   - **Push first**: commit what the tree holds per the adapter, then push, never forcing, so the description never covers code the PR lacks.
   - **Someone else's PR** (`author.login` other than `gh api user --jq .login`, `author.username` other than `glab api user`'s `username`, compared case-insensitively): stop after the push and leave the title and body as they are.
   - **Rewrite from scratch**: `git fetch origin <base>` for the PR's own base (`baseRefName` / `target_branch`), then draft title and body as point 5 would for a new PR, from `git diff origin/<base>...HEAD`, the task and the plan, never patching the old body ("also adds…").
     The title keeps its task id.
   - **Owned sections**: a section the overlay names as maintained elsewhere (a screenshots section a script regenerates) is copied verbatim from the old body into its place in the new one; one the old body lacks stays out.
     The old body is read for these sections only.
   - **Write it** through the project's PR adapter: a skill adapter refreshes through that skill, and one with no way to refresh fails the step (`conventions/failure.md` rule 4).
     Plain mechanics: `gh pr edit <n> --title … --body-file <file>` / `glab mr update <n> --title … --description "$(cat <file>)"`, the draft in a temp file outside the repo.
     The base, labels and assignee stay as they are.
7. **Never merge** unless project.md explicitly says otherwise; merging is a human's action.
8. **Report** per `conventions/report.md`: a `branch` row and a `pr` row (`mr` on GitLab) carrying the url, then one line saying whether the PR was opened, refreshed, or only pushed to.

**On failure:** the PR adapter errors: report whether the branch was pushed (`conventions/failure.md`); a refresh that fails after its push leaves the commits on the PR under a stale description, said so.
