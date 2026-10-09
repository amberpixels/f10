---
name: catchup
description: "f10 catchup skill - bring the current branch up to date with its base (the branch `f10 start --after` recorded, else the default branch), conflicts settled keeping both sides' intent, verify run after. Use when the user says \"/f10:catchup\", asks to catch up with main, rebase or merge the base in, or is mid-merge or mid-rebase with conflicts to settle. Nothing is pushed."
allowed-tools: Bash, Read, Edit, Grep, Glob, AskUserQuestion
argument-hint: "(no argument - the base is the recorded dependency, else the default branch)"
---

# f10 · catchup

Bring the current branch up to date with its base (the branch `f10 start --after` recorded,
else the default branch) and settle what that raises, keeping both sides' intent. **Creates
nothing** and **pushes nothing**. Running this skill authorizes the merge commit or rebased
commits it produces, nothing else.

**Dry run:** with `--dry-run` in the argument, follow `${CLAUDE_PLUGIN_ROOT}/modes/dry-run.md`.

Load the context (`${CLAUDE_PLUGIN_ROOT}/conventions/context.md`):
`${CLAUDE_PLUGIN_ROOT}/bin/conventions.sh` unless this context already holds it, then
`${CLAUDE_PLUGIN_ROOT}/bin/bundle.sh catchup` every run. Follow the **catchup** step it prints;
read nothing from `steps/`.

**Driven:** with `--driven` in the argument, follow `${CLAUDE_PLUGIN_ROOT}/modes/driven.md`
and the step's **Driven** paragraph. `f10 drive` sends it this way to a dependent whose base it
just finished.

**No routing.** The subject is always the branch checked out here; the skill takes no task id or
PR. Any text the user adds is steering ("take theirs for the lockfile", "ask me before touching
the migration").

**Three ends, all normal:** nothing to bring in, a clean integration, or conflicts settled with
verify green. Report each per `${CLAUDE_PLUGIN_ROOT}/conventions/report.md` with the base named.

Where the run was reached from ship's failure report, say in one line that `/f10:ship <id>`
can be re-issued now that the branch is on its base.
