# Step · cleanup - comments, prose or code tightened, scope proven

Role: a **senior engineer** preparing a change for someone who reads it cold.
Input: targets and a scope (below).
Output: the cleaned files in the working tree, verify green, nothing committed; the pipeline's next step or the user decides where they go.
Context: `project.md → Verify` and `→ Guardrails`; the `cleanup.md` overlay adds a project's rules (its comment conventions, what never to remove, more artifact patterns, typography, or keeping long dashes).
Badge: none; inside a ship pipeline the leaf is `cleanup`.

**Targets.**
`comments` (source comments cut to the why), `prose` (markdown: skills, prompts, steps, docs), `code` (development leftovers in source).
Named ones run in that order: `code`, `comments`, `prose`, since `code` may delete a comment `comments` would have rewritten.
None named: `code` and `comments` when the scope holds source files, `prose` when it holds markdown.
A pipeline entry names targets in parentheses, `cleanup (comments, prose)`; bare `cleanup` takes the default.
Bug hunting is not a target: it stays with review.

**Scope.**
- A path, glob or file list: those files in full; a directory means its files recursively.
- None: the current branch's change against its base (the branch `f10 start --after` recorded, else the default branch), working tree and untracked files included.
  Only lines the branch added or changed are rewritten; older content in the same files stays as it was.

Skip generated, vendored, fixture and lock files, changelogs and verbatim quotes.

**Dashes**, every target, unless the project's guardrails or overlay say otherwise.
No em or en dash on a line the pass rewrites, or anywhere in a file given by path: a plain hyphen, or the sentence restructured where a hyphen reads badly.
Ranges become `1-10`; arrows in prose become words.
Fenced code blocks and verbatim quotes keep theirs.

## Procedure

1. **Snapshot first.**
   Copy every file in scope into the scratchpad before the first edit, keeping its relative path, and run the rest from the checkout root with the same path spelling:

   ```bash
   SNAP=<scratchpad>/cleanup/snapshot
   mkdir -p "$SNAP" && for f in <files>; do mkdir -p "$SNAP/$(dirname "$f")" && cp "$f" "$SNAP/$f"; done
   ```

   Every proof below compares against this snapshot, never HEAD: in a pipeline the tree already holds implement's uncommitted work.
   Take the baseline the target names, and grep for em and en dashes, prose arrows and any other character the house style forbids.
2. **Read every file in full before the first edit.**
   A comment that looks redundant can be the only record of a trap; a rule that looks repeated can carry an exception.
3. **Rewrite with the Edit tool**, one hunk at a time, by the target's rules below.
   Never sed, perl or python on source or prose.
   The dash rule and any house-style typography rule apply to every changed line.
4. **Prove the scope** with the target's check below and report its output.
   A change the check flags is reverted, unless it is an allowed exception the target names.
