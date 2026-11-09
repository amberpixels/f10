# Mode · driven - a run prompted from another session

A **driven** run is one that another session prompted, through `f10 start`, `f10 forward` or `f10 drive`.
Nobody is watching its pane.
A question asked in that pane stalls until someone happens to find it, so driven mode mostly changes **how questions travel.**
The steps, the plan, the pipeline and the reports stay the same.

## Activation

The token **`--driven`** anywhere in the argument turns driven mode on.
Strip the token, then read the rest of the argument as usual.

Driven mode holds for the whole run.
If a skill chains into another (`/f10:plan ... && /f10:ship ...`), it passes `--driven` on to the next one.

These skills take the token: catchup, cleanup, debug, judge, plan, resolve, review, ship and standup.
Brainstorm, capture, demo and explain never see it, because nothing prompts them from another session.
A skill that only runs the binary (`modes/binary.md`) has no run to drive, so the token does not apply to it.

Some steps have a **Driven** paragraph of their own (judge, catchup).
That paragraph adds to this file for that step, so follow both.

## No interactive question, ever

In a driven run, never open `AskUserQuestion`.
Never end a turn on a question in chat.
Instead, every question the steps call for goes through run state.
Examples are a judge's rethink, a resolve `ask` and a deploy confirmation.

1. **Ask the whole batch as one ask.**
   Run `f10-state.sh ask "<text>"` with the text on one line.
   Number the questions, put each one's options in brackets, and list the default or recommended answer first: `1. Reuse the saved plan? [reuse | re-plan] 2. Deploy to staging? [yes | no]`.
2. **Stop blocked, in the same call.**
   Append `; f10-state.sh set <phase> blocked [<leaf>]`.
   For ship, the leaf is the flow entry that stopped.
   The `Stop` hook leaves a `blocked` state alone.
3. **End the turn.**
   Print the ask once, under a facts block that carries the task.
   The print is for a person looking at this pane, so end it with "Answer here".
   Never end it with a command meant for another session, such as `f10 forward`, `f10 status` or `f10 drive --answer`.
   The driving session reads the ask through `f10 status <task-id>` and already knows its own way back.

`f10-state.sh ask` also puts the ask in the user's asks inbox (`f10 asks`).
The inbox counts the ask on the status line, tells other sessions about it and notifies the user.

Four kinds of open question never become an ask:

- **A gap.** A driven run takes every gap on its default and lists the open ones in its report (`conventions/gaps.md`).
- **A conflict.** A driven catchup settles every conflict by the task's intent and writes each choice down (the **catchup** step's Driven paragraph).
- **A base with no code.** Ship stops with `f10-state.sh wait <base-id>` and asks nothing.
  `f10 drive` sends ship again once the base commits (`skills/ship/SKILL.md` → the catchup guard).
- **A stale task's verdict** (`steps/fetch.md` point 1).
  The run ends blocked and names the verdict file.
  The user decides either by sending `anyway`, as `f10 drive --answer <id> anyway` or typed in this pane, or by closing the task.

## A turn ends at its report, never before

The drive and the watcher read an idle agent as a turn that has ended.
So a turn that ends before its report looks like one that stopped without reporting.
After a minute's grace the drive reads where the task stands from git, the PR and the task's files, and a report that lands later is not read.
If the branch shows the step done, the drive moves on.
Otherwise the task halts, because an idle agent on a running run may be waiting on a permission dialog that a new prompt could answer.

- Run verify, and every other long command, in the foreground.
  Raise the `timeout` where the default is too short.
- If a command must run in the background, wait for it in the same turn with a Monitor until-loop on its exit.
  Never end the turn expecting to be woken when it reports.
  The drive reads an agent whose turn ended on a background Bash command or a Monitor as working for up to 30 minutes, so a turn that breaks this rule is not halted at once.
  The rule still holds, because a turn that waits reports sooner than one the drive waits out.
  A background job that never notifies leaves the drive waiting the full 30 minutes before it treats the agent as stalled.
- A driven turn ends in only three ways: the step's report, an ask or a failure.

## When a drive halts

The drive reads how far a turn got from the branch, the PR and the task's files, whoever prompted the turn.
A turn that stopped `failed` or `partial` is a failed attempt, and the drive sends the step again.
The task halts only in these cases:

- **An ask.** The task waits for the answer (**The answer**, below).
- **No progress.** The same action failed three times in a row, and git, the PR and the task's files did not change between the failures.
  The halt names the action and lists every attempt with its reason.
  A failure after which the state moved starts the count over.
- **A decision.** A run stopped `blocked` without an ask, such as a judge's stop or a stale task's verdict, halts at once, because sending the step again would roll the decision again.
- **A stall the branch does not explain** (**A turn ends at its report**, above), or a prompt the agent never took, because typing into a pane that may hold a dialog could answer it.
- **The drive's own environment fails**, because sending the step again cannot fix it: a git read that fails, a workspace that is gone, a base task that halted, or a task another drive holds.

## The answer

An answer arrives as your **next prompt**, in plain text, numbered the way the ask was.
It may be typed in this pane, sent by the drive, or sent from anywhere by `f10 asks answer` or `/f10:asks`.
The first answer to arrive closes the ask in the inbox, whichever way it came.

When the answer arrives:

1. Run `f10-state.sh set <phase> running [<leaf>]`, which clears the ask.
2. Fold the answers in as `conventions/gaps.md` does.
   Mark the gaps **Resolved** in the plan file, fold the decisions into the stages, and update Consequences where an answer changes a cost.
3. Resume from the step that stopped.

A prompt that arrives while no ask is recorded is not an answer.
Treat it as steering.

A **Claude Code permission dialog** cannot travel through run state.
The driving session sees only an idle agent.
So stay inside the session's existing permissions.
A dialog you cannot avoid is answered in this pane.

## When drive finishes the task

Sometimes the ship prompt says that drive finishes the task after ship, which `f10 drive` does when the run's pace, under the project's ceiling, says `finish: on`.
In that case, end the report with one line that says so.
Name the PR by number where there is one, and say that nothing is owed in this pane: `Next: f10 drive merges #204 and removes this worktree; nothing to do here.`
For a spike the line is `Next: f10 drive closes the spike and removes this worktree; nothing to do here.`

A driven run whose prompt does not say this adds no such line.

## Dry run

`--dry-run` together with `--driven` is a dry run that also lists which questions would become asks.
