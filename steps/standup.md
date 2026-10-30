# Step · standup - a status report from a person's activity

Role: a colleague **writing a status report for a reader who was not there**.
Input: a period, a scope, and whether to show `other` activity.
The period is the default one, or the one `--since <date>` or `--week` names.
The scope is this checkout, or the checkouts `-C`, `--group` or `--all` name.
Output: the report printed raw, a draft of it where a **Deliver** adapter applies, and the period marked as reported.
Context: the `standup.md` overlay of each project in the report, and the person's own prose, which is everything in `~/.f10/standup.md` below its frontmatter.
Both are read as instructions.
There is no template.

**Which instructions apply** never depends on the directory the session runs in.
A project's overlay is loaded only for a project in the report, and only from that project's checkout.

- **The person's prose has two parts.**
  A group section is a `## <name>` heading whose name is a key of the frontmatter's `groups:`.
  It runs to the next such heading or to the end of the file, and any other heading inside it belongs to it.
  The person-wide prose is everything above the first group heading.
- **A Deliver adapter** is a skill, an MCP tool or a CLI command that creates a draft of the report, such as a Gmail draft to a given address.
  When none is picked, the report is printed and nothing else happens.
- **Wording, sections, audience and the Deliver adapter belong to the person**, because they depend on who reads the report: a manager wants outcomes, a team wants PR links.
  They come from three places, and a later one wins over an earlier one:
  1. the overlay of the project the report covers, only when the report covers exactly one project
  2. the person-wide prose
  3. the group section for the group the run names
- **A group section applies only when the scope is that one group alone**: no other `--group`, no `-C` and no `--all`.
  Otherwise only the person-wide prose applies.
  One line under the report then names the group sections left out and why, such as several groups or extra checkouts.
- **The Assigned adapter** names how to list a project's open tasks.
  It belongs to a tracker, so it is set per project: each project's own overlay names it, in any run.
  The person's prose may also name one, for a project by its `name` in the JSON, or for every project when it names none.
  The person's adapter wins over the project's overlay.
- So with one project in the report, its overlay feeds wording and Deliver as well as Assigned.
  With several, each project's overlay feeds Assigned only.

**`f10 standup` builds the report, and this step only renders it.**
The grouping, the states, the split between work and upkeep, and the days all come from `f10 standup --json`, whose schema is in `docs/cli.md` § Standup.
Never re-read git or the host to second-guess them.
Never re-read them as a fallback when the command fails either.

1. **Collect** the read time, the person's settings and the report, in one call:

   ```
   date -u +%Y-%m-%dT%H:%M:%SZ; cat ~/.f10/standup.md 2>/dev/null; f10 [-C <checkout>]... standup --json [--since <date> | --week] [--group <name>]... [--all]
   ```

   Having no `~/.f10/standup.md` is normal.
   A project overlay then applies alone, where **Which instructions apply** allows one.
   The activity is under `projects`, one entry per tracker.
   A project's `repos` are the repositories its activity was read from.

2. **Load the overlays**, in one call.
   For each entry in `projects`, run `(cd <its first repos[].checkout> && f10-load-project.sh --overlays standup)`.
   It prints that checkout's `standup.md` overlays, layered, and nothing else.
   A project whose checkout is this session's reuses the load the skill already ran.
   When no project in the report is this session's checkout, ignore the overlay that load printed.
   A project with no overlay is normal.
   Use each overlay as **Which instructions apply** says.

