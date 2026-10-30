# Step · cleanup - comments cut to the why, prose rewritten for clarity, code leftovers removed, scope proven on a full pass

Role: a **senior engineer** preparing a change for someone who reads it cold.
Input: targets and a scope (below).
Output: the cleaned files in the working tree, verify green and nothing committed. The pipeline's next step, or the user, decides where they go.
Context: the loader's `verify:` line and `project.md → Guardrails`. The `cleanup.md` overlay adds the project's own rules, such as its comment conventions, what never to remove, more artifact patterns, typography, or keeping long dashes.
Badge: none. Inside a ship pipeline the leaf is `cleanup`.

**Targets.**
Cleanup has three targets:
- `comments` cuts source comments down to the why.
- `prose` rewrites markdown: skills, prompts, steps and docs.
- `code` removes development leftovers from source.

With no target named, the scope's files choose them: `code` and `comments` run when the scope holds source files, and `prose` runs when it holds markdown.
Named targets run in the order `code`, `comments`, `prose`, because `code` may delete a comment that `comments` would otherwise have rewritten.
A flow entry names its targets after a colon, as in `cleanup:comments,prose`.
A bare `cleanup` entry runs the default targets.
Bug hunting is not a target, because it belongs to review.

**Scope.**
The scope is the set of files cleanup may change.
- With no scope given, it is the current branch's change against its base, working tree included: `f10 pr diff`, or `f10 pr diff --name-only` for the file list.
  The untracked files that `git ls-files --others --exclude-standard` lists are in scope too.
  Only the lines the branch added or changed are rewritten, and older content in the same files stays as it was.
- A path, glob or file list puts those files in scope in full.
  A directory means every file under it, recursively.

Whatever the scope, cleanup skips generated, vendored, fixture and lock files, changelogs and verbatim quotes.

**Size.**
A small change gets the **light pass** (below), and every other run gets the full **Procedure**.
A change is small when all of these are true:
- The scope is the default one, the branch's change.
- No target was named, either in `/f10:cleanup comments` or in a flow entry such as `cleanup:comments,prose`.
- The scope holds under 200 lines after the skips.

To count the lines, add the `+` lines of `f10 pr diff` (not the `+++` headers) to every line of the untracked files.
The step's first line says which pass runs, with the count when there is one.

**Dashes.**
Every target follows this rule, unless the project's guardrails or overlay say otherwise.
A line the pass rewrites keeps no em or en dash, and neither does any line of a file given by path.
Replace each with a plain hyphen, or restructure the sentence where a hyphen reads badly.
Ranges become `1-10`, and arrows in prose become words.
Fenced code blocks and verbatim quotes keep their dashes.

## Light pass

1. **Read the diff** from `f10 pr diff` and the untracked files.
   Open the lines around a hunk only when the hunk needs context.
2. **Rewrite with the Edit tool** whatever the applicable targets' rules flag on the added or changed lines.
   The light pass never moves a rule between files.
   Giving a rule stated in several files its one home, and the search outside the scope that it needs, belong to the full pass.
3. **Fix the dashes.**
   Grep the changed files for em and en dashes, and fix those on the added or changed lines.
   In a source file, fix only those inside comments, because a dash in code stays.
4. **Skip the proofs.**
   The light pass takes no snapshot or baseline, reads no file in full, and runs no `f10-cleanup-check.sh`, inventory or rules subagent.
   A target's Baseline and Check belong to the full pass only.
   The `code` target still splits certain fixes from uncertain ones, and asks about the uncertain ones.
5. **Verify** as point 5 of the Procedure says.
6. **Report** in two lines.
   The first says what changed, or that nothing needed it.
   The second says the proofs were skipped for size, with the line count.
   Then add one line per `code` fix with its category, and one line per problem noticed and left alone, with its `file:line`.

## Procedure

The full pass runs whenever the light pass does not.

