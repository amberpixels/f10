# Step · review-host - the local review on the host's own code review

Input: the facts the review step's **The blind reviewer** lists, which the main agent gathers first.
Output: the findings file and its lore entries.
Badge: none.

The host is the agent running the session: in Claude Code, its built-in `code-review` skill, which verifies each finding before it reports it.
It takes the blind reviewer's place in rounds, resolve, lore and the state block.

1. **Invoke** the host's `code-review` skill with the Skill tool, from the main agent, because the skill's higher levels spawn reviewer agents and a spawned agent may not be allowed to.
   Hand it nothing beyond its arguments, no plan and no facts, so it reviews the diff exactly as it would when the user runs it.
   Whatever part of the review the skill runs inline still shares the main agent's context, which in a ship flow holds the plan and the implementation.
   So a project that wants a reviewer that never saw the session keeps `review:local`.
   Its arguments are a level, a findings bound and a target:
   - **Level**: `high` by default.
     A `Host level:` line in `project.md → Review` overrides it.
     Its value is one of `low`, `medium`, `high`, `xhigh` or `max`.
     Always pass it, because with none the skill reuses the level the user typed last.
     Never pass `ultra`, because that is a billed cloud review only the user launches.
   - **Findings bound**: always `--max-findings all`, because the skill otherwise keeps the cap the user chose last, and point 2 applies its own cap.
     The skill keeps the level and the bound for the user's next `/code-review`, and the report says so in one line.
   - **Target**: the PR number when `f10 review round` prints one on `pr:`, because the PR's target branch is the base.
     With no open PR it is the current branch.
     The skill then diffs against the default branch, so when `f10 pr base` names another branch, the report says so in one line.

   Never pass `--fix` or `--comment`: resolve makes the fixes, and a comment would leave the machine before resolve read it.
2. **Shape** each finding the skill reports through `ReportFindings` into one block:
   - the title is its short summary, else its summary
   - `summary:` is its summary
   - `where:` is its `file:line`
   - `failure:` is its failure scenario
   - `confidence:` is `confirmed` for `CONFIRMED`, and `plausible` for `PLAUSIBLE` or no verdict
   - `category:` maps its category onto the core categories:
     - security and performance findings go to **security and performance**
     - tests and docs go to **tests and docs**
     - simplification and reuse go to **architecture and guidelines**
     - efficiency goes to **security and performance** when it names a runtime cost, and to **architecture and guidelines** otherwise
     - the rest go to **correctness and logic**
     - a finding on a project category's subject goes to that category, whatever the skill called it

   The skill ranks findings most severe first, so a cap of three per category keeps the first three.
   **Never flag** items are dropped.
   A run that reports no findings is recorded as a `clean` round.
   The header says `reviewer: local`, because the findings parser reads any other name as a remote reviewer, and adds `engine: host <level>`.
3. **Round two** writes `## Prior round` before the skill runs, so the agent that made the fixes does not judge them.
   One fresh `general-purpose` agent writes it: it runs `f10-load-conventions.sh` and `f10-load-project.sh review review-local` as two Bash calls, and follows point 5 of that brief.
4. **Lore** is written as the review step's **Lore and the state block** says, with `--source local`.

**On failure:** no `code-review` skill in the session, or a run that ends without `ReportFindings`, fails the entry (`conventions/failure.md`).
It never falls back to the blind reviewer, because the flow declared the host.
