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
| Modes: `--dry-run`, `--driven`, forward | How the step behaves in a pipeline or a mode, where it differs |
| Authorization: what running this skill permits (a push, a PR, a merge commit) or must ask for | That an action is outward-facing and needs the run's authorization |
| The session: stay in the discussion, offer the next `/f10:*` skill, never roll on into another | What counts as failure, and which ends are normal |
| The `description` frontmatter, which routes the invocation before any step is loaded | |

A skill says which step to follow and, at most, one line of what the skill is for. It never
re-describes what the step does, what it creates, or its normal ends. A step never names the
skill that runs it, except where its behavior differs by caller.

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
"Never roll on into capturing" fails that test (a skill that runs brainstorm, then capture,
would drop it), so it goes in the skill. "No code, no plan file, no tracker item" holds for any
caller, so it goes in the step.
