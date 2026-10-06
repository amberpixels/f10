---
name: drive
description: "f10 drive skill - run a list of tasks through a chain of skills from this session, one task at a time: each task gets its worktree and agent, the agent is prompted skill by skill, and the run pauses only when an agent asks. Use when the user says \"/f10:drive <ids | id -- id> [skills...]\", or asks to run several tasks through plan, judge, ship, review, resolve and finish without switching workspaces."
allowed-tools: Bash, AskUserQuestion
argument-hint: "<id>... | <id> -- <id> [plan|judge|ship|review|resolve|finish ...] [--every <duration>] [-C <project>]"
---

# f10 · drive

Run tasks through a chain from here: `f10 drive` starts each task's worktree and agent the way
`f10 start` does, prompts the agent one skill at a time (`--driven`, per
`${CLAUDE_PLUGIN_ROOT}/modes/driven.md`), polls Herdr and the task's run state until each turn
ends, runs `finish` itself, and moves to the next task. The chain is the user's inline skills,
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
- **4** - an agent asked. The output carries a `<id> asks at <skill>: ...` line: numbered
  questions, each with its options in brackets, the first the default. Put them to the user in
  **one `AskUserQuestion`**: one question per numbered item, the bracketed options as the
  choices, the first marked recommended. Then run the command again in the background, the same
  arguments plus the answers, numbered the way the ask was:

  ```
  f10 drive <arguments> --answer "1. <answer> 2. <answer>"
  ```

  The output's `answer with:` line is that command. The driver sends the answers to the same
  task's agent and carries on from there. Repeat on every exit 4.
- **5** - a task halted: a judge stop, a failed step, a refused finish, an agent idle while its
  run still says running (a permission dialog in its pane). Print the report block and the lines
  beneath it verbatim, and stop. The user settles it in that workspace, then reruns the same
  command, which resumes where the task stands.
- **anything else** - the command refused before or while running: outside Herdr, a task the
  tracker does not know, a dependency the list cannot satisfy (`After:` naming a task later in
  the list, or one outside it that is not finished). Print the message verbatim and stop.

If `f10` is not on `PATH`, or `f10 drive` is an unknown command, the binary is missing or older
than this plugin: say so, quote `f10 --version` where it ran, and point at
`go install github.com/amberpixels/f10/cli/cmd/f10@latest`.
