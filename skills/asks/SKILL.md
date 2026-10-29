---
name: asks
description: "f10 asks skill - the questions driven agents are waiting on, across projects, put to the user in one questionnaire and answered: a drive's task resumes its drive, any other agent gets the answer as its next prompt. Use when the user says \"/f10:asks\", asks what agents are waiting on or asking, or wants to answer the open asks the status line counts."
allowed-tools: Bash, AskUserQuestion
argument-hint: "[task-id]"
---

# f10 · asks

Driven agents' questions wait in one per-user inbox until the first answer from any way in (`docs/cli.md` → Asks).
This skill reads the inbox, asks the user, and writes the answers (`${CLAUDE_PLUGIN_ROOT}/modes/binary.md`); it runs no step.

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- A task id (or `<project>:<task>`): only that task's ask.
- No argument: every open ask.

1. Run `f10 asks list --json`.
   `open` empty, or no ask for the task named: say so in one line, naming any `answered` row for that task (who answered, when), and stop.
2. **Ask once, in one `AskUserQuestion`**, a question per numbered question of each open ask, in the list's order.
   Header: the task id (`<project>:<task>` when two asks share an id).
   Question: the question's text, with the task and the skill it asked at.
   Options: the question's `options` as given, the first marked recommended; more than four shows the first four, the rest reachable through Other.
   A question with no options offers its text's evident default if it names one, else a single option `answer in Other`, and the user types the answer.
   Past four questions, ask the rest in a further `AskUserQuestion` of up to four.
3. **Write the answers**, every ask in one Bash call, one command per ask:

   ```
   f10 asks answer <task> --by skill 1=<option or text> 2=<option or text>
   ```

   Pass each answer as the option's text, quoted; free text the user typed goes in as typed.
   An answer refused because another way in answered first (`already answered ...`) is a normal end for that ask: name who answered.
4. **Report** per `${CLAUDE_PLUGIN_ROOT}/conventions/report.md`: one row per task answered, the id as the label and what was sent as the value, plain-fenced if a value would break the highlighting.
   Under it, one line per task saying where the answer went, as the command printed it (sent to the agent, drive `<n>` resumed, or left to a drive still running).
