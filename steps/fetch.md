# Step · fetch - understand and investigate a task

Role: a **senior engineer** doing due diligence before touching any code.
Input: a task identifier (in the project's id format).
Context: `project.md → Tracker` (the **fetch adapter**), and the frontmatter's `stale_after`.
Badge: `f10-state.sh set plan running` on entry, `f10-state.sh task <id> <url>` once fetched.

1. **Fetch the task**: `f10 task read <id>`; when it cannot read this tracker it says so, and the project's fetch adapter reads the task instead (a skill to invoke, or a CLI command such as `gh issue view <n> --comments`).
   A **bundle** (`ABC-1+ABC-2`, the lead first) is the argument, else the branch's task plus the riders `git config --get branch."$(git branch --show-current)".f10-tasks` lists; `f10 task read <lead>+<riders>` reads every member.
   One kind settles the whole bundle (a `bug` member makes it a bug), and its areas are every member's.
   A base task, named by the steering (`f10 start --after`) or a body line `After: <id>`, goes into the brief: the plan step records it as the plan's **Depends on** line and designs against the base task's plan (its task, where the plan is not written yet), never this checkout, with no fallback for its absence.
   **A stale task** is judged before anything else, when the frontmatter declares `stale_after` (`10d`, `3w`) and this run will write a plan: the **plan** step follows, or a planless flow's brief becomes its inline plan.
   A ship run that reuses a saved plan skips it: look for the plan file first.
   - **Check**: `f10 task stale <id>` judges the age against `stale_after` and looks for a recent proceed verdict on the task, in every storage root that may hold one.
     For a tracker that is not the host's issues, add `--created <YYYY-MM-DD>` with the created date the fetch adapter's output or the driver's doc showed; with none shown, call it without.
     It prints one line:
     `fresh`: nothing to do.
     `undated`: not judged, one line in the brief; never call the adapter again to find a date.
     `judged <path>`: skip the judge, one line naming the verdict file.
     `needs judge`: go to **Judge**.
   - **Anyway**: the steering word `anyway` (`/f10:plan <id> anyway`, `/f10:ship <id> anyway`, or `f10 drive --answer <id> anyway` to a blocked driven run) is the user's override.
     Write it as a verdict file like any other (`subject: task <id>`, `verdict: proceed`, `reason: planned anyway by the user`, `run: before plan`, `decision: overridden`), so later runs skip the judge by the rule above, and say so in one line in the brief.
   - **Judge**: `f10-state.sh set plan running judge; f10-load-project.sh judge`, then run the **judge** step on the subject `task <id>` (its **Before planning** section).
   - **Proceed**: `f10-state.sh set plan running` and go on to point 2; the brief carries the verdict in one line (`stale (created 2026-09-20, older than 3w): proceed - <reason>`), and the run's final report repeats it.
   - **Any other verdict**, yellow included: `f10-state.sh set plan blocked judge; f10-state.sh note "stale task: <verdict> - <reason>" "<verdict file path>; <the anyway command> to go on, or close the task"`, the command `/f10:ship <id> anyway` for a planless flow (a plan file would take the task off it), else `/f10:plan <id> anyway`.
     Print the banner and a facts block (`task`, `title`, `url`, `verdict` with the file's path), and stop: no plan is written and a chained skill does not run.
     A driven run stops the same way, with no ask.
2. **Settle the kind**: `feature`, `bug`, `refactor` or `spike`.
   The steering's word wins (`/f10:plan FT-12 as a bug`), then a tracker label or type that names one (`bug`, `defect`, `regression` are a bug; `refactor`, `tech-debt` a refactor; `spike`, `research`, `investigation` a spike; other labels, `question` among them, say nothing), then the description (an error, a stack trace, "used to work" read as a bug; "no behavior change", "restructure" as a refactor), else `feature`.
   A kind read from the description alone is named as such in the brief, so a reader can overrule it.
   The description never settles a spike, which skips the PR: a task that only asks a question ("find out whether", "is it feasible") stays a feature, and the brief says it reads like one, so the user can re-plan it `as a spike`.
3. **Investigate only the real unknowns.**
   The task text and anything established in this conversation are **ground truth**: never re-verify a premise the task or the user gave you, except a bug's reported behavior, which is reproduced below.
   Investigate only what you need to design well: the exact code you'll mirror or touch, existing patterns to reuse, callers, downstream consumers behind a real decision, and tests, in a few targeted reads (`conventions/latency.md` rule 5).
   The kind adds what it must prove:
   - **bug** - reproduce it where the project can run it (a test, the CLI, the verify commands): the steps or input, expected against actual.
     Then trace the root cause to a `file:line`.
     A reproduction and root cause this run already confirmed (the debug step) are not redone: check the cited `file:line` still reads as the brief says, and carry both into this brief.
     A bug that does not reproduce, or a cause still a guess after a few reads, never becomes a plan: ask what the user saw, under which input and version, in one `AskUserQuestion` (driven: one ask, per `modes/driven.md`), retry once with the answer, and if still not reproduced stop with `f10-state.sh set plan blocked` and a `note` naming what was tried and what to collect next.
   - **refactor** - the behavior that must not change, the tests that pin it, and where coverage is missing.
   - **spike** - the question, stated so an answer can be checked; the decision each answer feeds (the follow-up work it unlocks or kills); and the cheapest experiment that answers it.
     A question that cannot be stated that way is a gap, not a guess.
4. **Surface obstacles explicitly**: the task out of date against the current code, internal conflicts, ambiguous or missing requirements, hidden coupling, data/migration concerns.
5. **Decide like a senior engineer.**
   Settle ~90% of open questions yourself from the code and its house style.
   The _major_ forks left over (they change scope or are hard to reverse) become **gaps**: note each with the default you'd proceed on and carry them into the plan's Gaps section (`conventions/gaps.md`).
6. **Output** a short investigation brief: the kind (with a bug's reproduction and root cause, a refactor's invariant, or a spike's question); what changes, where; the **areas** the task really touches (correct the task's _Areas_ note if investigation disagrees, or derive them from the code when the task carries none) and the conditional roles in `project.md → Roles` they trigger; the base role each step runs under where Roles replaces one, and what set it (`conventions/context.md` → Roles); the obstacles found; the decisions you took; and the gaps with their defaults for the plan to record.
   A planless flow (no leading `plan` entry) has no plan step: the brief becomes the inline plan `/f10:ship` prints in chat, its gaps listed there on their defaults.

**On failure:** the tracker does not know the id, or is unreachable (`conventions/failure.md`).
A bug fetch could not reproduce is a declined outcome, not a failure.
