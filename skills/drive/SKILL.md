---
name: drive
description: "f10 drive skill - run a list of tasks through the project's flow from this session, each in its own worktree and agent, dependents once their base has a commit, pausing only when an agent asks; a parent task expands into the subtasks the user picks. Use when the user says \"/f10:drive <ids | id -- id> [--flow <name> | --local] [--agent <profile> | --model <model>]\", or asks to run several tasks through plan, ship and finish without switching workspaces."
allowed-tools: Bash, AskUserQuestion
argument-hint: "<id>[:profile]... [--flow <name> | --local]"
---

# f10 · drive

Run tasks through a flow from here: `f10 drive` opens each task as `f10 start` does, a dependent once its base branch has a commit (its finish waits for the base's), and prompts each agent one skill at a time, marked `--driven` (`${CLAUDE_PLUGIN_ROOT}/modes/driven.md`): `/f10:plan` when the flow leads with `plan` and the task has no plan file, then `/f10:ship`, which runs every other entry, then finish when the frontmatter's `drive_finish` is on (a spike is closed after ship either way: `f10 finish` closes it without a merge).
This skill expands parents, runs the command (`${CLAUDE_PLUGIN_ROOT}/modes/binary.md`), relays its questions and prints its report; it runs no step.

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- `<id>`: a task to drive; a parent id expands into the subtasks the user picks (**Parents** below).
  A bundle, `ABC-1+ABC-2`, is one entry: one branch and agent, driven as one task under its lead; its finish leaves every rider open, named in the report.
- `<id>:<profile>`: that task's agent profile, which wins over `--agent`.
- `--flow <name>` / `--local`: the flow every task runs instead of `default` (`f10 flow` lists them); a rerun keeps the flow a task's branch already records.
  No chain is typed after the ids: the flow says what runs.
- `--agent <profile>`: every agent's profile, `kind:model:effort` or a part of it, over the agents map's `default` (`docs/project-setup.md` → Agents); `--model <model>` is the model-only form.
- Everything else goes to `f10 drive` verbatim.

**Parents**: a task id whose task has subtasks is never driven itself; the user picks which subtasks run, every time, with no silent default.

1. **Find the parents, in one Bash call**: `f10 task children <id> --json` for each listed id; not a range's ends or flags.
   Strip a `:profile` suffix (`:opus`, `:opus:high`) for the lookup and keep it.
   Exit 3 or `[]`: the id stays as given.
   Rows: the id is a parent; its unfinished rows are the candidates.
   A parent whose rows are all finished is dropped from the list with one line saying so.
2. **Show each parent's tree**: a line `<parent id>`, then the output of `f10 task children <id> --open --plain` verbatim, in one plain fenced block per parent.
   Never redraw it; the rows, glyphs and urls are the binary's.
3. **Ask once, in one `AskUserQuestion`**: multi-select questions, header the parent id.
   An option is one candidate: label `<id> <title>`, cut to fit; description `after <after>`, or `first in its chain`.
   Non-draft candidates are marked recommended, draft ones are not.
   Packing: take the top-level chains (a `depth` 0 row and the rows under it) in order; keep a chain in one question when it fits in four options, fill a question with whole chains, and split a longer chain over consecutive questions in order.
   Past four questions, ask the rest in a further `AskUserQuestion` of up to four.
   A parent with one candidate gets a single-select question instead, `[include <id> | leave it out]`, since multi-select needs two options.
4. **Check for a broken chain**: a chosen candidate whose `after` is an unfinished sibling left out.
   Name each (`FT-75 is after FT-74, which you left out`) and ask once more, in one `AskUserQuestion`, a question per break: `[include FT-74 | drop FT-75]`.
   Apply the answers and check again; never fix a break silently.
   A base outside the siblings is not checked here: `f10 drive` refuses an unfinished one itself.
5. **Build the arguments**: each parent replaced in place by its chosen subtasks, in the rows' order, each carrying the parent's `:profile` suffix; other ids and flags as given.
   Nothing left to drive: say so in one line and stop.

Run it **in the background** (`run_in_background`): a list can take hours, and the command's exit wakes this session.
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
3. **Exit 4**: one or more agents asked; each ask waits in the asks inbox, so it can also be answered in the agent's pane, with `f10 asks` or from another session.
   Follow `${CLAUDE_PLUGIN_ROOT}/skills/asks/SKILL.md` for this drive's asking tasks: the questions in one `AskUserQuestion`, then `f10 asks answer`, which resumes the drive through the daemon.
   Then rerun the same arguments in the background to follow the resumed drive, and act on its exit as above; an ask already answered elsewhere resumes the drive the same way.
4. **Exit 5**: a task halted; the lines beneath the report say which and why.
   Print the report block and those lines verbatim, and stop.
   A row `owned by drive <n> (pid …)` is a task another drive holds: name that drive; `f10 drive list` shows it and `f10 drive cancel <n>` stops it.
   The user settles a halt in that workspace, then reruns the same command, which resumes where each task stands.
   A task whose run stopped (a `next:` line under its halt) is prompted again only when it stopped for a catchup (its `next:` names `/f10:catchup`, or a catchup failed) and a later catchup ended clean; any other needs steering through `--answer`.
   A ship stopped by its catchup guard gets a catchup from the drive each time its base moves and resumes without a halt; it halts here when a catchup made no progress or did not end clean.
   A task whose branch is on origin is caught up whenever its PR's target moves, at its next skill boundary, and the drive pushes the caught-up branch.
5. **Any other exit**: the command refused; its message names the cause.
   `drive <n> is running` means another session drives the same list: point at `f10 drive list`, and at `f10 drive cancel <n>` if the user wants to take it over.
   Print it verbatim and stop.
