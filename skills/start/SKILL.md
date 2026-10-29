---
name: start
description: "f10 start skill - start the next task without leaving this session: a worktree, a Herdr workspace and a claude agent in it, already prompted; a plan the main checkout saved for the task is shipped by the agent, read in place; a PR link picks up someone else's PR. Use when the user says \"/f10:start <task-id | PR url | #n> [--plan | --local | --flow <name> | --discuss] [--after <task-id> | --base <branch>] [--task <id>] [--agent <profile> | --model <model>]\" or asks to start a task in a new worktree or workspace, including one to discuss before planning, one on another flow or model, or a PR handed over to continue or review."
allowed-tools: Bash
argument-hint: "<task-id | PR url> [--plan | --local | --discuss]"
---

# f10 · start

Start a task beside this one: `f10 start` creates the branch and worktree, opens them as a Herdr workspace, starts a claude agent in its root pane and prompts it, marked `--driven` (`${CLAUDE_PLUGIN_ROOT}/modes/driven.md`) unless `--discuss`.
A plan the main checkout saved for the task (`/f10:plan` after a discussion there) stays where it is, in the storage root every worktree shares, and the agent ships it instead of planning again.
This skill only runs the command and relays its output (`${CLAUDE_PLUGIN_ROOT}/modes/binary.md`); it runs no step and never waits on the new agent.
The per-user f10 daemon then watches the task while no drive owns it: a merged PR, a stalled agent and a stopped run land in its watch log, which `f10 watch` reads, and under `watch_catchup: on` its open PR is kept up to date with its target (`docs/cli.md` → Watch).

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- `<task-id>`: the task to start; `ABC-1+ABC-2` starts a **bundle**, several tasks on one branch, one worktree, one agent and one PR, keyed on the lead (the first id), which any member's id reopens.
- `<PR url>` or `#n`: pick that PR up; its branch is checked out tracking origin's and linked to its task, and the agent summarises it and waits.
  A merged or closed PR is picked up the same way, for a post-merge review: checked out at the commit it landed as, on a local-only branch named for the task, the agent told the PR's state; `--plan` is refused on it.
- `--plan`: stop after the plan.
- `--local` / `--flow <name>`: the `local` flow, or any declared one (`f10 flow` lists them), instead of `default`.
- `--discuss`: the agent is prompted without `--driven`; it summarises the task and waits.
- `--agent <profile>`: the agent's profile, `kind:model:effort` (`claude:opus:high`), `model:effort` or either alone, laid over the agents map's `default` (`docs/project-setup.md` → Agents); `--model <model>` is the model-only form (`opus`, `fable`, `opus-5.5`), near misses corrected.
- `<note>`: with `--discuss` or a PR, the words after the id; the agent acts on it after its summary, then waits.
- Everything else (`--after`, `--base`, `--task`) goes to `f10 start` verbatim.

```
f10 start <arguments>
```

1. Run it, then print the command's report block in a fenced `yaml` block (`conventions/report.md`) and any line beneath it, and stop.
2. **Exit 3 is a question**: a PR whose body, title and branch name no task.
   Ask the user for the task id once with `AskUserQuestion` (the PR's url in the question; the id comes through Other), rerun the same command with `--task <id>` appended, and relay its report.
3. **Exit 6 is a question**: a parent task with unfinished subtasks, named in the message.
   Read them with `f10 task children <id> --json` (unfinished rows only), then ask once with `AskUserQuestion`, header the parent id: **Drive the subtasks** first, then up to three subtasks as `Start <id>` with the title as description, depth 0 first; another subtask comes through Other.
   Drive invokes the `/f10:drive <id>` skill, carrying `--agent` or `--model` and dropping start's other flags with one line saying so.
   A subtask reruns `f10 start <subtask>` with the original mode or flow flag and `--agent` or `--model`, dropping `--after` and `--base` with one line saying so, since the subtask's own `After:` line sets its base; relay its report under these same rules.
4. Every other failure is final: print the message verbatim and stop.
