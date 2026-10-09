# Convention · latency - what a run costs

**The unit of cost is the turn, not the command.** A measured `/f10:ship` over already-written
code took 202s: ~50s of commands, ~150s in fifteen round trips at ~10s each.

1. **Independent calls go in one message.** A call that does not read another's output never
   waits for it.
2. **A badge never gets a turn of its own.** Join `f10-state.sh` to the step's first real
   command with `;`, or send it beside a `Read`.
3. **A mechanical sequence is one call.** Branch, format, commit, fetch, rebase, push: one `Bash`
   call. Split only where you would read the output before choosing what comes next.
4. **A numbered procedure is a spec, not a turn budget.** Numbered items in a step file or a
   project adapter fix what happens and in what order, never how many messages. Collapse
   consecutive mechanical items; every one still runs.
5. **Read wide once.** A few targeted `Read`/`Grep` calls in one message, not a narrowing
   sequence, and not broad parallel `Explore` agents, whose contradictory summaries cost more
   turns to reconcile than they save.

**Batching never licenses skipping**: never drop or reorder a command, or substitute a tool for a
declared adapter (`conventions/failure.md` rule 4). Two things travel alone: a command whose
output decides what comes next, and anything outward-facing (`pr`, `push`, `deploy`), which never
shares a call with something that could have vetoed it. Correctness beats latency.
