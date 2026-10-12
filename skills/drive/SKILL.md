---
name: drive
description: "f10 drive skill - run a list of tasks through a chain of skills from this session, each in its own worktree and agent, dependents after their base, pausing only when an agent asks. Use when the user says \"/f10:drive <ids | id -- id> [skills...] [--model <model>]\", or asks to run several tasks through plan, judge, ship, review, resolve and finish without switching workspaces."
allowed-tools: Bash, AskUserQuestion
argument-hint: "<id>... [plan|judge|ship|review|resolve|finish ...]"
---

# f10 · drive

Run tasks through a chain from here: `f10 drive` opens each task as `f10 start` does, dependents
after their base, and prompts each agent one skill at a time, marked `--driven`
(`${CLAUDE_PLUGIN_ROOT}/modes/driven.md`). `--model` picks every agent's model, `<id>:<model>`
one task's, which wins. This skill only runs the command, relays its questions and prints its
report; it runs no step.

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- Everything else goes to `f10 drive` verbatim.

Run it **in the background** (`run_in_background`): a list can take hours, and the command's
exit wakes this session.

```
f10 drive <arguments>
```

When it exits, read its output and act on the exit code. On every exit, a line starting `!!!`
goes first, verbatim, above everything else.

- **0** - every task went through its chain. Print the report block (one row per task) in a
  fenced `yaml` block and any line beneath it, and stop.
- **4** - one or more agents asked. The output carries one `<id> asks at <skill>: ...` line per
  asking task: numbered questions, each with its options in brackets, the first the default.
  Put every asking task's questions to the user in **one `AskUserQuestion`**: one question per
  numbered item, its header carrying the task id, the bracketed options as the choices, the
  first marked recommended. Then run the output's `answer with:` line in the background, its
  placeholders filled with the answers, numbered the way each ask was. Repeat on every exit 4.
- **5** - a task halted; the lines beneath the report say which and why. Print the report block
  and those lines verbatim, and stop. The user settles it in that workspace, then reruns the
  same command, which resumes where each task stands.
- **anything else** - the command refused; its message names the cause. Print it verbatim and
  stop.

If `f10` is not on `PATH`, or `f10 drive` is an unknown command, the binary is missing or older
than this plugin: say so, quote `f10 --version` where it ran, and point at
`go install github.com/amberpixels/f10/cli/cmd/f10@latest`.
