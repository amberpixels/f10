# Convention · gaps - open decisions that need the user

A **gap** is a fork the user decides, because it changes scope or is hard to reverse.
Settle everything else yourself, about 90% of the questions (`steps/fetch.md`).
A gap never blocks the work, because the plan ships on its defaults.
Filling gaps is optional at any stage.

The Gaps section is the last section of the plan file (`<storage root>/plans/<TASK-ID>.md`), in prose with no code:

```
## Gaps - need your call
_The plan proceeds on the **default** for each open gap unless you answer otherwise._

1. **<short title>** - <the question, one line>.
   Why it matters: <one line>.
   Default: <what the plan does if you don't answer>. - **Open**
```

With no real forks, the section is `## Gaps - none`.

**Filling** is one `AskUserQuestion`, a question per open gap, each with the default first and recommended.
Never ask one gap at a time across turns.
Offer it, and let the user decline, at these points:

- After `/f10:plan` saves the file, listing the open gaps by title.
- Before `/f10:ship` implements: fill now, or proceed on the defaults, which you name.
- Whenever the user asks.

A driven run never offers it: each gap stays on its default, and the report lists it (`modes/driven.md`).

**On answers**, mark each gap resolved in place and keep it in the file:

```
1. **<short title>** - … Default: <…>. - **Resolved: <the answer>**
```

Fold each decision into the stages, and into Consequences where it changes a cost.
Then re-save the plan file.
If an answer changes a default the code already built on, say so, because that is a redesign pass, not a silent edit.
