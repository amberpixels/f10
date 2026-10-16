# Step · standup - a status report from a person's activity

Role: a colleague **writing a status report for a reader who was not there**.
Input: a period (the default, `--since <date>` or `--week`), the scope (this checkout, or `-C`, `--group`, `--all`) and whether to show `other`.
Output: the report, printed raw, a draft of it where a **Deliver** adapter applies, and the period marked as reported.
Context: the `standup.md` overlay of each project in the report, and the prose under `~/.f10/standup.md`'s frontmatter, the person's own.
All of it is read as instructions; there is no template.

**Which instructions apply** never depends on the directory the session runs in: an overlay is loaded for a project in the report, from that project's checkout, or not at all.

- **Wording, sections, audience and the Deliver adapter** belong to the person, since a standup does: a manager wants outcomes, a team wants PR links.
  Later wins: the overlay of the one project the report covers (only when it covers exactly one), then `~/.f10/standup.md`'s person-wide prose, then its section for the group the run names.
- **A group section** is a `## <name>` heading in that prose whose name is a key of the frontmatter's `groups:`.
  It runs to the next such heading or the end of the file, and any other heading inside it belongs to it, so the person-wide prose is everything above the first group heading.
  A section applies only when the scope is that one group alone: no other `--group`, no `-C`, no `--all`.
  Otherwise only the person-wide prose applies, and one line under the report names the group sections left out and why (several groups, extra checkouts).
- **The Assigned adapter** belongs to a tracker, so it is per project: each project's own overlay names how to list its open tasks, in any run.
  The person's prose may also name one for a project by its `name` in the JSON (or for every project, unnamed), and it wins over that project's overlay.
- **A Deliver adapter** is a skill, an MCP tool or a CLI command that creates a draft of the report (a Gmail draft to a given address, say).
  None picked: the report is printed and nothing else.

**The binary decides, the step renders.**
Grouping, states, work against upkeep and the days all come from `f10 standup --json` (schema in `docs/cli.md` § Standup).
Never re-read git or the host to second-guess them, and never fall back to doing so when the command fails: the same history always gives the same report.

1. **Collect**, in one call: the read time, the person's settings, then the report.

   ```
   date -u +%Y-%m-%dT%H:%M:%SZ; cat ~/.f10/standup.md 2>/dev/null; f10 [-C <checkout>]... standup --json [--since <date> | --week] [--group <name>]... [--all]
   ```

   No `~/.f10/standup.md` is normal: a project overlay alone applies, where the rule above allows one.
   The activity is under `projects`, one per tracker; a project's `repos` are the repositories it was read from.

2. **Load the overlays**, in one call: for each entry in `projects`, `(cd <its first repos[].checkout> && f10-load-project.sh --overlays standup)`, which prints that checkout's `standup.md` overlays, layered, and nothing else.
   A project whose checkout is this session's reuses the load the skill already ran.
   When no project in the report is this session's checkout, the overlay that load printed is ignored.
   A project with no overlay is normal.
   One project in the report: its overlay feeds wording and Deliver as well as Assigned; several: Assigned only.

