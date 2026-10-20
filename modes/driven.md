# Mode · driven - a run prompted from another session

A **driven** run has nobody watching its pane: `f10 start`, `f10 forward` or `f10 drive` prompted it from another session.
A question asked here stalls until someone finds it, so driven mode mostly changes **how questions travel.**
Steps, plan, pipeline and reports stay the same.

**Activation:** the token **`--driven`** anywhere in the argument.
Strip it and read the argument as normal.
It holds for the whole run: a skill that chains into another (`/f10:plan ... && /f10:ship ...`) passes it on.

A step with a **Driven** paragraph (judge, catchup) adds to this file for that step; follow both.

## No interactive question, ever

Never open `AskUserQuestion`, never end a turn on a question in chat.
Every question the steps call for (**wait or proceed** on a base with no code, a judge's rethink, a resolve `ask`, a conflict catchup cannot settle, a deploy confirmation) goes through run state:

1. **The whole batch as one ask**: `f10-state.sh ask "<text>"`, one line, numbered questions, options in brackets, the default or recommended answer first: `1. Reuse the saved plan? [reuse | re-plan] 2. Base GH-7 has no code yet: [wait | proceed on its plan]`.
2. **Stop blocked, in the same call**: `; f10-state.sh set <phase> blocked [<leaf>]` (for ship, the pipeline step as leaf).
   The `Stop` hook leaves `blocked` alone.
3. **End the turn**, printing the ask once under a facts block carrying the task.
   The print is for a person looking at this pane: end it with "Answer here", never a command for another session (`f10 forward`, `f10 status`, `f10 drive --answer`).
   The driving session reads the ask through `f10 status <task-id>` and knows its own way back.

Two questions never become asks: a driven run takes every gap on its default, listing the open ones in its report (`conventions/gaps.md`), and ships a saved plan as it stands rather than asking **reuse or re-plan**.

## The answer

Answers arrive as your **next prompt**, plain text, numbered as the ask was, typed in this pane or sent from the driving session.
Run `f10-state.sh set <phase> running [<leaf>]`, which clears the ask; fold the answers in as `conventions/gaps.md` does (gaps **Resolved** in the plan file, decisions into the stages, and Consequences where they change a cost); resume from the step that stopped.
An answer with no ask recorded is steering.

A **Claude Code permission dialog** cannot travel this way: the driving session sees an idle agent.
Stay inside the session's existing permissions; an unavoidable dialog is answered in this pane.

`--dry-run` with `--driven` is a dry run that also lists which questions would become asks.
