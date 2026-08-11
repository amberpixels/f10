# Convention · latency - what a run costs

**The unit of cost is the turn, not the command.** A measured `/f10:ship` over already-written
code took 202s: ~50s of commands, ~150s in fifteen round trips at ~10s each.

1. **Independent calls go in one message.** Calls in one message run together; the same calls
   spread over five messages cost five round trips. A call that does not read another call's
   output never waits for it.
2. **A badge never earns a turn of its own.** `f10-state.sh` is cosmetic
   (`conventions/report.md`). Join it to the step's first real command with `;`, or send it
   beside a `Read`. A message carrying only a badge call buys nothing.
3. **A mechanical sequence is one call.** Branch, format, commit, fetch, rebase, push: six
   commands, no decision between them, one `Bash` call. Split only where you would read the
   output before choosing what comes next.
4. **A numbered procedure is a spec, not a turn budget.** Numbered items in a step file or a
   project adapter say what must happen and in what order, never how many messages to spend.
   Consecutive mechanical items collapse into one call. Every one of them still runs.
5. **Read wide once.** A few targeted `Read`/`Grep` calls in one message beat a sequence of
   narrowing ones. They also beat broad parallel `Explore` agents, whose contradictory summaries
   cost more turns to reconcile than they save (`steps/fetch.md`).

**Batching is never a licence to skip.** Nothing here permits dropping a command, reordering one,
or substituting a different tool for a declared adapter (`conventions/context.md`,
`conventions/failure.md` rule 4).

Two things always travel alone: a command whose output changes what you do next, and anything
outward-facing (`pr`, `push`, `deploy`), which must never share a call with something that could
have vetoed it.

Where this cost rule meets a correctness rule, correctness wins and the extra turn is the right
price.
