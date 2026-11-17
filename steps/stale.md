# Step · stale - judge an old task before it is planned

The investigate step runs this when the frontmatter declares `stale_after` (`10d`, `3w`), because the work an old task asks for may no longer make sense.

1. **Check** with `f10 task stale <lead id> --created <date>`, the date being the created date the read showed, else no flag.
   - `fresh`: go on.
   - `undated`, or a non-zero exit: say so in the brief and go on, without asking the tracker again for a date.
   - `judged <path>`: a verdict covers the task, so name its file in the brief and go on.
   - `needs judge`: run `f10-state.sh set plan running judge; f10-load-project.sh judge`, then the **judge** step on `task <id>` (its **Before planning**).
2. **Proceed** runs `f10-state.sh set plan running`, and the brief and the final report carry the verdict in one line.
3. **Any other verdict**, yellow included, stops the run blocked.
   A driven run stops the same way, without an ask.
   Run `f10-state.sh set plan blocked judge; f10-state.sh note "stale task: <verdict> - <reason>" "<verdict file>; <the anyway command> to go on, or close the task"`.
   The anyway command is `/f10:ship <id> anyway` on a planless flow, because a plan file would take the task off it, and `/f10:plan <id> anyway` otherwise.
   Report the task's facts block with a `verdict` row, and stop: no plan, and no chained skill.

**Anyway** in the steering is the user's override.
Write it as a verdict file in `verdicts/<id>/<n>.md` under the storage root.
Its fields let later runs find it as `judged`:

- `task: <id>`
- `subject: task <id>`
- `verdict: proceed`
- `reason: planned anyway by the user`
- `run: before plan`
- `decision: overridden`

The brief says the user overrode the check.
