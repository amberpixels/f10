---
name: read
description: "f10 read skill - load a task into this session and stop: its text, its comments and every image it links, opened and looked at, with no planning or work started. Use when the user says \"/f10:read <task-id>\", or asks to read, fetch, load or pull in a task, or to see what a task says, without doing anything about it yet."
allowed-tools: Bash, Read
argument-hint: "[<task-id> | <id>+<id>]"
---

# f10 · read

Read a task into the conversation: `f10 task read` writes it as markdown, downloads the images it links and names their local paths at the end.
This skill runs the command, opens every image, and stops (`${CLAUDE_PLUGIN_ROOT}/modes/binary.md`); it runs no step.

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- `<task-id>`, or a bundle `ABC-1+ABC-2`: goes to `f10 task read` verbatim.
- A `&& /f10:<skill> ...` tail is a chain: cut it from the command, and invoke that skill with the Skill tool after the report, its arguments verbatim (`--driven` kept).
- No argument: `f10 task read` reads the task out of the current branch.

1. `f10 task read <arguments>` in one Bash call.
2. **Open every image** each `Images:` line names (a bundle has one per member), with `Read`, all in one message.
   Images are part of the task: never leave one downloaded and unopened.
   Each `Images not downloaded:` line names the ones that could not be read; say so, one line each.
3. **Report** in a fenced `yaml` block, labels padded to one column.
   One task: `task:`, `title:` and `url:` rows (the id, the title as the tracker holds it, the bare url after `↗ `).
   A bundle: one row per member, the id as the label and `↗ <url>` as the value, titles in the lines beneath.
   Then what the task asks in two to four lines, folding in what the images show.
4. **Stop and wait**, unless a chain follows.
   Do not plan, judge, implement or offer a next skill: the user reads the task to talk about it, and says what comes next.
