# Convention · gaps - open decisions that need the user

A **gap** is a fork that needs *the user's* call because it changes scope or is hard to reverse.
Settle everything else yourself (~90%, per `steps/fetch.md`). Gaps are **recorded, not
blocking**: the plan is shippable on its defaults, and filling gaps is optional at any stage.

The last section of the plan (`<storage root>/plans/<TASK-ID>.md`), prose, no code:

```
## Gaps - need your call
_The plan proceeds on the **default** for each open gap unless you answer otherwise._

1. **<short title>** - <the question, one line>.
   Why it matters: <one line>.
   Default: <what the plan does if you don't answer>. - **Open**
```

An answered gap flips in place, and the decision folds into the plan stages:

```
1. **<short title>** - … Default: <…>. - **Resolved: <the answer>**
```

No real forks: `## Gaps - none`. Don't pad the list.

**Filling** is one **`AskUserQuestion`**: a question per open gap, the default first and
recommended; never one at a time across turns. Offer it, declinable, after `/f10:plan` saves
the file (list open gaps by title), before `/f10:ship` implements (*fill now, or proceed on the
defaults?*, naming the defaults), and whenever the user asks. **Driven** runs send the batch as
one ask (`modes/driven.md`).

On answers: mark each **Resolved**, fold it into the stages, and re-save the plan file. If an
answer differs from a default the code already built on, say so: that is a redesign pass, not a
silent edit. Keep resolved gaps in the file.
