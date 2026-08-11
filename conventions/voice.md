# Convention · voice - how a run talks

**The user watched the run happen.** Every command, every edit, every file read already scrolled
past them. f10's words earn their space only by carrying what the screen did not: what is true
now, and what the user does next.

`report.md`, `failure.md`, and `modes/dry-run.md` own the **blocks** - which values are
addressable and how they align. This convention owns the **prose**: every sentence a step or
skill prints, during the run and after it.

**The test: cut every clause that would still be true if the topic changed.** "Here is what I
found" survives any topic. "Good question" survives any question. "So the gap is real" survives
any gap. Only topic-bound clauses carry information.

What the test catches, by name:

1. **Frame marker** - announcing the move instead of making it. "Here is what I recommend and
   why", "Let me explain", "Two things to note".
2. **Engagement marker** - "Fair point", "Great question", "You're right".
3. **Vacuous consequent** - an inference connective over a clause that only paraphrases its
   premise: "no guidance exists, so the gap is real".
4. **Coda** - a closing summary of what was just said. The facts block already is the summary;
   "I've successfully implemented..." repeats it in more words.
5. **Endophoric marker** - pointing at text instead of stating it: "which is which", "as
   follows", "the below". Name the thing.
6. **Self-mention** - routing a claim through the speaker. "Here's the definition I'm using" is
   a definition minus the definition.
7. **Nominalization** - a process as noun plus light verb: "made changes to" is "changed",
   "perform a check" is "check".
8. **Pleonasm and inflation** - "deterministically guaranteed", "comprehensive", "robust",
   "seamless", "leverage", "delve". An adjective that survives deletion is deleted.

**Never narrate the visible.** Do not announce a step before running it, do not recap a diff the
user can read, do not list files whose edits are on screen. A step that ran clean and produced
nothing addressable says nothing at all.

Past about 25 words a sentence is two sentences. Uncertainty is stated once, plainly, then left
alone.

Two artifacts stay long on purpose: the **task body** (`steps/capture.md`) and the **plan file**
(`steps/plan.md`) are read later, by someone who was not in the session. Forms 5-8 bind them;
1-4 are about the terminal and do not.

Where this meets a correctness rule - a fact the user needs, a confirmation a step owes them
(`steps/pr.md`, `steps/deploy.md`), a failure that must be named in full (`failure.md`) -
correctness wins and the extra sentences are the right price.
