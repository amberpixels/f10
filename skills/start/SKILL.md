---
name: start
description: "f10 start skill - start tasks without leaving this session, each in its own worktree, Herdr workspace and agent, run through the project's flow by the f10 daemon: plan, ship, then finish when the flow ends in it; a list, a range or a parent runs every task, a dependent once its base has a commit (under --plan, once its base's plan is saved), pausing once an agent asks and nothing else can move; --discuss or a PR link opens one agent that summarises and waits. Use when the user says \"/f10:start <task-id | ids | id -- id | parent | PR url | #n> [--plan | --discuss | --local | --flow <name>] [--pace <name>] [--after <task-id> | --base <branch>] [--task <id>] [--agent <profile> | --model <model>] [--parallel N]\", asks to start, run or ship one or several tasks in new worktrees, to run a parent's subtasks, to discuss a task before planning, to pick up someone else's PR, or asks what to bundle, run in parallel or order before starting a set of tasks, which --discuss answers: its agent reads the tasks and proposes it."
allowed-tools: Bash, AskUserQuestion, Read, Grep
argument-hint: "<id>[:profile]... | <id> -- <id> | <PR url> [--plan | --discuss] [--pace <name>]"
---

# f10 · start

Start tasks beside this one.
`f10 start` gives each task a branch, a worktree, a Herdr workspace and a claude agent.
The per-user f10 daemon then prompts each agent one skill at a time, marked `--driven` (`${CLAUDE_PLUGIN_ROOT}/modes/driven.md`).
The first skill is `/f10:plan` when the flow leads with a `plan` the pace keeps and the task has no plan file.
Then comes `/f10:ship`, which runs every other entry.
Then comes finish, when the flow ends in a `finish` entry.
The pace can skip that entry as it skips any step, and the project's `ceiling: { finish: off }` skips it under every pace.
A spike is closed after ship either way, because `f10 finish` closes it without a merge.
A plan the main checkout saved for a task stays where it is, in the storage root every worktree shares, and the agent ships it instead of planning again.
A dependent opens once its base branch has a commit, and its finish waits for the base's.
Under `--plan` a dependent opens once its base's plan is saved, because it designs against that plan.
Two starts open one agent, prompt it once and leave it to you: `--discuss` and a PR pickup, because their agent waits for a person.
This skill expands parents, runs the command (`${CLAUDE_PLUGIN_ROOT}/modes/binary.md`), relays its questions and prints its report.
It runs no step.

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- `<id>`: a task to start.
  A parent id expands into its unfinished subtasks (**Parents** below).
  A bundle, `ABC-1+ABC-2`, is one entry with one branch, worktree, agent and PR.
  It is keyed on the lead, and any member's id reopens it.
  Its finish leaves every rider open, and the report names them.
  `<id> -- <id>` is a range.
- `<id>:<profile>`: that task's agent profile, which wins over `--agent`.
- `<PR url>` or `#n`: pick that PR up; its branch is checked out tracking origin's and linked to its task, and the agent summarises it and waits.
  A merged or closed PR is picked up the same way, for a post-merge review: checked out at the commit it landed as, on a local-only branch named for the task, the agent told the PR's state; `--plan` is refused on it.
- `--discuss`: one worktree and one agent, prompted without `--driven`; it reads the tasks and waits.
  A parent opens the parent's worktree and its agent shows the subtask tree; several ids open the first id's, and its agent reads them all.
- `<note>`: with `--discuss` or a PR, the words after the ids; the agent acts on it after its summary, then waits.
- `--plan`: plan every task and implement none.
  A later start without it ships the saved plans.
- `--flow <name>` / `--local`: the flow every task runs instead of `default` (`f10 flow` lists them).
  A rerun keeps the flow a task's branch already records.
- `--pace <name>`: the pace every task runs at (`docs/project-setup.md` → Paces).
  It is recorded on each branch over any earlier one.
  A rerun that names none keeps the pace a task's branch records.
- `--after <task-id>` / `--base <branch>`: one task's base, over its body's `After:` line.
- `--agent <profile>`: every agent's profile, laid over the agents map's `default` (`docs/project-setup.md` → Agents).
  It is `kind:model:effort` (`claude:opus:high`), `model:effort` or either alone.
  `--model <model>` is the model-only form (`opus`, `fable`, `opus-5.5`), and near misses are corrected.
