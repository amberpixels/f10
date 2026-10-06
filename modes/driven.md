# Convention · driven - a run prompted from another session

A **driven** run is one nobody is watching in its own pane. `f10 start` prompts an agent in a
worktree of its own, `f10 forward` hands a command to that agent from wherever the user typed
it, and `f10 drive` prompts it skill by skill from the main session. The user is in the session
that sent the command, not in the agent's pane, so an interactive question asked there is a
question nobody sees: the run stalls on a dialog until someone switches tabs to find it.

Driven mode changes one thing: **the transport of every question.** The steps, the plan, the
pipeline, the reports are the same.

## Activation

Any f10 skill runs driven when its argument contains the token **`--driven`** (anywhere in it,
like `--dry-run`). `f10 start` and `f10 forward` append it to every f10 command they send, so an
agent is driven from its first turn; `f10 drive` marks every skill it sends the same way. Driven
is for the whole run: a skill that chains into another (`/f10:plan ... && /f10:ship ...`) passes
the token on when it invokes the next skill, and nothing downstream drops it.

Strip the token, route the argument exactly as normal, follow the steps as written, and apply
the rules below wherever a step calls for a question.

## No interactive question, ever

A driven run never opens `AskUserQuestion`, and never ends a turn on a question in chat. Every
place the steps and conventions call for one - the gaps questionnaire before implementing
(`conventions/gaps.md`), **reuse this plan or re-plan?** (`skills/ship`), **wait or proceed** on
a base task with no code yet, a judge's rethink (`steps/judge.md`), an `ask` verdict in resolve
(`steps/resolve.md`), a conflict catchup cannot settle from the diff (`steps/catchup.md`), a
deploy confirmation - goes through run state instead:

1. **Write the whole batch as one ask.** `f10-state.sh ask "<text>"` - one line, numbered
   questions, each with its options in brackets, the default or recommended answer first:
   `1. Reuse the saved plan? [reuse | re-plan] 2. Base GH-7 has no code yet: [wait | proceed on its plan]`.
   The gaps convention already batches every open question into one questionnaire; driven mode
   sends that batch as one `ask`, never one question per turn.
2. **Stop blocked, in the same call.** `f10-state.sh set <phase> blocked [<leaf>]` - the phase
   the run is in, the pipeline step for ship - joined to the `ask` call with `;`. `blocked`
   is the state for "stopped by a decision, not by breakage" (`conventions/report.md`), and the
   `Stop` hook leaves it alone, so the badge holds.
3. **End the turn.** Print the ask once, as prose under a facts block carrying the task, and
   stop. Nothing is pending in the pane: the question lives in run state, where `f10 status
   <task-id>` reads it from any session.

`f10 start`'s own prompt steers with "take defaults for all gaps", so the gaps questionnaire
raises no ask at all in that run; the rule above covers the questions a steering line cannot
settle in advance.

## The answer

The driving session reads the ask with `f10 status <task-id>`, puts the same questions to the
user, and sends the answers back with `f10 forward <task-id> "<answers>"`, numbered the way the
ask was. They arrive as this agent's **next prompt**, plain text, no slash command. Under
`f10 drive` the driver is the driving session: it reads the ask from run state itself, exits so
`/f10:drive` can put it to the user, and sends the answers back the same way when rerun with
`--answer`. Several tasks can be asking at once. Each answer is then keyed by its task
(`--answer GH-12="1. …"`) and reaches that task's agent alone.

On that prompt: `f10-state.sh set <phase> running [<leaf>]`, which clears the ask; fold the
answers in exactly as the gaps convention folds a filled questionnaire - gaps flipped to
**Resolved** in the plan file, decisions into the stages; then resume from the step that stopped.
An answer that arrives with no ask recorded is steering, and is read as such.

## What cannot travel this way

A **Claude Code permission dialog** in this pane is not f10's. It does not go through run state,
and the driving session cannot answer it: it reports the agent as idle and not reporting, and
names the workspace. Keep the run inside what the session's permissions already allow, and when
a dialog is unavoidable, the user answers it in this pane.

`--dry-run` beside `--driven` is a dry run: it reports what the driven run would do, including
which questions would become asks, and executes nothing.