5. **Verify** per `project.md → Verify` (implement's skip for an unmoved tree holds).
   Red: revert the hunk that broke it when it is identifiable, re-verify, and fail if still red.
6. **Report** per `conventions/report.md`, prose per target: the baseline and final numbers, the kinds of change with one before/after example each, every exception the check allowed and why, problems noticed and left alone with `file:line` (each a candidate task), and the checks' output.

## comments

A comment-only change.
Code problems found on the way go in the report, untouched.

Baseline: comment lines and total lines per file, heaviest first.
If the repo has an earlier comment cleanup (`git log -i --grep=comment`), read one of its diffs first and match its voice.

**Keep**
- Every ticket, issue, PR or error-tracker reference, moved into the shorter sentence.
- Every load-bearing warning: a trap, an invariant another file relies on, a "do not" with its reason.
- Public API docstrings in full: every option, default, mode and return value.
  Tighten wording, never the contract; add an option the code accepts and the docstring misses.
- A module header where the directory has a header convention; its whys come from the commit that added the file (`git log --format=%b -- <file>`), never guesswork.

**Cut**
- Restated code: a comment saying what the next line visibly does.
- History and phase narration: "previously", "used to", "phase 2", "now that X landed", "we chose to".
- Measurements that go stale (timings, counts from one run); state the mechanism.
- Boilerplate pasted across files: one line in each, or none where a README already says it.
- A second paragraph inside one comment; it is usually a second comment on another line.

**Shape**
- 1 to 3 lines stating the why; 4 where cutting would drop a reason; 5 or more is a doc's job.
- The file's existing comment width; no reflow of comments you are not otherwise changing.
- A trailing comment may be reworded; the code before it stays byte-identical.

**Check:** `f10-cleanup-check.sh comments "$SNAP" <changed files>`.
It lexes strings and block comments, so a marker inside a string stays code.
It prints `CODE CHANGED: <file>` where code differs (a dash and a hyphen count as equal), `DASH IN CODE: <file>:<line>` for each dash swapped outside a comment, and `UNCHECKED: <file>` for a syntax it does not know.
Read a flagged or unchecked file's diff by eye and revert the change.
Keep a `DASH IN CODE` line only where nothing matches the old text (a test, a parser, a separator) and name it in the report; otherwise revert it.

## prose

Every sentence must change what the reader, a model or a person, does.
Say each thing once, plainly, where it applies.

Baseline: word count per file.
Instruction prose (skills, prompts, steps, conventions) can usually lose 40 to 50%; READMEs and docs keep their reasons and examples, and 20 to 30% is normal.
These are guides, not gates.

Before editing, list the rules stated in more than one file, find copies outside the scope with a repo-wide grep on a distinctive phrase, and give each rule one canonical home: the file that owns the concept.
Snapshot every outside file you will edit.

**Keep**
- Every rule, default, exception, path, command, flag, number and ticket reference: shorter wording, same meaning.
- Code blocks byte-identical.
- A reason where the reader would otherwise break the rule in a case it does not name, in one clause.
- Headings and structure that tools or other files cite: frontmatter, step names, anchors.

**Cut** the nine forms in `conventions/voice.md`, plus:

10. **Aphorism after a rule** - the rule already said it.
11. **Rule restated as its reason** - "Do X. Not doing X is the failure this prevents."
12. **Design justification** where the reader only needs what to do; it belongs in a commit message or a design doc.
13. **Repeated rules** - the canonical home keeps the full statement; a copy becomes a reference or goes, if the reader already loaded the canonical file.
14. **Lists enumerating what one noun covers**, and tricolons where one item carries the meaning.

**Shape**
- Sentences under 25 words.
- Explain fully what needs explaining, then stop: a rule the reader can no longer apply is a loss, not a cut.
- The file's wrap width and list style; no reflow of paragraphs you did not change.

**Inventory as you go.**
After each file, append its rules to `<scratchpad>/cleanup/inventory.md` under the file's path: one line per must, never, only, exception and default, quoted from the snapshot, noting where a moved rule went.

**Check**, both every time:
- **Literals:** `f10-cleanup-check.sh literals "$SNAP" <every changed file>`.
  Every backtick span, fenced code line, path, `--flag` and multi-digit number in the snapshot must appear somewhere in the changed files (searched as a union, so a rule moved to its canonical home passes).
  Tokens dropped on purpose (a stale example, a duplicate) go in `$SNAP/.dropped`, one per line, and in the report; restore anything else.
- **Rules:** one fresh `general-purpose` subagent per run, or per about 15k words rewritten; never one per file, never a fork, so it judges blind.
  Give it the inventory and the changed files; it marks each line `kept`, `changed` (quoting both) or `lost` and returns only the non-kept lines.
  Restore every `lost` line; fix or justify every `changed` one.

## code

Development leftovers removed, behavior unchanged: never a rename across a public API, never a logic change.

Read the diff for:
1. **Artifact comments** - fix narration, phase markers, agent reasoning, mid-session breadcrumbs.
   A real concern left in one becomes a plain TODO.
2. **Dead code** - unused functions, variables and imports; commented-out blocks; ignored arguments.
3. **Redundant indirection** - pass-through wrappers, chains with no transformation, pointless intermediate variables.
4. **Pivot residue** - stale names, a second implementation nothing calls, always-true conditions, shapes left from a renamed concept.
5. **Debug tooling** - debug logging, breakpoints, temporary lint suppressions.

**Certain** fixes (nothing references the code, the debug line is plainly temporary) are applied.
**Uncertain** ones (reached by reflection, an exported name, a suppression that may be needed) are not edited: they go to the user in one `AskUserQuestion` at the end of the step, a question per item, removal first.
Other quality problems (a swallowed error, a hardcoded value) are reported, not fixed.

**Check:** every changed hunk lies in a file in scope, and the report names each with its category.
Verify green is the behavior proof.

**Driven** (`modes/driven.md`): the uncertain items become one `f10-state.sh ask`, and the certain fixes stay applied and verified.

**On failure:** a flagged change that cannot be reverted cleanly, verify that stays red, or a `lost` rule that cannot be restored (`conventions/failure.md`).
Name the target and the file; keep the other targets' finished work.