- `--parallel N`: at most N tasks run a skill at once, and at most twice N agents stay open, because an idle session still holds its memory.
  The default is half the CPU cores, from 2 to 8.
  Tasks past the cap queue in the start's order and show as `queued`.
  A task frees its slot once it asks, waits for its base's first commit, or waits at finish for its base.
- Everything else (`--task`, `--resume`) goes to `f10 start` verbatim.

**Parents**: a task id whose task has subtasks is never started itself.
Its unfinished subtasks run instead.
The user picks which ones only when a candidate is a draft (a `draft` label), because a draft is an idea not ready to plan.
With no draft among them, the parent goes to `f10 start` as typed, which runs every subtask.
`--discuss` and a PR skip this section.

1. **Find the parents, in one Bash call**: `f10 task children <id> --json` for each listed id; not a range's ends or flags.
   Strip a `:profile` suffix (`:opus`, `:opus:high`) for the lookup and keep it.
   Exit 3 or `[]`: the id stays as given.
   Rows: the id is a parent; its unfinished rows are the candidates.
   A parent whose rows are all finished is dropped from the list with one line saying so.
2. **Show each parent's tree**: a line `<parent id>`, then the output of `f10 task children <id> --open --plain` verbatim, in one plain fenced block per parent.
   Never redraw it; the rows, glyphs and urls are the binary's.
3. **No draft among a parent's candidates**: the parent stays in the arguments, with no question.
   Say so in one line under its tree (`starting FT-397, FT-398 and FT-399`).
   Points 4 to 6 apply only to a parent with a draft candidate.
4. **Ask once, in one `AskUserQuestion`**: multi-select questions, header the parent id, for every parent with a draft candidate.
   An option is one candidate: label `<id> <title>`, cut to fit; description `after <after>`, or `first in its chain`.
   Non-draft candidates are marked recommended, draft ones are not.
   Packing: take the top-level chains (a `depth` 0 row and the rows under it) in order; keep a chain in one question when it fits in four options, fill a question with whole chains, and split a longer chain over consecutive questions in order.
   Past four questions, ask the rest in a further `AskUserQuestion` of up to four.
   A parent with one candidate gets a single-select question instead, `[include <id> | leave it out]`, since multi-select needs two options.
5. **Check for a broken chain**: a chosen candidate whose `after` is an unfinished sibling left out.
   Name each (`FT-75 is after FT-74, which you left out`) and ask once more, in one `AskUserQuestion`, a question per break: `[include FT-74 | drop FT-75]`.
   Apply the answers and check again; never fix a break silently.
   A base outside the siblings is not checked here: `f10 start` stacks on one with a branch and refuses one with no branch that is not finished.
6. **Build the arguments**: replace each such parent in place by its chosen subtasks, in the rows' order.
   Each subtask carries the parent's `:profile` suffix.
   Other ids and flags stay as given.
   Nothing left to start: say so in one line and stop.
