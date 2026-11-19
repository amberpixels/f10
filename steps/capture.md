# Step · capture - from a description to a task

Input: a free-text description of a feature or a bug, or a debug brief.
Context: `project.md → Tracker`, for the id format and the **Properties** a new task carries.

1. **Scope it yourself**, and ask only when the description is too thin to scope.
   Pieces that each ship on their own become a **parent** plus one **subtask** per piece, unless the user asks for one task.
2. **Draft each task.**
   - **Title:** short and imperative.
     A task from a debug brief names the defect, not the fix.
   - **Body**, sized by what the reader needs to act, not by the size of the change:
     - **small** - one place, nothing to decide: a couple of paragraphs, no headings
     - **normal** - several places or one decision: the problem, the scope and an _Areas_ note
     - **large** - cross-cutting or worth fencing off: normal, plus _Acceptance criteria_ and _Out of scope_

     A heading goes only over what the sentences above it do not already say.
     A parent holds the problem, the settled shape and what is out of scope.
   - **_Areas_** names the parts of the codebase touched, such as UI, schema or auth, because later steps pick roles from it.
   - **A debug brief** makes the task at least normal, and its _Reproduction_ and _Root cause_ go in as written, the `Reproduction: <outcome>` line included, so a later pass can trust them.
   - **Altitude:** write for the engineer who picks it up.
     A snippet goes in only when its shape is the ask, reproduction stops at what reproduces it, and there are no estimates.
   - **Other tasks** named in the body carry their titles from one `f10 task ref` call (`conventions/context.md` → Task references).
     `f10 task create` links a first mention still bare, but never the title line, because a title renders nothing.
     A closed task the description asks to change gets a new follow-up task, never its own body.
   - **A dependency** is the body's last line, `After: <id>`, written only when the order is known.
     Check with a fresh `f10 task ref` that the task is open, because a `[closed]` one gets no line and becomes shipped work in the prose.
     Tasks meant to run in sequence are created in that order.
   - On GitHub and GitLab, each paragraph is one line, because a single newline renders as `<br>`.
3. **Create it** with `f10 task create`, the draft on stdin with `# <title>` first, and these flags:
   - `--status` and `--priority`
   - `--label` and `--set <key>=<value>`, once each
   - `--parent <id>` for a subtask

   It prints the new id, and `f10 task open <id> --link` prints the url.
   - **Exit 3** means nothing here creates tasks: load `f10-load-project.sh capture-adapter` and create through the project's create adapter.
   - **A failure that says the task may exist** is never re-run, and the user checks the tracker.
     Any other failure wrote nothing, so fix the input and run it again.
   - **Labels**: a bug or a refactor gets its kind label, always for a debug brief.
     A spike gets its label only when the user calls it one, because that label alone makes ship skip the PR.
     Add the project's default status and labels.
   - **Properties** declared under `project.md → Tracker` (a capture overlay may add some) are chosen from the draft by each entry's rule.
     A property the draft does not settle stays unset, because a wrong value misfiles the task.
     A warning that a `--set` was dropped is retried with `f10 task update <id> --set <key>=<value>`.
   - **A split** creates the parent first, and each subtask with `--parent`.
     An existing task joins with `f10 task update <id> --parent <parent>`.
     Then check `f10 task children <parent>`, and on exit 3 give the parent a checklist of its subtasks.
     A create warning that the host refused the parent link (GitLab) gets the checklist too.
     Any other subtask missing from the rows is named in the report, for the user to link.
   - **A claimed regression** ("regression of FT-414") is recorded once the task exists: `f10 task regression <new-id> --claimed <id> --text "<the phrase>"`.
     It is the user's word, kept apart from what debug later finds, and a failure is one line in the report.
4. **Report** per `conventions/report.md`, with `task`, `title` and `url` rows, or `f10 task report --children <parent>` for a split.
   In the same call, run `f10-state.sh set capture done` and `f10-state.sh task <id> <url>`, the parent's for a split.

Keep PII out of anything logged: ids, not names or emails.

**On failure:** `f10 task create` fails with anything but exit 3, or the create adapter errors or is missing (`conventions/failure.md`).
