# Step · fetch - understand and investigate a task

Role: a **senior engineer** doing due diligence before touching any code.
Input: a task identifier (in the project's id format).
Context: `project.md → Tracker` (the **fetch adapter**), and the frontmatter's `stale_after`.
Badge: `f10-state.sh set plan running` on entry, `f10-state.sh task <id> <url>` once fetched.

The step ends in an **investigation brief** (point 6): a short write-up of what the task needs, which the plan step designs from.

1. **Fetch the task** with `f10 task read <id>`.
   If `f10 task read` cannot read this tracker, it says so, and the project's fetch adapter reads the task instead.
   The adapter is a skill to invoke or a CLI command, whichever `project.md` declares.

   **A bundle** is several tasks shipped as one, written as their ids joined by `+` with the lead first (`ABC-1+ABC-2`).
   The bundle is the argument when one was given.
   Otherwise it is the bundle `f10 task current` prints (`<lead>+<riders>`).
   `f10 task read <lead>+<riders>` reads every member.
   One kind covers the whole bundle, and a single `bug` member makes it a bug.
   The bundle's areas are the areas of all its members.

   **A base task** is a task this one builds on.
   The steering names it (`f10 start --after`), or the task body ends with a line `After: <id>`.
   Put the base task in the brief.
   The plan step records it as the plan's **Depends on** line.
   The plan step designs against the base task's plan, or against the base task itself when that plan is not written yet.
   It never designs against this checkout, and there is no fallback when the base task is absent.

   **A stale task** is one older than the frontmatter's `stale_after` (`10d`, `3w`).
   A stale task is judged before anything else, because the work it asks for may no longer make sense.
   The check runs only when `stale_after` is declared and this run will write a plan.
   A run writes a plan when the **plan** step follows, or when the flow is planless and the brief becomes its inline plan.
   A ship run that reuses a saved plan writes none, so it skips the check: look for the plan file first.
   - **Check** with `f10 task stale <id> --created <YYYY-MM-DD>`.
     It compares the task's age with `stale_after`, and looks for a recent proceed verdict on the task in every storage root that may hold one.
     The `--created` value is the created date that what was read showed: the `created` meta line of `f10 task read`, the driver's doc, or the fetch adapter's output.
     If nothing showed a created date, leave the flag out.
     A bundle is checked on its lead, because the lead's id keys the plan and the verdicts.
     The command prints one line:
     - `fresh`: nothing to do.
     - `undated`: the task is not judged.
       Say so in one line in the brief, and never call the adapter again to find a date.
     - `judged <path>`: a verdict already covers the task, so skip the judge and name the verdict file in one line.
     - `needs judge`: go to **Judge**.

     A non-zero exit counts as `undated`, and its one line also gives the error.
   - **Anyway**: the steering word `anyway` is the user's override of the check.
     It arrives as `/f10:plan <id> anyway`, `/f10:ship <id> anyway`, or `f10 drive --answer <id> anyway` sent to a blocked driven run.
     Write it as a verdict file like any other: `subject: task <id>`, `verdict: proceed`, `reason: planned anyway by the user`, `run: before plan`, `decision: overridden`.
     Later runs then skip the judge, by the `judged <path>` rule above.
     Say in one line in the brief that the user overrode the check.
   - **Judge**: run `f10-state.sh set plan running judge; f10-load-project.sh judge`.
     Then run the **judge** step on the subject `task <id>`, following its **Before planning** section.
   - **Proceed**: run `f10-state.sh set plan running` and go on to point 2.
     The brief carries the verdict in one line (`stale (created 2026-09-20, older than 3w): proceed - <reason>`), and the run's final report repeats it.
   - **Any other verdict**, yellow included, stops the run blocked.
     Run `f10-state.sh set plan blocked judge; f10-state.sh note "stale task: <verdict> - <reason>" "<verdict file path>; <the anyway command> to go on, or close the task"`.
     For a planless flow the anyway command is `/f10:ship <id> anyway`, because a plan file would take the task off that flow.
     For any other flow it is `/f10:plan <id> anyway`.
     Print the banner and a facts block (`task`, `title`, `url`, and `verdict` with the file's path), then stop.
     No plan is written, and a chained skill does not run.
     A driven run stops the same way, without an ask.
2. **Settle the kind**: `feature`, `bug`, `refactor` or `spike`.
   The first of these sources that names a kind wins:
   1. The steering's word, as in `/f10:plan FT-12 as a bug`.
   2. A tracker label or type that names a kind.
      `bug`, `defect` and `regression` mean a bug.
      `refactor` and `tech-debt` mean a refactor.
      `spike`, `research` and `investigation` mean a spike.
      Other labels, `question` among them, name no kind.
   3. The description.
      An error, a stack trace or "used to work" reads as a bug.
      "No behavior change" or "restructure" reads as a refactor.
   4. Otherwise, the kind is `feature`.

   If the description alone settled the kind, say so in the brief, so a reader can overrule it.
   The description never settles a spike, because a spike skips the PR.
   So a task that only asks a question ("find out whether", "is it feasible") stays a feature.
   The brief says that the task reads like a spike, so the user can re-plan it `as a spike`.
3. **Investigate only the real unknowns.**
   The task text and anything established in this conversation are ground truth.
   Never re-verify a premise the task or the user gave you.
   The one exception is a bug's reported behavior, which you reproduce (below).
   Investigate only what you need to design well: the exact code you will mirror or touch, existing patterns to reuse, callers, the downstream consumers behind a real decision, and the tests.
   Do it in a few targeted reads (`conventions/latency.md` rule 5).

   Each kind adds what the investigation must prove:
   - **bug** - reproduce it where the project can run it: a test, the CLI, or the verify commands.
     Record the steps or input, and the expected behavior against the actual one.
     Then trace the root cause to a `file:line`.
     If this run already confirmed the reproduction and the root cause (the debug step), do not redo them.
     Instead, check that the cited `file:line` still reads as the debug brief says, and carry both into this brief.
     A bug that does not reproduce never becomes a plan, and neither does one whose cause is still a guess after a few reads.
     Ask what the user saw, under which input and version, in one `AskUserQuestion` (driven: one ask, per `modes/driven.md`).
     Retry once with the answer.
     If it still does not reproduce, stop with `f10-state.sh set plan blocked` and a `note` naming what was tried and what to collect next.
   - **refactor** - find the behavior that must not change, the tests that pin it, and where coverage is missing.
   - **spike** - state the question so that an answer can be checked.
     Name the decision each answer feeds: the follow-up work that answer unlocks or kills.
     Find the cheapest experiment that answers the question.
     A question that cannot be stated that way is a gap, not a guess.
4. **Surface obstacles explicitly.**
   Look for a task that is out of date against the current code, requirements that conflict with each other, requirements that are ambiguous or missing, hidden coupling, and data or migration concerns.
5. **Decide like a senior engineer.**
   Settle ~90% of the open questions yourself, from the code and its house style.
   The major forks that remain become **gaps**.
   A fork is major when it changes scope or is hard to reverse.
   Note each gap with the default you would proceed on, and carry it into the plan's Gaps section (`conventions/gaps.md`).
6. **Write the investigation brief**, and keep it short.
   It holds:
   - the kind, with a bug's reproduction and root cause, a refactor's invariant, or a spike's question
   - what changes, and where
   - the **areas** the task really touches, and the conditional roles in `project.md → Roles` they trigger.
     If the task's _Areas_ note disagrees with the investigation, correct it.
     If the task carries no _Areas_ note, derive the areas from the code.
   - the base role each step runs under where Roles replaces one, and what set it (`conventions/context.md` → Roles)
   - the obstacles found (point 4)
   - the decisions you took (point 5)
   - the gaps, each with its default, for the plan to record

   A planless flow, one with no leading `plan` entry, has no plan step.
   There the brief becomes the inline plan `/f10:ship` prints in chat, with its gaps listed on their defaults.

**On failure:** the tracker does not know the id, or the tracker is unreachable (`conventions/failure.md`).
A bug fetch could not reproduce is not a failure: it is a declined outcome.