7. **The hint**: with two ids or more, or a parent, in the arguments as typed, run `f10 drive plan <the arguments as typed> --hint` in the same Bash call as point 1.
   It runs before point 6 builds the expanded list, so it reads the typed arguments, and `drive plan` expands a parent itself.
   It prints one line only when bundling would save a CI wait, with the command that saves it.
   The rules read saved plans (a small plan's size, its `Areas:` line), because a plain start reads no code.
   Print that line under the trees, and start as asked.
   A hint that errors (a flag `drive plan` does not take) prints nothing here.

**Run**

```
f10 start <arguments>
```

**A discussion or a PR** (`--discuss`, `--task` or a PR url) runs in the foreground and returns once the agent is prompted.
A bare `#n` runs in the background with **every other start**, because the binary asks the host whether it is a PR or a task.
A pickup then ends at once with its report and exit 0, or with exit 3 as below.
A task runs its flow.
The per-user f10 daemon then watches the task.
A merged PR, a stalled agent and a stopped run land in its watch log, which `f10 watch` reads.
Under `watch_catchup: on`, the daemon catches the task's open PR up with its target when the PR conflicts with it or must be caught up (`docs/cli.md` → Watch).

1. Print the command's report block in a fenced `yaml` block (`conventions/report.md`) and any line beneath it, and stop.
2. **Exit 3 is a question**: a PR whose body, title and branch name no task.
   Ask the user for the task id once with `AskUserQuestion` (the PR's url in the question; the id comes through Other), rerun the same command with `--task <id>` appended, and relay its report.
3. Every other failure is final: print the message verbatim and stop.

**A start whose every task is closed**, none of them with a branch, opens a discussion instead of running the flow, because there is nothing left to run.
It prints one line saying so, and the report names, for each closed task, the commits that shipped it and any open task whose id is one digit off.
A PR pickup is unaffected.

**A start inside a discussion's worktree** hands the worktree to the start's first group, renamed to its lead, and opens the other groups as usual.
Run it in the foreground: it returns once the daemon has the drive, and prints that this agent gets its first prompt when the turn ends.
Print its output and end the turn there, because the drive's first prompt comes into this pane.

**Every other start** runs the tasks through their flow.
Run it in the background (`run_in_background`), because a flow can take hours, and the command's exit wakes this session.
The drive itself runs in the per-user f10 daemon, so closing or clearing this session leaves it running; the command only prints its progress and exits with its code.
Rerunning the same arguments follows the drive already running; from another session `f10 status --drives` lists the drives, `f10 daemon logs <n> -f` follows one, and `f10 stop <id | n>` stops one.

1. Run it with the arguments from **Parents**.
   Its first line is `drive <n>`, the record it keeps; rerunning the same arguments picks that record up.
   When it exits, read its output and act on the exit code.
2. **Exit 0**: every task went through its flow.
   Print the report block (one row per task) in a fenced `yaml` block and any line beneath it, and stop.
   A finished task's `<id> handoff:` lines are markdown: relay them verbatim beneath the block, outside the fence.
3. **Exit 4**: one or more agents asked, and the other tasks went as far as they can.
   Each ask waits in the asks inbox from the moment it was asked, so it can also be answered in the agent's pane, with `f10 asks` or from another session.
   First print the `<id> handoff:` lines of the tasks that finished, verbatim and outside a fence.
   Print them now, because a rerun reports those tasks as already finished, with their handoffs archived and not printed again.
   Then follow `${CLAUDE_PLUGIN_ROOT}/skills/asks/SKILL.md` for this drive's asking tasks: the questions in one `AskUserQuestion`, then `f10 asks answer`, which resumes the drive through the daemon.
   Then rerun the same arguments in the background to follow the resumed drive, and act on its exit as above; an ask already answered elsewhere resumes the drive the same way.
4. **Exit 5**: a task halted; the lines beneath the report say which and why.
   Print the report block and those lines verbatim, and stop.
   A row `owned by drive <n> (pid …)` is a task another drive holds: name that drive; `f10 status --drives` shows it and `f10 stop <n>` stops it.
   The user settles a halt in that workspace, then reruns the same command, which resumes where each task stands.
   The drive reads each task's state from git, its PR and its files.
   So a turn typed by hand, a turn shorter than a poll, or an agent that never reported does not halt it.
   A failed or partial turn is sent again.
   The task halts when the same action failed three times in a row with nothing changed, and the lines list each attempt.
   A blocked stop, such as a judge's or a stale task's, halts at once.
   So does a stall the branch does not explain.
   So does a failure of the drive's own environment (a git read, a workspace gone, a base that halted).
   A task whose run stopped (a `next:` line under its halt) needs steering through `--answer`.
   The exception is a task that stopped for a catchup, when a later catchup ended clean: a rerun prompts it again.
   A run counts as stopped for a catchup if it carries the guard's `stop guard` kind, or if a catchup failed.
   A ship stopped by its catchup guard gets a catchup from the drive each time its base moves and resumes without a halt.
   The drive runs a catchup itself (git, then verify, then the push).
   It prompts the agent only for a conflict or a red verify, or for verify itself when the project's Verify heading is prose.
   A catchup that git does not show settled after that turn is a failed attempt under the same rule.
   A task whose branch is on origin is caught up whenever its PR's target moves, at its next skill boundary, and the drive pushes the caught-up branch.
5. **Any other exit**: the command refused; its message names the cause.
   `drive <n> is running` means another session runs the same tasks: point at `f10 status --drives`, and at `f10 stop <n>` if the user wants to take it over.
   Print it verbatim and stop.