3. **Resolve the tracker**, in one call:
   - every task's url, with `f10 -C <its project's first repos[].checkout> task open --link <id>`
   - for a task whose `title` is empty, its tracker title, which is the first line of `f10 -C <that checkout> task read --no-images <id>`

   A task whose url does not resolve is rendered unlinked and named once under the report.
   It does not fail the run.
   PR urls are the JSON's `url`, and are never rebuilt.
4. **Render** the report under a header naming the period, taken from `since` and `period`.
   - **A section per entry in `days`**, oldest first, headed by its weekday and date.
     With more than one project, the section's lines go under each project's `name`, in the JSON's order.
     A project with several `repos` names each PR's `repo` beside its number.
     A section holds these lines for the tasks whose `days` carry its date:
     - **A task whose class that day is `work`** gets one line: its id linked to the tracker, its title, then its PRs linked in the JSON's order, which is chain order.
       A task with `riders` is a bundle, and its riders follow the title as `with FT-12, FT-13`, each linked.
       Its `state` goes only on the line of its last work day, because the state is where the task ended the period, not where it stood each day.
     - **Every task whose class that day is `upkeep`** shares one line, such as `kept up to date: FT-12, FT-14 (in review)`.
       A task with no work day in the period carries its `state` in parentheses on this line, on its last day.
     - **Each reviewed PR whose `days` carry that date** gets one line: its number linked, its title and its author.

     Without `--other`, a date with no task and no reviewed PR on it gets no section, since its only activity is `other`.
   - **`other`** appears only when asked for, as one section after the days, with its PRs linked and its commits counted.
   - **An empty period**, one with no `days`, gets one line saying nothing was recorded since `since`.
5. **Ask what is next**, in one `AskUserQuestion` with `multiSelect`.
   It offers:
   - the carry-over, which is the tasks whose `state` is neither `shipped` nor `closed`
   - the open tasks assigned to the user, per project, minus the carry-over
   - free text, through Other

   A project's assigned tasks come from the Assigned adapter that **Which instructions apply** picks for it: the person's, else its overlay's.
   With neither, a project on a GitHub tracker uses `f10 -C <its first checkout> task search "is:open assignee:@me"`.
   A project with no adapter and no such search gets one line saying only its carry-over is offered, and why.
   GitLab is one such case, because its search is text only, and so is a driver that may ignore the query.

   Put at most four options in a question, and at most four questions in the ask.
   The carry-over comes first, then the assigned tasks project by project in the JSON's order, each project's tasks in its tracker's order.
   Name what did not fit in one line.
   The answers become the report's **Next** section, each task linked as in point 4.
6. **Print** the whole report as one fenced `markdown` block, so it copies with its links intact.
   Print it this way even where a Deliver adapter is named.
7. **Deliver**, only where **Which instructions apply** picks an adapter.
   Deliver a draft of the same report, Next section included, in the markup the adapter takes, such as HTML links for mail.
   - **Draft, never send.**
     Call only the adapter's operation that creates a draft, never its send.
     An adapter that can only send, such as a `send_message` tool or a command that posts, is refused.
     The refusal is reported as a fallback, naming that the adapter would send.
   - **Links come from the report**: the JSON's urls and the tracker urls of point 3.
     Never re-read them from the delivered draft, because mail clients rewrite links.
   - **A skill or MCP adapter only delivers.**
     Its input is the rendered report and the destination.
     A skill that collects its own activity, such as one reading git for a daily status, is not a Deliver adapter, because it would draft a different report from the one printed.
   - **On success**, print a facts block per `conventions/report.md` with a `draft` row: the draft's url when the adapter returns one, else its id.
     Name the destination in prose beneath it.
   - **A failure falls back instead of failing the run.**
     A missing tool or skill, missing auth and an erroring command all fall back.
     Report the fallback in `conventions/failure.md`'s columns under the header `f10 · standup delivery fell back`, with the error verbatim and `next` naming the fix, such as authenticating the MCP server.
     Point at the printed report as the text to send.
     Retry and substitution follow `failure.md` rules 3 and 4, so a failed Gmail adapter is never retried through another mail tool.
     The badge stays as it was, and the period is still marked, because the report was printed and read.
8. **Mark** the period as reported, only for the default period, where `period` is `last_standup` or `last_working_day`:

   ```
   f10 standup mark --since <the JSON's since> --at <the read time>
   ```

   `--at` is the read time, so work done while answering point 5 falls into the next standup.
   `--since` and `--week` are look-backs, so they leave the stored standup alone.

**Driven** (`modes/driven.md`): there is no questionnaire, and nothing becomes an ask.
The Next section lists the carry-over tasks and says assigned tasks were not offered.
A driven run never marks, because nobody read the report.
It never delivers either, because the adapter's permission dialog would stall a pane nobody watches.
One line under the report names the adapter that was not called.

**On failure:** when `f10 standup` fails, report its stderr verbatim per `conventions/failure.md`, and do not mark.
It fails, for example, when the plugin's `f10` has no binary (it says so in one line), when git has no `user.email`, when a host CLI is not logged in, or when `~/.f10/standup.json` is unreadable.
A Deliver adapter that fails is point 7's fallback, not a run failure.
A `mark` that fails after the report printed leaves the report valid.
Say so, with its stderr, and say that the next standup starts where this one did.
