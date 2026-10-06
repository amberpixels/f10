---
name: drive
description: "f10 drive skill - run a list of tasks through a chain of skills from this session, each once its base task is finished and independent ones together: each task gets its worktree and agent, the agent is prompted skill by skill, and the run pauses only when an agent asks. Use when the user says \"/f10:drive <ids | id -- id> [skills...]\", or asks to run several tasks through plan, judge, ship, review, resolve and finish without switching workspaces."
allowed-tools: Bash, AskUserQuestion
argument-hint: "<id>... | <id> -- <id> [plan|judge|ship|review|resolve|finish ...] [--every <duration>] [-C <project>]"
---

# f10 · drive

Run tasks through a chain from here. `f10 drive` reads each task's one base: the `--after` its
branch recorded, else its body's `After:` line. It starts every task whose base is finished, and
those run together, each in its own worktree and agent opened the way `f10 start` does. Each
agent is prompted one skill at a time (`--driven`, per `${CLAUDE_PLUGIN_ROOT}/modes/driven.md`).
The driver polls Herdr and every task's run state until each turn ends, runs `finish` itself,
catches a started dependent up with `/f10:catchup` once its base is finished, and starts the
dependents that finish releases. The chain is the user's inline skills,
else `project.md → Drive chain`, else `plan → judge → ship → review → resolve → finish`. This
skill only runs the command, relays its questions and prints its report. It runs no step.

Run it **in the background** (`run_in_background`), with the user's arguments verbatim: a list
can take hours, and the command's exit is what wakes this session.

```
f10 drive <arguments>
```

When it exits, read its output and act on the exit code:

- **0** - every task went through its chain. Print the report block (one row per task) in a
  fenced `yaml` block and any line beneath it, and stop.
- **4** - one or more agents asked. The output carries one `<id> asks at <skill>: ...` line per
  asking task: numbered questions, each with its options in brackets, the first the default.
  Put every asking task's questions to the user in **one `AskUserQuestion`**: one question per
  numbered item, its header carrying the task id, the bracketed options as the choices, the
  first marked recommended. Then run the command again in the background, the same arguments
  plus the answers, numbered the way each ask was. One asking task takes a bare answer; several
  take one answer per task, keyed by its id:

  ```
  f10 drive <arguments> --answer "1. <answer> 2. <answer>"
  f10 drive <arguments> --answer "GH-12=1. <answer>" --answer "GH-14=1. <answer> 2. <answer>"
  ```

  The output's `answer with:` line is that command. The driver sends each answer to its own
  task's agent and carries on from there. Repeat on every exit 4. The other tasks' agents kept
  working while the driver was down; rows reading `running <skill>` are those, and the rerun
  picks them up where they stand.
- **5** - a task halted: a judge stop, a failed step, a refused finish, a catchup that did not
  end clean, an agent idle while its run still says running (a permission dialog in its pane).
  Its dependents read `halted: base <id> halted`, and every other task ran on. Print the report
  block and the lines beneath it verbatim, and stop. The user settles it in that workspace, then
  reruns the same command, which resumes where each task stands.
- **anything else** - the command refused before or while running: outside Herdr, a task the
  tracker does not know, a dependency the list cannot satisfy (a base outside the list that is
  not finished, or tasks that wait on each other in a circle). Print the message verbatim and
  stop.

If `f10` is not on `PATH`, or `f10 drive` is an unknown command, the binary is missing or older
than this plugin: say so, quote `f10 --version` where it ran, and point at
`go install github.com/amberpixels/f10/cli/cmd/f10@latest`.
