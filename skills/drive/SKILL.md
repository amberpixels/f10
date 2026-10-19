---
name: drive
description: "f10 drive skill - run a list of tasks through a chain of skills from this session, each in its own worktree and agent, dependents after their base, pausing only when an agent asks; a parent task expands into the subtasks the user picks. Use when the user says \"/f10:drive <ids | id -- id> [skills...] [--model <model>]\", or asks to run several tasks through plan, judge, ship, review, resolve and finish without switching workspaces."
allowed-tools: Bash, AskUserQuestion
argument-hint: "<id>... [plan|judge|ship|review|resolve|finish ...]"
---

# f10 · drive

Run tasks through a chain from here: `f10 drive` opens each task as `f10 start` does, dependents after their base, and prompts each agent one skill at a time, marked `--driven` (`${CLAUDE_PLUGIN_ROOT}/modes/driven.md`).
`--model` picks every agent's model, `<id>:<model>` one task's, which wins.
This skill expands parents, runs the command, relays its questions and prints its report; it runs no step.

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- A parent id expands into the subtasks the user picks (**Parents** below); everything else goes to `f10 drive` verbatim.

## Parents

A task id whose task has subtasks is never driven itself: the user picks which subtasks run, every time, with no silent default.

1. **Find the parents, in one Bash call**: `f10 task children <id> --json` for each listed id; not a range's ends, flags or chain skills.
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
5. **Build the arguments**: each parent replaced in place by its chosen subtasks, in the rows' order, each carrying the parent's `:model` suffix; other ids, chain skills and flags as given.
   Nothing left to drive: say so in one line and stop.

Run it **in the background** (`run_in_background`): a list can take hours, and the command's exit wakes this session.

```
f10 drive <arguments>
```

The arguments are the expanded ones from **Parents**.
When it exits, read its output and act on the exit code.
On every exit, a line starting `!!!` goes first, verbatim, above everything else.

- **0** - every task went through its chain.
  Print the report block (one row per task) in a fenced `yaml` block and any line beneath it, and stop.
- **4** - one or more agents asked.
  The output carries one `<id> asks at <skill>: ...` line per asking task: numbered questions, each with its options in brackets, the first the default.
  Put every asking task's questions to the user in **one `AskUserQuestion`**: one question per numbered item, its header carrying the task id, the bracketed options as the choices, the first marked recommended.
  Then run the output's `answer with:` line in the background, its placeholders filled with the answers, numbered the way each ask was.
  Repeat on every exit 4.
- **5** - a task halted; the lines beneath the report say which and why.
  Print the report block and those lines verbatim, and stop.
  The user settles it in that workspace, then reruns the same command, which resumes where each task stands.
- **anything else** - the command refused; its message names the cause.
  Print it verbatim and stop.

If `f10` is not on `PATH`, or `f10 drive` is an unknown command, the binary is missing or older than this plugin: say so, quote `f10 --version` where it ran, and point at `go install github.com/amberpixels/f10/cli/cmd/f10@latest`.
