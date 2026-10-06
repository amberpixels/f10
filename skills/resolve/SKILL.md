---
name: resolve
description: "f10 resolve skill - settle the findings a review wrote: a verdict per finding (fix, skip or ask), fixes made and verified, every ask batched into one questionnaire. Use when the user says \"/f10:resolve <task-id | PR | this branch>\", asks to go through review findings, or wants a review's findings fixed or answered. Producing the findings is /f10:review."
allowed-tools: Bash, Read, Write, Edit, Grep, Glob, AskUserQuestion, Skill
argument-hint: "<task-id | PR number or url | this branch>"
---

# f10 · resolve

Settle what a review found: read the findings file by task, give every finding a verdict - fix,
skip or ask - make and verify the fixes, and put every ask to you in one questionnaire at the
end. Never guesses which reviewer wrote the file, and never reviews - that is `/f10:review`.

**Dry run:** if the argument contains `--dry-run`, follow
`${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` - load and report, execute nothing.

First load the context per `${CLAUDE_PLUGIN_ROOT}/conventions/context.md` - two calls:
`${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh` (skip if this context already holds the bundle),
then `${CLAUDE_PLUGIN_ROOT}/bin/bundle.sh review resolve`, every run - **both steps**: the
findings file's shape lives in the review step, and this one reads it from there. Then follow
the **resolve** step that bundle just printed - it carries the step file, so there is nothing
left to read from `steps/`.

**Route by the argument.** The change settled is always the one checked out here - the fixes
land in this working tree - so the argument names the task, never another tree:
- **A task id** (the project's id format, an id, or a tracker url): the findings under
  `<storage root>/reviews/<TASK-ID>/`, the task via the fetch adapter, and its plan when one
  exists. Its branch must be the current one; when it is not, say which worktree holds it
  (`git worktree list`), or that none does, and stop - a normal end.
- **A PR / MR** (number, url, or "this branch"): the same, with the task the head branch's name
  points to.
- **No argument**: the current branch. Do not ask which review - name the file in one line and
  settle it.

A task with no findings file is a normal end, said in one line with `/f10:review` as the next
step. A file whose findings all carry a verdict already is the same.

Stop once the verdicts are written and verify is green, and report per
`${CLAUDE_PLUGIN_ROOT}/conventions/report.md` - a `findings` row with the file's path, then the
verdict counts, what the fixes changed and which asks were answered, as prose. Nothing is
committed or pushed from here - that is the ship pipeline's, or your own next request. The one
thing that can leave the machine is a `+reply` on a review that lives on a PR or MR
(`gh pr comment` / `glab mr note`): running this skill does not authorize it, so the step asks
once before the first post, and the text reads as your own.