3. **Resolve the tracker**, in one call: every task's url with `f10 -C <its project's first repos[].checkout> task open --print <id>`, and for a task whose `title` is empty, its tracker title from the first line of `f10 -C <that checkout> task read <id>`.
   A task whose url does not resolve renders unlinked and is named once under the report; it does not fail the run.
   PR urls are the JSON's `url`, never rebuilt.
4. **Render**, under a header naming the period from `since` and `period`:
   - **A section per entry in `days`**, oldest first, headed by its weekday and date.
     With more than one project, the section's lines go under each project's `name`, in the JSON's order; a project with several `repos` names each PR's `repo` beside its number.
     In each, for the tasks whose `days` carry that date:
     - a task whose class that day is `work`: one line, its id linked to the tracker, its title, then its PRs linked in the JSON's order (chain order).
       Its `state` goes on its last work day's line only: it is where the task ended the period, not where it stood each day;
     - every task whose class that day is `upkeep`: one line for all of them, such as `kept up to date: FT-12, FT-14 (in review)`.
       A task with no work day in the period carries its `state` in parentheses here, on its last day;
     - each reviewed PR whose `days` carry that date: one line, its number linked, its title and author.

     Without `--other`, a date with no task and no reviewed PR on it (only `other` activity) gets no section.
   - **`other`** only when asked for: one section after the days, its PRs linked and its commits counted.
   - **An empty period** (no `days`): one line saying nothing was recorded since `since`.
5. **Ask what is next**: one `AskUserQuestion`, `multiSelect`, offering
   - carry-over: the tasks whose `state` is not `shipped` or `closed`;
   - the open tasks assigned to the user, per project: through the **Assigned** adapter the rule above picks for it (the person's, else its overlay's), else `f10 -C <its first checkout> task search "is:open assignee:@me"` on a GitHub tracker, minus the carry-over;
     a project with neither (GitLab's search is text only, a driver may ignore the query) is named in one line saying only its carry-over is offered and why;
   - free text through Other.

   Four options per question, at most four questions, carry-over first, then assigned tasks by project in the JSON's order, each in its tracker's order; name what did not fit in one line.
   The answers are the report's **Next** section, each task linked as in point 4.
6. **Print** the whole report as one fenced `markdown` block, so it copies with its links intact.
   It is printed even where a Deliver adapter is named: it is the fallback a failed delivery points at.
7. **Deliver**, only where the rule above picks an adapter: a draft of the same report, Next section included, in the markup the adapter takes (HTML links for mail).
   - **Draft, never send.**
     Call only the adapter's draft-creating operation, never its send.
     An adapter that can only send (a `send_message` tool, a command that posts) is refused, as a fallback naming that it would send.
   - **Links come from the report**: the JSON's urls and point 3's tracker urls, never re-read from the delivered draft, since mail clients rewrite links.
   - **A skill or MCP adapter only delivers**: its input is the rendered report and the destination.
     A skill that collects its own activity (one reading git for a daily status) is not a delivery adapter: it would draft a different report than the one printed.
   - **On success**, a facts block per `conventions/report.md`: a `draft` row with the draft's url when the adapter returns one, else its id, the destination in prose beneath.
   - **A failure falls back**, it does not fail the run: a missing tool or skill, missing auth, or an erroring command.
     Report it in `conventions/failure.md`'s columns under the header `f10 · standup delivery fell back`, the error verbatim, `next` naming the fix (authenticate the MCP server, say), and point at the printed report as the text to send.
     Retry and substitution follow `failure.md` rules 3 and 4: a failed Gmail adapter is never retried through another mail tool.
     The badge stays as it was, and the period is still marked: the report was printed and read.
8. **Mark** the period as reported, only for the default period (`period` is `last_standup` or `last_working_day`):

   ```
   f10 standup mark --since <the JSON's since> --at <the read time>
   ```

   `--at` is the read time, so work done while answering point 5 falls in the next standup.
   `--since` and `--week` are look-backs and leave the stored standup alone.

**Driven** (`modes/driven.md`): no questionnaire.
The Next section lists the carry-over tasks and says assigned tasks were not offered; nothing becomes an ask.
A driven run never marks: nobody read the report, so moving the period would hide that work from the next one.
Nor does it deliver: the adapter is a tool the session may not have approved, and its permission dialog would stall a pane nobody watches; without a mark, each driven run would also draft the same period again.
One line under the report names the adapter that was not called.

**On failure:** `f10` is not on `PATH`, or `f10 standup` fails (no git `user.email`, a host CLI not logged in, an unreadable `~/.f10/standup.json`).
Report its stderr verbatim per `conventions/failure.md` and do not mark.
A Deliver adapter that fails is point 7's fallback, not a run failure.
A `mark` that fails after printing leaves the report valid: say so, with its stderr, and that the next standup starts where this one did.
