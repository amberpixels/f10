---
name: drive
description: "f10 drive skill - run a list of tasks through the project's flow from this session, each in its own worktree and agent, dependents once their base has a commit, pausing once an agent asks and nothing else can move; a parent task expands into its subtasks, the user picking only when a draft is among them. Use when the user says \"/f10:drive <ids | id -- id> [--flow <name> | --local] [--pace <name>] [--agent <profile> | --model <model>] [--parallel N] [--plan]\", asks to run several tasks through plan, ship and finish without switching workspaces, or asks what to bundle, run in parallel or order before driving a set of tasks."
allowed-tools: Bash, AskUserQuestion, Read, Grep
argument-hint: "<id>[:profile]... [--flow <name> | --local] [--pace <name>] [--plan]"
---

# f10 · drive

Run tasks through a flow from here: `f10 drive` opens each task as `f10 start` does, a dependent once its base branch has a commit (its finish waits for the base's), and prompts each agent one skill at a time, marked `--driven` (`${CLAUDE_PLUGIN_ROOT}/modes/driven.md`): `/f10:plan` when the flow leads with a `plan` the pace keeps and the task has no plan file, then `/f10:ship`, which runs every other entry, then finish unless the pace's `finish` (capped by the project's `ceiling`) is off (a spike is closed after ship either way: `f10 finish` closes it without a merge).
This skill expands parents, runs the command (`${CLAUDE_PLUGIN_ROOT}/modes/binary.md`), relays its questions and prints its report; it runs no step.

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- `<id>`: a task to drive; a parent id expands into its unfinished subtasks (**Parents** below).
  A bundle, `ABC-1+ABC-2`, is one entry: one branch and agent, driven as one task under its lead; its finish leaves every rider open, named in the report.
- `<id>:<profile>`: that task's agent profile, which wins over `--agent`.
- `--flow <name>` / `--local`: the flow every task runs instead of `default` (`f10 flow` lists them); a rerun keeps the flow a task's branch already records.
- `--pace <name>`: the pace every task runs at (`docs/project-setup.md` → Paces), recorded on each branch over any earlier one; a rerun that names none keeps the pace a task's branch records.
  No chain is typed after the ids: the flow says what runs.
- `--agent <profile>`: every agent's profile, `kind:model:effort` or a part of it, over the agents map's `default` (`docs/project-setup.md` → Agents); `--model <model>` is the model-only form.
- `--parallel N`: at most N tasks run a skill at once, and at most twice N agents stay open, because an idle session still holds its memory.
  The default is half the CPU cores, from 2 to 8.
  Tasks past the cap queue in the drive's order and show as `queued`.
  A task frees its slot once it asks, waits for its base's first commit, or waits at finish for its base.
  A rerun or `--resume` keeps the drive's cap unless it passes a new one.
- `--plan`: propose the drive first (what to bundle, run in parallel and order), settle it with the user, then drive what was agreed (**Plan** below).
  Without it, this skill never plans unasked.
- Everything else goes to `f10 drive` verbatim.

**Parents**: a task id whose task has subtasks is never driven itself; its unfinished subtasks run instead.
The user picks which ones only when a candidate is a draft (a `draft` label), because a draft is an idea not ready to plan.
With no draft among them, every candidate runs, because naming the parent already asked for all of its subtasks.

1. **Find the parents, in one Bash call**: `f10 task children <id> --json` for each listed id; not a range's ends or flags.
   Strip a `:profile` suffix (`:opus`, `:opus:high`) for the lookup and keep it.
   Exit 3 or `[]`: the id stays as given.
   Rows: the id is a parent; its unfinished rows are the candidates.
   A parent whose rows are all finished is dropped from the list with one line saying so.
2. **Show each parent's tree**: a line `<parent id>`, then the output of `f10 task children <id> --open --plain` verbatim, in one plain fenced block per parent.
   Never redraw it; the rows, glyphs and urls are the binary's.
3. **No draft among a parent's candidates**: every candidate is chosen, with no question; say so in one line under its tree (`driving FT-397, FT-398 and FT-399`).
   Points 4 and 5 apply only to a parent with a draft candidate.
4. **Ask once, in one `AskUserQuestion`**: multi-select questions, header the parent id, for every parent with a draft candidate.
   An option is one candidate: label `<id> <title>`, cut to fit; description `after <after>`, or `first in its chain`.
   Non-draft candidates are marked recommended, draft ones are not.
   Packing: take the top-level chains (a `depth` 0 row and the rows under it) in order; keep a chain in one question when it fits in four options, fill a question with whole chains, and split a longer chain over consecutive questions in order.
   Past four questions, ask the rest in a further `AskUserQuestion` of up to four.
   A parent with one candidate gets a single-select question instead, `[include <id> | leave it out]`, since multi-select needs two options.
5. **Check for a broken chain**: a chosen candidate whose `after` is an unfinished sibling left out.
   Name each (`FT-75 is after FT-74, which you left out`) and ask once more, in one `AskUserQuestion`, a question per break: `[include FT-74 | drop FT-75]`.
   Apply the answers and check again; never fix a break silently.
   A base outside the siblings is not checked here: `f10 drive` refuses an unfinished one itself.
6. **Build the arguments**: each parent replaced in place by its chosen subtasks, in the rows' order, each carrying the parent's `:profile` suffix; other ids and flags as given.
   Nothing left to drive: say so in one line and stop.
7. **The hint**, without `--plan`: in the same Bash call as point 1, run `f10 drive plan <the arguments as typed> --hint`.
   It prints one line only when the rules alone would save a CI wait, read from saved plans (a small plan's size, its `Areas:` line), since a plain drive reads no code; print that line under the trees, and drive as asked.
   A hint that errors (a flag `drive plan` does not take) prints nothing here.

**Plan** (`--plan`): `f10 drive plan` proposes the command, and this skill supplies what only a read of the code can tell.
Strip `--plan`; the rest is the **planning arguments**, kept verbatim, because a rerun finds the agreed drive by them.
A parent with a draft candidate goes through **Parents** points 4 and 5 first, and the chosen subtasks replace it in the planning arguments, since the binary leaves drafts out.

1. **Agreed already?** Run `f10 drive plan <planning arguments> --json`.
   An `agreed` number means this planning call already drove: say `following drive <n>` in one line, then run its `command` as **Run** below, with no new proposal.
2. **Read each task the binary could not size**, a task in `tasks` with no `lines`: `f10 task read <id>`, then a few targeted `Grep` and `Read` calls against the code, the way a task is investigated before planning, with no plan written.
   Settle for each: `areas` in the words `project.md` Roles and `flow_by_area` use, the `files` it most likely changes, `lines` (an estimate of changed lines), `buildsOn` (a listed task whose new code it needs, when its body has no `After:` saying so), `risky: migration` for a schema or data migration, and `kind` only when no label settled it.
   Read in one message per few tasks (`conventions/latency.md` rule 5); a task whose plan exists is read through its plan's stages instead.
3. **Write the facts sheet** with one Bash heredoc to `<scratchpad>/drive-plan.json`, the shape `docs/cli.md` → Plan gives, keyed by task id, and run `f10 drive plan <planning arguments> --facts <sheet>`.
4. **Show the proposal** verbatim in a plain fenced block, then take edits in plain words ("keep FT-45 alone", "bundle 46 with 45", "put 47 in stage 2").
   Turn each into an override in the sheet: `alone: true`; `with: <id>`; `after: <id>` or `after: "none"` (stage 2 means after nothing in stage 2 or later).
   Rerun the command and show the new proposal; never edit the command or the stages by hand, because the reasons would no longer match.
5. **Confirm once**, in one `AskUserQuestion`: a single-select question `Drive this command?` `[drive it | change it]`, each option carrying the proposal as its `preview`, plus one per proposed `After:` edit, `[add After: <base> to <id> | leave it]`.
   The preview is the last point 3 run's output, verbatim in a plain fenced block, because the dialog can cover what point 4 printed, and the user must see the stages whenever the question is on screen.
   Both options carry it, because the preview pane shows only the option in focus.
   `change it` goes back to point 4, so the next question previews the new proposal.
   Apply each accepted edit with `f10 task update <id> --after <base>` (`none` drops the line), all in one Bash call.
   A declined or refused (exit 3) edit is taken back in the sheet, so the command never relies on an order the tracker lacks, nor drops one it holds:
   - an edit the planner proposed (the task has no `After:` of its own, the order came from `buildsOn` or a shared file) sets `after: "none"`;
   - an edit from the user's own `after` override removes that override, so the body's `After:` stands.

   Then rerun point 3 once.
   If the rerun's command or stages differ from the previewed proposal, ask `Drive this command?` again with the new proposal as its preview, because a taken-back edit can regroup or reorder the stages the user confirmed.
6. **Run** `f10 drive <the command's arguments> --planned "<planning arguments>"` as **Run** below, in the background.
   The record keeps the planning arguments, so `/f10:drive <planning arguments> --plan` follows this drive (point 1).

**Run** it in the background (`run_in_background`): a list can take hours, and the command's exit wakes this session.
The drive itself runs in the per-user f10 daemon, so closing or clearing this session leaves it running; the command only prints its progress and exits with its code.
Rerunning the same arguments follows the drive already running, and from another session `f10 daemon status` lists running drives and `f10 daemon logs <n> -f` follows one.

```
f10 drive <arguments>
```

1. Run it with the expanded arguments from **Parents**.
   Its first line is `drive <n>`, the record it keeps; rerunning the same arguments picks that record up.
   When it exits, read its output and act on the exit code.
2. **Exit 0**: every task went through its flow.
   Print the report block (one row per task) in a fenced `yaml` block and any line beneath it, and stop.
   A finished task's `<id> handoff:` lines are markdown: relay them verbatim beneath the block, outside the fence.
3. **Exit 4**: one or more agents asked, and the other tasks went as far as they can; each ask waits in the asks inbox from the moment it was asked, so it can also be answered in the agent's pane, with `f10 asks` or from another session.
   First print the `<id> handoff:` lines of the tasks that finished, verbatim and outside a fence, because a rerun reports those tasks as already finished, with their handoffs archived and not printed again.
   Then follow `${CLAUDE_PLUGIN_ROOT}/skills/asks/SKILL.md` for this drive's asking tasks: the questions in one `AskUserQuestion`, then `f10 asks answer`, which resumes the drive through the daemon.
   Then rerun the same arguments in the background to follow the resumed drive, and act on its exit as above; an ask already answered elsewhere resumes the drive the same way.
4. **Exit 5**: a task halted; the lines beneath the report say which and why.
   Print the report block and those lines verbatim, and stop.
   A row `owned by drive <n> (pid …)` is a task another drive holds: name that drive; `f10 drive list` shows it and `f10 drive cancel <n>` stops it.
   The user settles a halt in that workspace, then reruns the same command, which resumes where each task stands.
   The drive reads each task's state from git, its PR and its files, so a turn typed by hand, a turn shorter than a poll, or an agent that never reported does not halt it.
   A failed or partial turn is sent again, and the task halts when the same action failed three times in a row with nothing changed, the lines listing each attempt; a blocked stop (a judge's, a stale task's), a stall the branch does not explain, and a failure of the drive's own environment (a git read, a workspace gone, a base that halted) halt at once.
   A task whose run stopped (a `next:` line under its halt) is prompted again on a rerun only when it stopped for a catchup (its run carries the guard's `stop guard` kind, or a catchup failed) and a later catchup ended clean; any other needs steering through `--answer`.
   A ship stopped by its catchup guard gets a catchup from the drive each time its base moves and resumes without a halt.
   The drive runs a catchup itself (git, then verify, then the push) and prompts the agent only for a conflict or a red verify, or for verify itself when the project's Verify heading is prose; a catchup git does not show settled after that turn is a failed attempt under the same rule.
   A task whose branch is on origin is caught up whenever its PR's target moves, at its next skill boundary, and the drive pushes the caught-up branch.
5. **Any other exit**: the command refused; its message names the cause.
   `drive <n> is running` means another session drives the same list: point at `f10 drive list`, and at `f10 drive cancel <n>` if the user wants to take it over.
   Print it verbatim and stop.
