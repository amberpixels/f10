# Step · capture - from a description to a task

Role: a **senior engineer** capturing a well-scoped task.
Input: a free-text feature/bug description (may be terse), or a debug brief.
Context: `project.md → Tracker` (the **create adapter**, used where `f10 task create` exits 3, and **id format**).

1. **Scope check.**
   Ask the user only if the description is too thin to scope; otherwise infer the scope yourself.
   A description whose pieces each ship on their own becomes a **parent** and one **subtask** per piece; a user who asks for one task gets one.
2. **Draft the task**, or each task of a split.
   A parent holds the problem, the settled shape and what is out of scope; a subtask holds its own scope, sized by the rules below.
   - **Title:** concise, imperative, at every size.
   - **Body: size it by what a reader needs to act**, not by how big the change is.
     A heading goes only over what the reader cannot infer from the sentences above, never over a restatement or an "n/a".
     - **small** - one place, nothing to decide: a couple of paragraphs, no headings.
     - **normal** - several places, or one real decision: the problem, the scope, an _Areas_ note.
       Acceptance criteria only where the scope does not already imply them.
     - **large** - cross-cutting, or with a boundary worth fencing: add _Acceptance criteria_ and _Out of scope_.

     The _Areas_ note names the parts of the codebase likely affected (user-facing UI, schema, auth, …) plainly, since later steps key conditional roles off it.
     A small body carries no note; `fetch` derives the areas from the code.
   - **From a debug brief**: at least **normal**, with _Reproduction_ (steps or input, expected, actual) and _Root cause_ (the `file:line` and its evidence) sections from the brief, and a title naming the defect, not the fix.
   - **Altitude: write for the engineer who will pick this up, not a stranger to the codebase.**
     - A snippet only when the shape *is* the ask (a required output format, a wire payload); otherwise name the file and say what is wrong with it.
     - Repro detail stops at what reproduces it, not the session that found it.
     - No effort estimates.
   - **A dependency is a fixed last line.**
     When the order is known (the user names a task this one builds on, or capture creates tasks meant to run in sequence), the body ends with `After: <id>`, one id in the project's format, alone on its line, in the shape `conventions/context.md` → Task references fixes.
     `f10 task create` rewrites the id as a link itself; a task the create adapter takes on a GFM renderer (GitHub, GitLab) gets `After: <the line f10 task ref <id> --md prints>`, bare when ref prints it bare; any other renderer keeps the bare id.
     No known order, no line.
     Tasks meant to run in sequence are created in that order, so each `After:` names an id that already exists.
   - **Other tasks named in the body** (a base task in prose, a parent, a subtask) carry their title, in the renderer's form, from one `f10 task ref` call (`conventions/context.md` → Task references).
   - **Markup follows the tracker's renderer; markdown is not a safe default.**
     `f10 task create` takes markdown (step 3); on GitHub and GitLab issues the **GitHub / GitLab** rule below binds its draft.
     A task the create adapter takes after an exit 3 is re-rendered for the adapter's renderer:
     - **GitHub / GitLab** - GFM, but **one line per paragraph, never hard-wrapped**: a single newline renders as `<br>`.
     - **Notion** through an adapter that takes structured content - blocks, not a markdown string.
     - **Jira Cloud** - not markdown at all.
     - **Unknown** - plain paragraphs, and no markup that reads as noise unrendered.
3. **Create it** through `f10 task create` first: the drafted task on stdin, `# <title>` on the first line and the body after it, and its properties as flags (`--status`, `--priority`, `--label` once per label, `--parent <id>` for a subtask).
   It prints the new id; `f10 task open <id> --link` prints the url for the report.
   It creates through the project's driver, else as an issue on GitHub or GitLab.
   **Exit 3** means neither creates tasks here: create it via the project's create adapter instead, a skill to invoke or the command `project.md` declares.
   With the frontmatter's `tracker.title` declared, run `f10 task retitle <id>` right after the adapter creates it, which puts the id on its title (`f10 task create` does this itself); the task exists, so a failure is reported, never a reason to create it again.
   **Never re-run a failed `f10 task create` whose error says the task may exist**, whatever `conventions/failure.md` rule 3 allows: report the failure and let the user check the tracker.
   Any other failure (a missing title, an unknown priority or id) ran before the driver did: fix the input and run it again.
   Apply the project's declared default status/labels.
   A description that reads as a bug or a refactor also gets the tracker's label for it where one exists (`f10 task create --label bug`), so fetch settles the same kind later; a debug brief's kind is confirmed, so it always gets one.
   A spike gets its label only when the user calls the task one, since the label alone makes ship skip the PR.
   A split creates the parent first, then links each subtask to it: through `f10 task create`, `--parent` is the link (a GitHub sub-issue, or the driver's); through the adapter, the tracker's native parent link or the declared **parent** adapter.
   GitLab nests no issue under an issue: there create warns, and the checklist below goes in.
   A tracker with no parent link gets a checklist of the subtasks in the parent's body, each with its title, edited in once they exist.
   Through `f10 task create`, `f10 task children <parent>` confirms the link: an exit 3 is a tracker with no parent link, so the checklist goes in; a subtask missing from its rows means the link was dropped (a driver ignoring `--parent`, or a host link create warned about), and the report names it for the user to link by hand.
4. **Report** per `conventions/report.md`: a `task` row (the id in the project's format), a `title` row and a `url` row, for the fetch step; a split reports through `f10 task report <parent> <subtask ids>` and `f10 status <parent> --plain`, each printed verbatim (`conventions/report.md` → Several tasks).
   Badge, in the same call: `f10-state.sh set capture done` and `f10-state.sh task <id> <url>`, the parent's id for a split.

Keep PII out of anything logged: ids, not names or emails.

**On failure:** `f10 task create` fails with anything but exit 3 (reported, never re-run when the task may exist), or the create adapter errors or is missing (`conventions/failure.md`).
