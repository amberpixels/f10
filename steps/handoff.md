# Step · handoff - what a change leaves for whoever ships it

Role: a **senior engineer** who will ship this change and was not there when it was written.
Input: a change, usually the current branch or an open PR, with its saved findings and verdicts.
Output: the **handoff file**, `<storage root>/handoffs/<key>.md`: what the code cannot do for itself once merged.
Context: the `handoff` overlay (`.f10/instructions/handoff.md`), which says where the project keeps its data changes, jobs and settings.

The handoff gives no verdict and holds no merge, because review and CI decide whether a change is safe.
It lists the work around the merge, so the person shipping it knows what to run, set or warn about.

1. **Gather the subject.**
   Run `f10 pr view [<subject>]`, which gives the branch, the base and the task.
   The subject is a PR number or url, a task id, or nothing for the current branch.
   - `<key>` is the task id, or, for a branch with no task id, the branch name with each `/` as `-`, the name `f10 review round` gives its findings directory.
   - `head` is `git rev-parse HEAD` for the branch checked out here, else the open PR's head sha.
2. **Reuse a current file.**
   Reuse `<storage root>/handoffs/<key>.md` when all of these hold, then print it as point 6 says and end the step:
   - Its `head` equals this head.
   - The working tree is clean, because the diff includes uncommitted work that no sha records.
   - No findings or verdict file that point 3 reads changed after its `written` time, because a resolve that only skips, or a judge run, changes Debt in prod without a commit.

   Otherwise write the file again.
   A dry run's `would produce` row says `write or reuse <path>`.
3. **Read the change.**
   Read the full diff from `f10 pr diff [<subject>]`, never a summary of it.
   Read the findings under `<storage root>/reviews/<key>/`, when they exist.
   Read the verdict files under `<storage root>/verdicts/<key>/` that changed after the committer date of `git merge-base <head> <base>`, with `head` from point 1.
   An older verdict judged an earlier PR on the same task, because the judge never archives its files per PR.
4. **Classify.**
   An item is anything that touches prod state or a consumer, even when it runs by itself, such as a migration.
   A pure code change is never an item.
   The groups, in this order:
   - **Data** - schema changes, backfills, data fixes.
   - **Run by hand** - jobs, rake tasks or scripts someone must run, and when.
   - **Config** - settings, env vars, secrets and flags, each with when to set it: before the deploy or after.
   - **Deploy order** - what must ship or run before what, across services or steps.
   - **Breaks for consumers** - a removed or renamed API field, CLI flag, exported symbol, event or file format that someone else reads.
   - **Debt in prod** - a finding whose verdict is `skip` because the cost was accepted, or a yellow verdict's change that its `decision:` did not build or fold, each only when it has a consequence in prod.
     A finding skipped because its code is gone is not debt.

   Without an overlay, look where the stack usually keeps these: migration directories, env and config files, deploy manifests, and the public surface the diff removes or renames.
   The overlay's locations come first, and a category it adds goes where it says, else after the built-in groups.
   Leave out a group with no item.
5. **Write the file** to `<storage root>/handoffs/<key>.md`, creating `handoffs/` if it is missing.
   The file opens with frontmatter of plain `key: value` lines, so a tool can read them without a YAML parser:

   ```
   ---
   task: FT-12
   head: <full sha>
   written: 2026-11-04T10:15:00Z
   ---
   ```

   The body has a `## <Group>` heading for each group that has items, and a `- ` line per item.
   An item is one line that names the thing and why it exists ("adds `users.locale` for per-user date formatting").
   A backfill or a job gets 2-3 lines: what it does, to which records, and when to run it.
   The body runs 2-3 lines for a typical change and 5-10 for a big one, always under a screen.
   With nothing to report, the body is the single line `Nothing to do after merge: no data, config or consumer changes.`
   The body is written for text that leaves the machine, so it follows Stealth mode and Task references (`conventions/context.md`).
   `f10 finish` reads the file before it merges, prints the body under its report and archives it, so keep the frontmatter in this shape.
6. **Report** with the rows `task`, `title`, `url` and `handoff` (the file's path), then the body beneath the block.
   In a ship flow the entry adds its `handoff` row to the final report and never replaces the shipment's rows.

**On failure:** no base can be found, `f10 pr diff` or `f10 pr view` errors, or the file cannot be written (`conventions/failure.md`).
A failed write puts the drafted body in the report, with the path it was meant for.
