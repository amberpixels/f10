# Skills and steps

A **skill** (`skills/<name>/SKILL.md`) is what Claude Code registers and the user invokes as
`/f10:<name>`. A **step** (`steps/<name>.md`) is a unit of work, reached only through
`bin/bundle.sh`. A skill runs zero or more steps; a step can be run by several skills and by the
ship pipeline, and a project can extend it with an overlay (`.f10/instructions/<step>.md`). A
skill cannot be extended.

## The rule

**A skill owns the invocation; a step owns the work.**

| Skill | Step |
|---|---|
| The argument: flags to strip, the no-argument default, which steps to run and in what order | Role, input, output, the procedure |
| Context loading (`conventions.sh`, `bundle.sh <steps>`) | What the step produces, and nothing else |
| Modes: `--help`, `--dry-run`, `--driven`, forward | How the step behaves in a pipeline or a mode, where it differs |
| Authorization: what running this skill permits (a push, a PR, a merge commit) or must ask for | That an action is outward-facing and needs the run's authorization |
| The session: stay in the discussion, offer the next `/f10:*` skill, never roll on into another | What counts as failure, and which ends are normal |
| The `description` frontmatter, which routes the invocation before any step is loaded | |

A skill says which step to follow and, at most, one line of scope or authorization. It never
re-describes what the step does, what it creates, or its normal ends. A step never names the
skill that runs it, except where its behavior differs by caller.

## Layout of a skill file

1. **Frontmatter.** `description` says what the skill does and when to call it. `argument-hint`
   is the common call only, under about 50 characters, without `--help`, `--dry-run` or
   `--driven`; `--help` shows the rest.
2. **`# f10 · <name>`.**
3. **At most one line of scope or authorization** the description cannot carry ("Runs the
   capture step only", what running the skill authorizes). Never the description restated. A
   skill that only runs the binary says what the command does here.
4. **`**Arguments**`**, a list, before anything loads: `--help`, `--dry-run`, the skill's other
   flags as `` `token`: effect ``, then one item per case of what remains: a `<placeholder>`
   item per positional argument, then a **No argument** item: a default such as the current
   branch, or, where none exists, sub-items split by context (the conversation's open
   question; nothing open, so ask in one line and wait). Read the argument by what it says,
   not by whether there is one: "let's judge something" names no subject and counts as none.
   A mode item says where the run ends, read top to bottom like early returns: `... and stop`
   (`--help`, `--dry-run`, forward when it forwards), or `everything below still runs` (`--driven`).
   Every token in `argument-hint` has an item here; `--help` prints this list.
5. **Load**: `conventions.sh`, `bundle.sh <steps>`.
6. **The skill's own content.**
7. **`**Exit**`**, a list: where the run ends (stop at an artifact, or stay in the conversation)
   and, where one follows, a `Next:` skill with its condition. `conventions/report.md` says how
   it is offered. Skills that only run the binary keep their ending in the procedure, since it
   follows the command's exit codes.

## Edge cases

- **Routing the subject goes in the step** when a pipeline also runs that step (judge, review,
  resolve, demo): in a pipeline there is no skill to route it. The skill keeps only the flags and
  the no-argument default.
- **Authorization is granted once, in the skill.** The step says the action is outward-facing;
  `ship/SKILL.md` grants the push and the PR, `resolve/SKILL.md` asks before a `+reply`.
- **Logic two skills share goes in a mode** (`modes/forward.md`), not copied into both.
- **A skill that needs part of another step's work** runs those points of that step and names
  where it differs (ship runs `steps/catchup.md` points 2 and 3, failing on a conflict).

## Where a line belongs

Ask: if a second skill ran this step, would the line still hold? Yes: the step. No: the skill.
brainstorm's `Next: /f10:capture, once a shape is settled` fails that test (a skill that runs
brainstorm, then capture, would drop it), so it goes in the skill. "It writes nothing" holds for any caller,
so it goes in the step.
