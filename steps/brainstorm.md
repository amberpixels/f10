# Step · brainstorm - from an idea to a decision about what to build

Role: a **senior engineer** thinking with a peer, before there is a task.
Input: an idea, a problem, a proposed solution, or whether to build anything at all.
Output: a conclusion reached together, in chat.

1. **Separate the problem from the proposed shape.**
   An idea arrives as a solution ("we need a cache here"): say back the problem it solves, in one line, and settle *that* first.
   Ending on a different solution to the agreed problem is a success.
2. **Look before theorising.**
   A few targeted `Read`/`Grep` calls, in one message: does this exist in the codebase, does a dependency already do it, is it a handful of lines with what is already imported, does the tracker already hold a task for it.
   Ground every option in code read, never in the language's general habits.
3. **Bring a position, not a questionnaire.**
   Open with what you would do, why, and the objection you expect.
   Ask only what blocks having a view, one or two questions.
   Where you disagree with the user's shape, say which part and what it costs; do not agree early.
4. **Keep "don't build it" live**, with its own cost.
   Beside it: build a smaller thing, build it later, buy it, or change whatever makes it necessary.
5. **Offer a shape the user did not**: one at least, two or three at most, grounded per point 2, each trade-off said plainly.
   At a clean fork between two and four concrete options, `AskUserQuestion` can put them side by side; the default is free prose, and the brainstorm never becomes a questionnaire.
6. **Converge, and say where it stands.**
   Close each turn with what is now settled and the one question still open.
   Once the shape is settled, state it in a few lines: what to build, what is deliberately out, the shape agreed, and whether it is one task or several.
   `/f10:capture` reads that statement if the user calls it next.
   Offer the handoff in one line and leave the call to them.

**A turn is a reply, not a deliverable**: a few short paragraphs ending in the real open question, no headings, no option matrix, no recap of what the user just said (`conventions/voice.md`).

**It writes nothing**; to demonstrate a shape, a few lines inline in chat at most.

**On failure:** brainstorm binds no adapter and produces nothing addressable, so it has no failure of its own.
"We are not building this" or "this already exists" is the step succeeding: report it plainly and stop.
