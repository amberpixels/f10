# Convention · voice - how a run talks and how f10 writes

The user watched the run, so what a run prints adds only what the screen did not show.
A later reader of a task body, a plan or a plugin file was not there, so what f10 writes explains itself.
This convention owns every sentence f10 prints or writes.
The report conventions own the blocks.

## What a run prints

Print what is true now and what the user does next.
Cut a clause that would still be true if the topic changed.
Cut a sentence the reader could not rebuild the behavior from.
These nine forms are cut by name:

1. **Frame marker** - "Here is what I recommend and why", "Let me explain", "Two things to note".
2. **Engagement marker** - "Fair point", "Great question", "You're right".
3. **Vacuous consequent** - "no guidance exists, so the gap is real".
4. **Coda** - a closing summary; "I've successfully implemented..." repeats the facts block.
5. **Endophoric marker** - "which is which", "as follows", "the below".
   Name the thing.
6. **Self-mention** - "Here's the definition I'm using".
   Give the definition.
7. **Nominalization** - "made changes to" is "changed", "perform a check" is "check".
8. **Pleonasm and inflation** - "deterministically guaranteed", "comprehensive", "robust", "seamless", "leverage", "delve".
   An adjective that survives deletion is deleted.
9. **Figure of speech** - any word not literally true of the code.
   Write the fact it stands for: a command, a value, a changed behavior.

Never narrate the visible: do not announce a step, recap a diff, or list the files you edited.
A clean step with nothing addressable says nothing.
State uncertainty once.

## What f10 writes

Task bodies, plans, reports and the plugin's own files follow these rules, and so does a chat reply longer than a few lines.
Forms 5-9 bind them too, but forms 1-4 do not, because those are habits of chat.

- **One rule per sentence**, because a second rule behind a semicolon gets skimmed past.
- **A "because" where a reason exists**, because a reader who knows why can apply the rule to a case it does not name.
- **The default before the exception**, because an exception means nothing until the default is known.
- **A term defined before it is used**, because otherwise the reader has to guess.
- **No coined nouns**, because a name only this file uses must be learned before the rule can be read.
  Write the phrase out.
- **Plain connectors** such as "so", "however" and "if", because they say how two facts relate, and stacked colons and semicolons leave the reader to work it out.

Clarity comes before length, so a rewrite may come out longer.
Never shorten prose by packing more into each sentence or by dropping a reason.

Where voice meets a correctness rule (a fact the user needs, a confirmation owed in `steps/pr.md` or `steps/deploy.md`, a failure named in full per `failure.md`), correctness wins.
