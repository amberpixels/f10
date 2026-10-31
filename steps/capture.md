# Step · capture - from a description to a task

Role: a **senior engineer** capturing a well-scoped task.
Input: a free-text description of a feature or a bug, possibly terse, or a debug brief.
Context: `project.md → Tracker`, for the task **id format**, the **create adapter**, which creates the task when `f10 task create` exits 3, and the **Properties** a new task carries.

1. **Check the scope.**
   Infer the scope yourself.
   Ask the user only when the description is too thin to scope.
   A description can hold several pieces that each ship on their own.
   Such a description becomes a **parent** task, plus one **subtask** per piece.
   However, a user who asks for one task gets one task.
2. **Draft the task**, or each task of a split.
   A parent holds the problem, the shape that is settled, and what is out of scope.
   A subtask holds its own scope, and its body is sized by the rules below, like any task.
   - **Title:** concise and imperative, at every size.
   - **Body:** size it by what a reader needs in order to act, not by how big the change is.
     Use one of three sizes:
     - **small** - one place to change and nothing to decide: a couple of paragraphs and no headings.
     - **normal** - several places, or one real decision: the problem, the scope, and an _Areas_ note.
       Add acceptance criteria only where the scope does not already imply them.
     - **large** - a cross-cutting change, or one with a boundary worth fencing off: the normal body, plus _Acceptance criteria_ and _Out of scope_ sections.

     A heading goes only over content the reader cannot infer from the sentences above it.
     Never put a heading over a restatement or over an "n/a".
   - **The _Areas_ note** names the parts of the codebase the task likely affects, such as user-facing UI, schema or auth.
     Name them plainly, because later steps choose conditional roles from this note.
     A small body carries no _Areas_ note, because the fetch step derives the areas from the code.
   - **A task drafted from a debug brief** is at least **normal**.
     It carries a _Reproduction_ section (the steps or input, the expected result and the actual one) and a _Root cause_ section (the `file:line` and its evidence), both taken from the brief.
     Its title names the defect, not the fix.
   - **Altitude:** write for the engineer who will pick the task up, not for a stranger to the codebase.
     - Include a snippet only when its shape *is* the ask, such as a required output format or a wire payload.
       Otherwise, name the file and say what is wrong with it.
     - Reproduction detail stops at what reproduces the problem.
       Leave out the session that found it.
     - Give no effort estimates.
   - **A dependency on another task is the body's last line.**
     Write it only when the order is known: the user names a task this one builds on, or capture creates tasks meant to run in sequence.
     With no known order, there is no line.
     The line is `After: <id>`: one id in the project's format, alone on its line, in the shape `conventions/context.md` → Task references fixes.
     `f10 task create` rewrites the id as a link itself.
     A task that the create adapter creates instead depends on the tracker's renderer.
     On a GFM renderer (GitHub, GitLab), the line is `After: ` followed by what `f10 task ref <id> --md` prints.
     When ref prints the id bare, the line keeps it bare.
     Any other renderer keeps the bare id.
     Create tasks meant to run in sequence in that order, so that each `After:` line names an id that already exists.
   - **Other tasks named in the body**, such as a base task mentioned in prose, a parent or a subtask, carry their titles.
     Get every title from one `f10 task ref` call, and write each in the renderer's form (`conventions/context.md` → Task references).
   - **Markup follows the tracker's renderer**, because markdown is not a safe default.
     `f10 task create` takes markdown (point 3).
     On GitHub and GitLab issues, its draft follows the **GitHub / GitLab** rule below.
     A task that the create adapter creates after an exit 3 is re-rendered for that adapter's renderer:
     - **GitHub / GitLab** - GFM, with **each paragraph on one line, never hard-wrapped**, because a single newline renders as `<br>`.
     - **Notion**, through an adapter that takes structured content - blocks, not a markdown string.
     - **Jira Cloud** - not markdown at all.
     - **Unknown** - plain paragraphs, with no markup that reads as noise when it is not rendered.
3. **Create the task**, through `f10 task create` first.
   Pass the draft on stdin, `# <title>` on the first line and the body after it.
   Pass its properties as flags: `--status`, `--priority`, `--label` once per label, `--parent <id>` for a subtask, and `--set <key>=<value>` once per declared property (below).
   Apply the project's declared default status and labels.
   `f10 task create` creates the task through the project's driver, else as an issue on GitHub or GitLab, and prints the new id.
   `f10 task open <id> --link` then prints the url for the report.
   - **Exit 3** means neither the driver nor the host creates tasks here.
     Create the task through the project's create adapter instead: a skill to invoke, or the command `project.md` declares.
     When the frontmatter declares `tracker.title`, run `f10 task retitle <id>` right after the adapter creates the task, which puts the id on its title.
     `f10 task create` retitles by itself, so it needs no such call.
     If the retitle fails, report it, but never create the task again, because it already exists.
   - **A failure whose error says the task may exist** is never re-run, whatever `conventions/failure.md` rule 3 allows.
     Report the failure, and let the user check the tracker.
   - **Any other failure**, such as a missing title or an unknown priority or id, happened before the driver ran.
     Fix the input and run the command again.
   - **Kind labels.**
     A description that reads as a bug or a refactor gets the tracker's label for that kind, where one exists (`f10 task create --label bug`), so that the fetch step settles the same kind later.
     A task from a debug brief always gets the label, because the brief has confirmed its kind.
     A spike gets its label only when the user calls the task a spike, because that label alone makes the ship skill skip the PR.
   - **Declared properties.**
     A tracker that classifies tasks with named properties declares them under `project.md → Tracker` as **Properties**, and a capture overlay may add or override entries.
     Each entry names a key, its allowed values and how to choose one.
     Choose each value from the draft by that rule, and pass it on create, so the task is never created half-classified and needs no `f10 task update` after it.
     Leave a property unset when the draft does not settle its value, because a wrong value misfiles the task.
     On an exit 3, the create adapter sets the same properties by its own means.
     A split sets them on the parent and on every subtask.
   - **A split** creates the parent first, then links each subtask to it.
     Through `f10 task create`, `--parent` makes the link: a GitHub sub-issue, or the driver's own link.
     Through the create adapter, use the tracker's native parent link, or the **parent** adapter the project declares.
     A tracker with no parent link gets a checklist in the parent's body instead: one item per subtask, with its title, edited in once the subtasks exist.
     GitLab cannot nest an issue under an issue, so on GitLab `f10 task create` warns, and the checklist goes in.
     After creating through `f10 task create`, confirm the links with `f10 task children <parent>`.
     Its exit 3 means the tracker has no parent link, so the checklist goes in.
     A subtask missing from its rows means the link was dropped, by a driver that ignores `--parent` or by a host link that create warned about.
     The report names that subtask, for the user to link by hand.
4. **Report** per `conventions/report.md`, with a `task` row (the id in the project's format), a `title` row and a `url` row, which the fetch step reads next.
   A split reports through `f10 task report <parent> <subtask ids>` and `f10 status <parent> --plain`, each printed verbatim (`conventions/report.md` → Several tasks).
   In the same call, set the badge: `f10-state.sh set capture done` and `f10-state.sh task <id> <url>`, with the parent's id for a split.

Keep PII out of anything logged: write ids, not names or emails.

**On failure:** `f10 task create` fails with anything but exit 3, or the create adapter errors or is missing (`conventions/failure.md`).
A failed `f10 task create` is reported, and never re-run when the task may exist.
