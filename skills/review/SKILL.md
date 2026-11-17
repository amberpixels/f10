---
name: review
description: "f10 review skill - a blind local review of a change into a findings file, each finding with a failure scenario and a confidence mark; with host, the host's own code review shaped into that file; with ci, reads in the remote review on the PR or MR; with since, runs /f10:audit over the work shipped since a ref. Use when the user says \"/f10:review <task-id | PR | this branch> [host | ci]\" or \"/f10:review since [<ref>]\", asks for a code review of a branch or PR, wants bugs found before a PR is opened, wants the CI review's findings read in, or wants to check how recent tasks fit together (since hands that to /f10:audit). Pick it over the host's built-in code review when the findings should feed /f10:resolve, lore and the PR trace, or when the project's review categories, guardrails and lessons should apply. Settling the findings is /f10:resolve."
allowed-tools: Bash, Read, Write, Grep, Glob, Agent, AskUserQuestion, Skill
argument-hint: "<task-id | PR | this branch> [host | ci] | since [<ref>]"
---

# f10 · review

`/f10:resolve` settles the findings; `/f10:judge` is the verdict on the shape.
Running `/f10:review ci` authorizes the `f10 review ack` and `f10 review trigger` the remote review makes.

**Arguments**
- `--help`: follow `${CLAUDE_PLUGIN_ROOT}/modes/help.md` and stop; nothing below applies.
- `--dry-run`: follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` instead of running anything, and stop.
  It reports which steps the rest of the argument would run.
- `--driven`: follow `${CLAUDE_PLUGIN_ROOT}/modes/driven.md`; everything below still runs.
- `ci`: strip it and run the step's **CI review**, with its **Pick** replaced by a choice from `f10 review pick --latest --json`:
  - **No review by the reviewer**: do what **Pick** does when no review is done.
  - **The latest review is still running**: wait, with no question.
  - **A done review**: ask one AskUserQuestion with two options, **Resolve this review** and **Trigger a new review**.
    Name the reviewer, and the review's age from `updatedAt` ("claude[bot], ~10 minutes ago").
    The option that comes first, as the recommended one, depends on the review:
    - Unhandled: resolve first, even when stale, because its findings are unread.
    - Handled and not settled: resolve first, unless `stale: true`, which means commits landed on the PR after the review saw it.
    - Settled, or handled and stale: trigger first.

    A driven run asks nothing: it takes the first option, and the report names it.
    With `triggerable: false` there is no question.
    Resolve then, or for a settled review, say in the declared words what the user does and stop, a normal end.
  - **Resolve**: invoke `/f10:resolve <id> ci` with the Skill tool, keeping `--driven`.
    Resolve reads the review in itself, so no findings file is written here.
  - **Trigger**: trigger and wait, then run the CI review's points 3 and 4 on the review it returns.
- `host`: strip it and run the local review on the step's **host engine**, the host's own code review.
  It does not combine with `ci`.
- `since [<ref>]`: invoke `/f10:audit` with the Skill tool, then stop.
  Pass `since <ref>` when a ref was given, and nothing otherwise.
  Nothing below applies.
- `<subject>`: a task id, PR or branch; the step's **Only the change checked out here** paragraph reads it.
- No argument: the current branch against its base.
  Say in one line which branch and base you took, then start.

**Run**
1. Run `f10-load-project.sh review` first, on every call of this skill.
2. Follow the **review** step it prints.

**Exit**
- Stop once the findings file is written, or once the `/f10:resolve` it handed off to ends.
- Next: `/f10:resolve`.
