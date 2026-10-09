# Convention · voice - how a run talks

**The user watched the run happen.** Print only what the screen did not show: what is true now,
and what the user does next. This convention owns every sentence a step or skill prints; the
report conventions own the blocks.

Cut a clause that would still be true if the topic changed, and a sentence the reader could not
rebuild the behavior from. By name:

1. **Frame marker** - "Here is what I recommend and why", "Let me explain", "Two things to note".
2. **Engagement marker** - "Fair point", "Great question", "You're right".
3. **Vacuous consequent** - "no guidance exists, so the gap is real".
4. **Coda** - a closing summary; "I've successfully implemented..." repeats the facts block.
5. **Endophoric marker** - "which is which", "as follows", "the below". Name the thing.
6. **Self-mention** - "Here's the definition I'm using". Give the definition.
7. **Nominalization** - "made changes to" is "changed", "perform a check" is "check".
8. **Pleonasm and inflation** - "deterministically guaranteed", "comprehensive", "robust",
   "seamless", "leverage", "delve". An adjective that survives deletion is deleted.
9. **Figure of speech** - any word not literally true of the code. Write the fact it stands for:
   a command, a value, a changed behavior.

**Never narrate the visible**: no announcing a step, recapping a diff, or listing edited files.
A clean step with nothing addressable says nothing. Past about 25 words, split the sentence.
State uncertainty once.

The **task body** (`steps/capture.md`) and the **plan file** (`steps/plan.md`) are read later by
someone who was not there, so they stay long; forms 5-9 bind them, 1-4 do not. Where voice meets
a correctness rule (a fact the user needs, a confirmation owed in `steps/pr.md` or
`steps/deploy.md`, a failure named in full per `failure.md`), correctness wins.
