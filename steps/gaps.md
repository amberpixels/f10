# Convention · gaps - open decisions that need the user

A **gap** is a genuine fork that needs *the user's* call - one that changes scope or is hard to
reverse - **not** something you should resolve yourself. You still decide ~90% from the code and
conventions (see `fetch.md`); gaps are only the real forks left over. Don't manufacture gaps out of
questions you can answer, and don't nickel-and-dime.

Gaps are **recorded, not blocking.** The plan is always complete and shippable on its noted
defaults; filling gaps is always optional and can happen at any stage.

## Where gaps live

In the plan file (`.f10/plans/<TASK-ID>.md`), as a section at the end. Keep it lean - same
altitude as the rest of the plan (prose, no code):

```
## Gaps - need your call
_The plan proceeds on the **default** for each open gap unless you answer otherwise._

1. **<short title>** - <the question, one line>.
   Why it matters: <one line>.
   Default: <what the plan does if you don't answer>. - **Open**
```

When a gap is answered, flip its status in place and fold the decision into the plan body:

```
1. **<short title>** - … Default: <…>. - **Resolved: <the answer>**
```

If there are no real forks, write `## Gaps - none` and move on. Don't pad the list.

## Filling gaps (the questionnaire)

To fill, use **`AskUserQuestion`** - batch all open gaps into one questionnaire (one question per
gap, with the default as the first / recommended option). Never ask them one-at-a-time across turns.

Offer to fill at any of these checkpoints - always as an offer the user can decline:
- **After planning** (`/f10:plan`): once the file is saved, list the open gaps by title in one line
  each and ask whether to fill them now.
- **Before implementing** (`/f10:ship`): if the loaded plan has open gaps, surface them and ask
  *fill now, or proceed on the defaults?* Proceeding on defaults is a valid choice - say which
  defaults you'll use.
- **On demand:** the user can ask to fill gaps at any point.

When answers come in: update each gap's status to **Resolved**, fold the decision into the relevant
plan steps, and re-save the plan file. If an answer **differs from the default the code was already
built on** (i.e. implementation happened first), say so plainly - that's a redesign/refactor pass,
not a silent edit. Leave resolved gaps in the file so the decisions stay auditable and revisitable.
