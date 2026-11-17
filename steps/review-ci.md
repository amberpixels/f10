# Step · review-ci - the remote review read into the findings file

Input: the review that arrived on the PR or MR, so the CI review runs after `pr`.
Output: the findings file and its lore entries, with the review marked handled.
Badge: none.

It runs as `review:ci` in a flow, as the CI part of `review:local+ci`, and standalone as `/f10:review ci`, whose skill replaces **Pick** with a question.
`/f10:resolve ci` runs its points 3 and 4.

## The protocol

The frontmatter's `review:` facts say who posts the review, where it arrives, what triggers it, and what marks it done, handled and settled.
`f10 review pick|wait|ack|settle|trigger` applies them over `gh` or `glab`, and refuses a value outside their vocabulary (`docs/project-setup.md` → Review).
When the facts are missing or the host refuses them, the entry fails before any reviewer is spawned, because there is nothing to wait for.
Three of them change what this step does:

- **trigger** is `automatic`, `manual` followed by how, or both.
  An automatic trigger fires on open or on push, and a manual one reads like `manual, comment \`@claude\` on the PR`.
  `f10 review trigger` performs a manual how written as a comment or a review request, and pick's JSON then shows `triggerable: true`.
  Any other how is a step a human takes, told to the user in the declared words.
- **handled** means a findings file read the review in, which `f10 review ack` marks.
  A check run has no remote handled mark, and a new push starts a new check run.
- **settled** is optional: the mark resolve leaves once every finding has a verdict.
  A settled review counts as handled.

**Wait** is `f10 review wait --budget 8m`, run once more if it expires, and then a failure.
Keep the Bash timeout above the budget.

**Trigger and wait** runs `f10 review trigger`, which prints a time, what it did, and the url.
Then wait with `--since <that time>`, and skip point 2, because a review that postdates the trigger is proven new.
A trigger can be refused, when the trigger is automatic only or its how is one `f10 review trigger` cannot read.
Print the refusal in one line, name the declared words when there are any, and stop, a normal end.

## The remote review

1. **Pick.**
   `f10 review pick --json` prints the latest unhandled review by the declared reviewer, and never a settled one.
   A done review is the review, even when `stale: true`.
   Print one line naming its sha and the commits since (`git rev-list --count <sha>..HEAD`), and go on, because a CI that reviews only on open never reviews a later push.
   When no review is done, the trigger decides:
   - **automatic**: wait.
   - **automatic, manual**: wait one budget, and when it expires, trigger and wait.
   - **manual** and triggerable: trigger and wait now.
   - **manual** and not triggerable, which pick shows as `f10 never performs it`: say in the declared words what the user does, and stop.
     In a ship run that is not driven, ask once (AskUserQuestion) whether they triggered it, and wait only on yes.
     In a driven run, that question is the run's one ask (`modes/driven.md`).

   Round two of an entry reaches the CI the same way: an automatic trigger fires on resolve's push, and a manual one is posted as the re-review comment.
2. **Already written?**
   A picked sha is already written when it equals the `reviewed:` sha of the `latest:` file `f10 review round` prints.
   Say so in one line and stop, a normal end.
   This rule holds for every arrival kind, a check run included, so pick's `stale` never changes it.
3. **Shape it into the findings file**: one block per finding, with `where:` from its `path:line`, `category:` mapped to the core or project categories, and its fix in the body.
   When the review gives a finding's failure scenario or its confidence, they become the block's `failure:` and `confidence:` lines, and otherwise the block leaves them out.
   A review that raised nothing is `clean`.
   `reviewed:` is pick's sha, `reviewer:` is the declared identity, `review:` is the picked url, and `revision:` is pick's revision.
   In round two, a prior finding the remote no longer raises is `fixed` if the code shows it fixed, else `still open`.
   If the review's body is not in the findings shape, say once that the review-local step's brief can be pasted into the workflow, and do not print the brief.
4. **Ack.**
   Write the lore entries with `--source remote`, then run `f10 review ack --id <id>` with pick's id.
   The ack marks the review handled, so a run that dies in resolve never picks it again.
   Handled means consumed, not settled: resolve's **CI review** point 5 leaves the settled mark.
   Ack and trigger are outward-facing, and run under the run's authorization.
   In stealth mode they read as the user's own.

## Local and CI

**`review:local+ci`** writes the local and the CI review into one round file, which one resolve settles, with one commit and one report.
The CI review is triggered or awaited first, so it runs while the local reviewer reads.

1. **Pick** with `f10 review pick --json`, and when it says to trigger, trigger now and keep the time it prints.
2. **The blind reviewer** writes the round file, told that the CI review joins the round.
3. **The CI review** then runs points 1 to 3 into the same file.
   It never triggers twice, and a review triggered in point 1 is awaited with `--since` the kept time.
   Its blocks go under `## Findings`, numbered on from the local ones and tagged `source: remote`, and their counts are added to `## Categories`.
   `verdict:` becomes `findings` when either review raised one.
   The header's `reviewer:` lists both reviewers, and `review:` and `revision:` are the CI review's.
   It adds no prior blocks, because the blind reviewer wrote every one, so point 3's "no longer raises" rule does not apply.
4. **Ack** as point 4 says.

A stop in **Pick** or **Already written?** ends the CI review only: a manual trigger nobody can fire, a refused trigger, or a review already written.
The header then keeps `reviewer: local`, the resolve covers the local blocks, and the report says the CI review was skipped and why.
