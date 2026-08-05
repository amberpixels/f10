# Convention · latency - what a run actually costs

A step's wall-clock is not its commands. A measured `/f10:ship` over code that was already
written took 202s: timing every command it ran accounts for ~50s, and the other ~150s was fifteen
agent round trips at roughly ten seconds each. **The unit of cost is the turn.** Everything below
follows from that one fact.

1. **Independent calls go in one message.** Tool calls in a single message run together; the same
   calls spread over five messages cost five round trips. Nothing that does not read another
   call's output has to wait for it.
2. **A badge never earns a turn of its own.** `f10-state.sh` is cosmetic
   (`conventions/report.md`) - join it to the step's first real command with `;`, or send it as a
   second call beside a `Read`. A message carrying only a badge call is a round trip spent on
   nothing.
3. **A mechanical sequence is one call.** Branch, format, commit, fetch, rebase, push: six
   commands, no decision between them, one `Bash` call. Split only where you would genuinely read
   the output before choosing what comes next.
4. **A numbered procedure is a spec, not a turn budget.** The numbered items in a step file, and
   in a project's adapter, say *what* must happen and *in what order* - never how many messages
   to spend. Consecutive mechanical items collapse into one call; every one of them still runs.
5. **Read wide once.** A few targeted `Read`/`Grep` calls in one message beat a sequence of
   narrowing ones - and beat broad parallel `Explore` agents, whose contradictory summaries cost
   more turns to reconcile than they saved (`steps/fetch.md`).

**Batching is never a licence to skip.** Nothing here permits dropping a command, reordering one,
or substituting a different tool for a declared adapter (`conventions/context.md`,
`conventions/failure.md` rule 4). Two things always stay on their own: a command whose output
changes what you do next, and anything outward-facing - `pr`, `push`, `deploy` - which must never
share a call with something that could have vetoed it.

This is a cost rule. Where it meets a correctness rule, correctness wins and the extra turn is
the right price.
