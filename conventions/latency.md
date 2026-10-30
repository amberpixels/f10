# Convention · latency - what a run costs

**A run's cost is its turns, not its commands.**
A turn is one round trip: a message of tool calls, then their results.
One measured `/f10:ship` over already-written code took 202s: ~50s of commands, ~150s in fifteen turns of ~10s each.

1. **Independent calls go in one message.**
   A call that does not read another's output never waits for it.
2. **A badge never gets a turn of its own.**
   Join `f10-state.sh` to the step's first real command with `;`, or send it beside a `Read`.
3. **A mechanical sequence is one call.**
   Branch, format, commit, fetch, rebase and push run as one `Bash` call.
   Split only where you would read the output before choosing what comes next.
4. **A numbered procedure is a spec, not a turn budget.**
   Numbered items in a step file or a project adapter fix what happens and in what order.
   They never fix how many messages it takes, so collapse consecutive mechanical items, and still run every one.
5. **Read wide once.**
   Send a few targeted `Read` and `Grep` calls in one message, not a sequence that narrows down.
   Never send broad parallel `Explore` agents, because reconciling their contradictory summaries costs more turns than they save.

**Batching never licenses skipping.**
Never drop or reorder a command, and never substitute a tool for a declared adapter (`conventions/failure.md` rule 4).
Two kinds of call travel alone:

- A command whose output decides what comes next.
- Anything outward-facing (`pr`, `push`, `deploy`), which never shares a call with something that could have vetoed it.

Correctness beats latency.
