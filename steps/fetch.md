# Step · fetch - read a task

Input: a task id in the project's format, or a bundle.
Context: `project.md → Tracker` (the **fetch adapter**).
Badge: `f10-state.sh set plan running` on entry, `f10-state.sh task <id> <url>` once read.

1. **Read the task** with `f10 task read <id>`.
   When it cannot read this tracker, it says so, and the project's fetch adapter reads the task instead.
2. **A bundle** is several tasks shipped in one PR, their ids joined by `+` (`ABC-1+ABC-2`).
   The first id is the **lead**, which names the plan, the branch and the verdicts.
   The others are its **riders**.
   The bundle is the argument, else what `f10 task current` prints, and `f10 task read` reads every member.
3. **Pass on what the task states**:
   - the **base task**, which the steering names (`f10 start --after`) or the body's last line `After: <id>` gives
   - the **kind**: the steering's word (`as a bug`) wins over a label, which maps as follows
     - `bug`, `defect` or `regression` is a bug
     - `refactor` or `tech-debt` is a refactor
     - `spike`, `research` or `investigation` is a spike
     - any other label, `question` included, names no kind

**On failure:** the tracker does not know the id, or does not answer (`conventions/failure.md`).