1. **Snapshot first.**
   Before the first edit, copy every file in scope into the scratchpad, keeping its relative path.
   Run the rest of the pass from the checkout root, and spell the paths the same way:

   ```bash
   SNAP=<scratchpad>/cleanup/snapshot
   mkdir -p "$SNAP" && for f in <files>; do mkdir -p "$SNAP/$(dirname "$f")" && cp "$f" "$SNAP/$f"; done
   ```

   Every proof below compares against this snapshot, never against HEAD, because in a pipeline the tree already holds implement's uncommitted work.
   Take the baseline the target names, if it names one.
   Then grep for em and en dashes, prose arrows and any other character the house style forbids.
2. **Read every file in full before the first edit.**
   A comment that looks redundant can be the only record of a trap, and a rule that looks repeated can carry an exception.
3. **Rewrite with the Edit tool**, one hunk at a time, by the target's rules below.
   Never use sed, perl or python on source or prose.
   The dash rule and any house-style typography rule apply to every changed line.
4. **Prove the scope** with the target's check below, and report its output.
   Revert every change the check flags, unless the target names it as an allowed exception.
5. **Verify** with the commands the load's `verify:` line names.
   Implement's skip applies here too: if this session already ran those commands green and nothing has touched the tree since, say so and move on.
   If verify is red, revert the hunk that broke it when you can identify it, and verify again.
   If it is still red, the step fails.
6. **Report** per `conventions/report.md`, with a prose section per target.
   Each section gives:
   - The baseline and final numbers, where the target has a baseline.
   - Each kind of change, with one before and after example.
   - Every exception the check allowed, and why.
   - Each problem noticed and left alone, with its `file:line`, because each one is a candidate task.
   - The output of the checks.

## comments

The `comments` target changes only comments.
Code problems found on the way go in the report, and the code stays untouched.

**Baseline:** the comment lines and total lines of each file, heaviest first.
If the repo has an earlier comment cleanup (`git log -i --grep=comment`), read one of its diffs first and match its voice.

**Keep**
- Every ticket, issue, PR or error-tracker reference.
  Move it into the shorter sentence.
- Every load-bearing warning: a trap, an invariant another file relies on, or a "do not" with its reason.
- Public API docstrings in full, with every option, default, mode and return value.
  Tighten the wording, but never the contract.
  If the code accepts an option the docstring misses, add it.
- A module header, where the directory has a header convention.
  Take its whys from the commit that added the file (`git log --format=%b -- <file>`), never from guesswork.

**Cut**
- Restated code, such as a comment saying what the next line visibly does.
- History and phase narration: "previously", "used to", "phase 2", "now that X landed", "we chose to".
- Measurements that go stale, such as timings or counts from one run.
  State the mechanism instead.
- Boilerplate pasted across files.
  Leave one line in each file, or none where a README already says it.
- A second paragraph inside one comment.
  It is usually a separate comment that belongs on another line.

**Shape**
- A comment states the why in 1 to 3 lines.
  It may take 4 where cutting would drop a reason, and 5 or more lines is a doc's job.
- Keep the file's existing comment width, and do not reflow comments you are not otherwise changing.
- A trailing comment may be reworded, but the code before it stays byte-identical.

**Check:** `f10-cleanup-check.sh comments "$SNAP" <changed files>`.
It lexes strings and block comments, so a comment marker inside a string still counts as code.
It prints three kinds of line:
- `CODE CHANGED: <file>` where the code differs.
  A dash and a hyphen count as equal here.
- `DASH IN CODE: <file>:<line>` for each dash swapped outside a comment.
- `UNCHECKED: <file>` for a syntax it does not know.

Read the diff of each flagged or unchecked file by eye, and revert the change to code.
A `DASH IN CODE` swap is reverted too, by default.
Keep it only where no test, parser or separator matches on the old text, and name it in the report.

## prose

The goal is prose that reads at least as easily as before and loses no rule.
The check for how it reads is `conventions/voice.md`, both its parts; this target does not repeat those rules.

The pass never sets a percentage or word target, and never reports a word count as a score.
It never shortens prose by packing sentences tighter or by dropping a reason, so a rewrite may come out longer.

Every rewrite keeps:
- Every rule, default, exception, path, command, flag, number and ticket reference, with the same meaning.
- Code blocks byte-identical.
- Headings and structure that tools or other files cite: frontmatter, step names, anchors.
- The file's list style and its layout of one sentence per line; no reflow of paragraphs you did not change.

