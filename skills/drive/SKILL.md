---
name: drive
description: "f10 drive skill - run a list of tasks through the project's flow from this session, each in its own worktree and agent, dependents once their base has a commit, pausing only when an agent asks; a parent task expands into the subtasks the user picks. Use when the user says \"/f10:drive <ids | id -- id> [--flow <name> | --local] [--model <model>]\", or asks to run several tasks through plan, ship and finish without switching workspaces."
allowed-tools: Bash, AskUserQuestion
argument-hint: "<id>... [--flow <name> | --local] [--model <model>]"
---

# f10 · drive

Run tasks through a flow from here: `f10 drive` opens each task as `f10 start` does, a dependent once its base branch has a commit (its finish waits for the base's), and prompts each agent one skill at a time, marked `--driven` (`${CLAUDE_PLUGIN_ROOT}/modes/driven.md`).
Each task runs the `default` flow, or the one `--flow <name>` or `--local` names (`f10 flow` lists them): `/f10:plan` when the flow leads with `plan` and the task has no plan file, then `/f10:ship`, which runs every other entry, then finish when the frontmatter's `drive_finish` is on; a spike is closed after ship either way (`f10 finish` closes it without a merge).
A rerun keeps the flow a task's branch already records.
No chain is typed after the ids: the flow says what runs.
`--model` picks every agent's model, `<id>:<model>` one task's, which wins.
A bundle, `ABC-1+ABC-2`, is one entry: one branch and agent, driven as one task under its lead; its finish leaves every rider open, named in the report.
This skill expands parents, runs the command, relays its questions and prints its report; it runs no step.

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- A parent id expands into the subtasks the user picks (**Parents** below); everything else goes to `f10 drive` verbatim.

## Parents

A task id whose task has subtasks is never driven itself: the user picks which subtasks run, every time, with no silent default.

1. **Find the parents, in one Bash call**: `f10 task children <id> --json` for each listed id; not a range's ends or flags.
   Strip a `:model` suffix for the lookup and keep it.
   Exit 3 or `[]`: the id stays as given.
   Rows: the id is a parent; its unfinished rows are the candidates.
   A parent whose rows are all finished is dropped from the list with one line saying so.
2. **Show each parent's tree** in chat, outside any code fence, so ids are links: a line `**<parent id>**`, then a nested markdown list in the rows' order, one level per `depth`.
   Each item: `[<id>](<url>) <title>`, then ` · after <after>` when `after` is set, then ` · draft` when `labels` holds `draft`.
   Finished rows are left out.
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
5. **Build the arguments**: each parent replaced in place by its chosen subtasks, in the rows' order, each carrying the parent's `:model` suffix; other ids and flags as given.
   Nothing left to drive: say so in one line and stop.

Run it **in the background** (`run_in_background`): a list can take hours, and the command's exit wakes this session.

```
f10 drive <arguments>
```

The arguments are the expanded ones from **Parents**.
Its first line is `drive <n>`, the record it keeps; rerunning the same arguments picks that record up.
When it exits, read its output and act on the exit code.
On every exit, a line starting `!!!` goes first, verbatim, above everything else.

- **0** - every task went through its flow.
  Print the report block (one row per task) in a fenced `yaml` block and any line beneath it, and stop.
- **4** - one or more agents asked.
  The output carries one `<id> asks at <skill>: ...` line per asking task: numbered questions, each with its options in brackets, the first the default.
  Put every asking task's questions to the user in **one `AskUserQuestion`**: one question per numbered item, its header carrying the task id, the bracketed options as the choices, the first marked recommended.
  Then run the output's `answer with:` line (`f10 drive --resume <n> --answer …`) in the background, its placeholders filled with the answers, numbered the way each ask was.
  Repeat on every exit 4.
- **5** - a task halted; the lines beneath the report say which and why.
  Print the report block and those lines verbatim, and stop.
  A row `owned by drive <n> (pid …)` is a task another drive holds: name that drive; `f10 drive list` shows it and `f10 drive cancel <n>` stops it.
  The user settles a halt in that workspace, then reruns the same command, which resumes where each task stands.
  A task whose run stopped (a `next:` line under its halt) is prompted again only when that `next:` names `/f10:catchup` and the catchup ended clean; any other needs steering through `--answer`.
- **anything else** - the command refused; its message names the cause.
  `drive <n> is running` means another session drives the same list: point at `f10 drive list`, and at `f10 drive cancel <n>` if the user wants to take it over.
  Print it verbatim and stop.

A `flag provided but not defined` or an unknown command means a stale `f10` earlier on `PATH` shadows the plugin's: say so, quote `f10 --version`, and point at `f10 doctor`. A missing binary fails inside the plugin's `f10` with its own one-line message: relay it.
