---
name: catchup
description: "f10 catchup skill - bring the current branch up to date with its base: the branch `f10 start --after` recorded, else the default branch, by merge or rebase as the project declares, conflicts settled by keeping both sides' intent, verify run after. Use when the user says \"/f10:catchup\", asks to catch up with main, rebase or merge the base in, or is mid-merge or mid-rebase with conflicts to settle. Nothing is pushed."
allowed-tools: Bash, Read, Edit, Grep, Glob, AskUserQuestion
argument-hint: "(no argument - the base is the recorded dependency, else the default branch)"
---

# f10 · catchup

Bring the current branch up to date with its base and settle what that raises. The base is the
branch `f10 start --after` recorded, else the default branch. The strategy - merge or rebase -
is the project's to declare (`catchup: merge | rebase` under `project.md → Hosting & PR`);
undeclared, it is the rebase ship already does before implementing. Conflicts, textual and
semantic, are settled by keeping both sides' intent, never by picking a side blindly, and the
project's verify runs after. **Creates nothing** - no task, no plan, no tracker comment - and
**pushes nothing**. A catchup that finds no conflict, or nothing to bring in, is a correct run.

**Authorization:** running this skill is the explicit ask for the merge commit or the rebased
commits it produces, and for nothing else - the same way `/f10:ship` authorizes its push.

**Dry run:** if the argument contains `--dry-run`, follow
`${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md` - load and report, execute nothing.

First load the context per `${CLAUDE_PLUGIN_ROOT}/conventions/context.md` - two calls:
`${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh` (skip if this context already holds the bundle),
then `${CLAUDE_PLUGIN_ROOT}/bin/bundle.sh catchup`, every run. Then follow the **catchup** step
that bundle just printed - it carries the step file, so there is nothing left to read from
`steps/`.

**Driven:** if the argument contains `--driven`, follow `${CLAUDE_PLUGIN_ROOT}/modes/driven.md`
alongside the step. No `AskUserQuestion`: an ambiguous conflict becomes the run's one ask, as the
step's **Driven** paragraph says. `f10 drive` sends it this way to a dependent whose base it
just finished.

**No routing.** The subject is always the branch checked out here; a task id or a PR is not an
argument this skill takes, since the branch to catch up is the one the session stands on. Any
text the user adds is steering for the run - "take theirs for the lockfile", "ask me before
touching the migration".

**Three ends, all normal:** the base was already in the branch (nothing to bring in), the
integration was clean (no conflict), or conflicts were settled and verify is green. Each is
reported per `${CLAUDE_PLUGIN_ROOT}/conventions/report.md` with the base named. A dirty tree
is a refusal that names the files, and a conflict the diff cannot settle is one question to
the user, never a guess.

Where the run was reached from ship's failure report, say in one line that `/f10:ship <id>`
can be re-issued now that the branch is on its base.