It fixes:
- A sentence that breaks a rule in `conventions/voice.md`.
- A rule said twice, such as an aphorism that restates the rule before it, or a rule restated as its own reason.
- A rule stated in more than one file.
  Before editing, list those rules and find their copies outside the scope with a repo-wide grep on a distinctive phrase.
  Give each rule one home, the file that owns the concept, which keeps the full statement.
  A copy becomes a reference to it, or goes if the reader already loaded that file.
  Snapshot every outside file you will edit.

A worked example, from the plan step's rule for a small plan.
Before, one packed sentence:

```
- **small** - every condition holds: the cause or the one place to change is already named (a feature's in its task; a bug's or a refactor's in the task's _Root cause_, this run's debug brief, or one `file:line` the fetch brief traced); one area; no decision another competent engineer would take the other way; no gap.
```

After, one rule per sentence, the default first:

```
Most plans are normal: the full shape above.

A plan is small when the work is already decided, which means all of these are true:
- You know where the change goes.
- It touches one area.
- No other competent engineer would make a different call on it.
- There are no gaps.

If you are unsure about any of them, the plan is normal.
```

**Inventory as you go.**
After each file, append its rules to `<scratchpad>/cleanup/inventory.md` under the file's path: one line per must, never, only, exception and default, quoted from the snapshot, noting where a moved rule went.

**Check**, both every time:
- **Literals:** `f10-cleanup-check.sh literals "$SNAP" <every changed file>`.
  Every backtick span, fenced code line, path, `--flag` and multi-digit number in the snapshot must appear somewhere in the changed files (searched as a union, so a rule moved to its canonical home passes).
  Tokens dropped on purpose (a stale example, a duplicate) go in `$SNAP/.dropped`, one per line, and in the report; restore anything else.
- **Rules:** one fresh `general-purpose` subagent per run, or per about 15k words rewritten; never one per file, never a fork, so it judges blind.
  The Agent tool call carries the `model` and `effort` the load's `cleanup` profile line names (`conventions/context.md` → Agent profiles).
  Give it the inventory, the changed files, their snapshots and `conventions/voice.md`.
  It marks each inventory line `kept`, `changed` (quoting both) or `lost`, and each changed sentence that reads harder than its snapshot `harder` (quoting both), and returns only the lines not marked `kept`.
  Restore every `lost` line, fix or justify every `changed` one, and rewrite or revert every `harder` one.

## code

The `code` target removes development leftovers and leaves behavior unchanged.
It never renames across a public API, and it never changes logic.

Read the diff for five kinds of leftover:
1. **Artifact comments** - narration, phase markers, agent reasoning and mid-session breadcrumbs.
   Fix each one.
   A real concern left in one becomes a plain TODO.
2. **Dead code** - unused functions, variables and imports, commented-out blocks, and ignored arguments.
3. **Redundant indirection** - pass-through wrappers, chains with no transformation, and pointless intermediate variables.
4. **Pivot residue** - what a change of direction left behind: stale names, a second implementation nothing calls, always-true conditions, and shapes left from a renamed concept.
5. **Debug tooling** - debug logging, breakpoints and temporary lint suppressions.

Each fix is either certain or uncertain:
- A **certain** fix is one where nothing references the code, or the debug line is plainly temporary.
  Apply it.
- An **uncertain** fix is one where the code may be reached by reflection, is an exported name, or is a suppression that may be needed.
  Do not edit it.
  At the end of the step, put every uncertain item to the user in one `AskUserQuestion`, with a question per item and removal as the first option.

Report other quality problems, such as a swallowed error or a hardcoded value, and do not fix them.

**Check:** every changed hunk lies in a file in scope, and the report names each hunk with its category.
Green verify is the proof that behavior did not change.

**Driven** (`modes/driven.md`): the uncertain items become one `f10-state.sh ask` in place of the `AskUserQuestion`, and the certain fixes stay applied and verified.

**On failure:** the step fails when a flagged change cannot be reverted cleanly, when verify stays red, or when a `lost` rule cannot be restored (`conventions/failure.md`).
The report names the target and the file, and the other targets' finished work is kept.
