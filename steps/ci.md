# Step · ci - triage a red CI run on the task's PR

Role: a **senior engineer on call for the change's CI**, deciding whether the change broke the build, the host flaked, or the default branch is broken.
Input: the open PR of the branch checked out here, whose head reads red.
Output: one of a fix round pushed, the failed jobs rerun, or an ask.
Context: the loader's `verify:` line, `project.md → Hosting & PR`, and the optional `ci` overlay (below).
Badge: none, because inside ship the step runs under the badge of the entry it runs before.
Sent on its own by `/f10:ci`, it ends with `f10-state.sh done ci`, which is not a badge: it is the end signal `f10 drive` reads, as catchup's is.

No flow names `ci`.
The step runs when one of three callers reads red checks on the PR:

- **Ship** reads the checks at each entry boundary once the PR is open (`skills/ship/SKILL.md` → Run), and at its Exit.
- **`f10 drive`** reads them on its tick, and sends `/f10:ci --driven` to the task's agent.
- **The watcher** reports a red PR once, and sends `/f10:ci --driven` only under `watch_ci: on`.

**The `ci` overlay** (`.f10/instructions/ci.md`) is optional.
It may name jobs that are known to flake, which are always rerun and never fixed.
It may also name jobs never to fix, such as a deploy preview or a licence scan, which are always an ask.
A job the overlay names overrides the table in point 3.

**The triage record.**
f10 keeps what this step tried as a git config key on the branch, `branch.<branch>.f10-ci`, which never leaves the machine.
A fix round counts for the whole branch, because it pushes a new head, and a limit kept per head would let the next red head be fixed again.
A rerun counts for its head, because a rerun leaves the head where it was.
Any read that finds the head green clears the record, so the limits hold for one red spell.
`f10 pr checks --record fix` and `f10 pr checks --rerun` write it, and refuse an action already spent.

1. **Read what the decision needs**, in one call: `f10 pr checks --triage`.
   - Green or pending means the head moved on since the caller looked.
     Say so in one line and end the step, because pending waits for no one but finish.
   - Red prints `first` (the first failed job that was not cancelled, and its run), `default` (how that job last ended on the default branch), and `tried` (this red spell's record).
2. **Bring the base in first** when the branch is behind or conflicts with it.
   Run the **catchup** step's **As a guard** with `f10 pr catchup --guard pr`, as before a `pr` entry.
   A guard that integrates pushes the result, never forcing, then ends this step, because the new head runs CI again on code that includes the base.
3. **Find the cause.**
   Read the `first` job's log with `f10 pr checks --log`, never a cancelled job's, because a cancelled job only stopped after another failed.
   Then run verify locally.
   The two facts decide:

   | local verify | `default` | what the step does |
   |---|---|---|
   | red | any | the change broke it: one fix round (point 4) |
   | green | green, or `none` | a flake or the host's trouble: `f10 pr checks --rerun`, once |
   | green | red | the default branch is broken: an ask, at every pace, and never a fix |

   `default: none` means the job has no finished run on the default branch, such as a new or renamed job.
   It counts as green, because a rerun costs little and a second red ends in an ask anyway.
   A log that shows a host failure (a timeout or a 5xx while downloading tools) while verify is red still follows the table: a red verify is the change's own.
4. **The fix round.**
   - If `tried` already holds a fix, this red spell has had its fix round, so go to point 5 instead.
   - Fix the cause the log and verify show, in the house style, then run verify until it is green.
   - Run `f10 pr checks --record fix`, then commit as `conventions/context.md` → Commits says, with `--step ci`.
     The commit message names the failing job and the cause in plain words.
   - Push, never forcing.
     When ship runs this step before an entry that pushes (`pr`, `push`, or a judge after `pr`), leave the push to that entry, so the fix goes out with it.
   - Red verify that cannot be fixed fails the step (`conventions/failure.md`), with the work left uncommitted.
5. **Ask** when the table says so, or when the action it picks is already in `tried`: red after a fix round, or red again after a rerun of the same head.
   The question names the job, its log url, the local verify result, the default branch's result, and what was tried.
   The options, recommended first, are:
   - after a spent fix or rerun: **fix again**, **rerun once more**, or **leave it red**.
   - with the default branch red: **leave it red** (the default branch needs its own fix), **fix it in this PR**, or **rerun once more**.

   Interactive, it is one `AskUserQuestion`.
   Driven, it is one ask, `f10-state.sh ask "<text>"; f10-state.sh set ship blocked ci` (`modes/driven.md`), with no `done ci`, because the run waits on the answer.
   Ship mid-flow takes the default branch's case differently: it notes it with `f10-state.sh note` and goes on, and the question is asked at the end of the run.
   **Fix again** and **rerun once more** are the user's override of the record: the step runs `git config --unset branch.<branch>.f10-ci`, then the point the answer names.

**Report**: one line, the outcome and the job: `ci: lint failed on the change - fixed in 3f2a1c9, pushed`, `ci: test flaked - reran run 77`, or the ask.
A facts block carries a `commit` row when the step committed.

**On failure:** an `f10 pr checks` error, a rejected push, or red verify that cannot be fixed (`conventions/failure.md`).
A rejected push leaves the commit local, badge `partial` on `ci`.
Never force-push to clear it.
