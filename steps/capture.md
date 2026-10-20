# Step · capture - from a description to a task

Role: a **senior engineer** capturing a well-scoped task.
Input: a free-text feature/bug description (may be terse).
Context: `project.md → Tracker` (the **create adapter** and **id format**).

1. **Scope check.**
   Ask the user only if the description is too thin to scope; otherwise infer the scope yourself.
   A description whose pieces each ship on their own becomes a **parent** and one **subtask** per piece; a user who asks for one task gets one.
2. **Draft the task**, or each task of a split.
   A parent holds the problem, the settled shape and what is out of scope; a subtask holds its own scope, sized by the rules below.
   - **Title:** concise, imperative, at every size.
   - **Body: size it by what a reader needs to act**, not by how big the change is.
     A heading appears only over something the reader cannot infer from the sentences above, never over a restatement or an "n/a"; a task whose prose makes the scope obvious has none.
     - **small** - one place, nothing to decide: a couple of paragraphs, no headings.
     - **normal** - several places, or one real decision: the problem, the scope, an _Areas_ note.
       Acceptance criteria only where the scope does not already imply them.
     - **large** - cross-cutting, or with a boundary worth fencing: add _Acceptance criteria_ and _Out of scope_.

     The _Areas_ note names the parts of the codebase likely affected (user-facing UI, schema, auth, …) plainly, since later steps key conditional roles off it.
     A small body carries no note; `fetch` derives the areas from the code.
   - **Altitude: write for the engineer who will pick this up, not a stranger to the codebase.**
     - **A snippet only when the shape *is* the ask** (a required output format, a wire payload); otherwise name the file and say what is wrong with it.
     - **Repro detail stops at what reproduces it**, not the session that found it.
     - **No effort estimates.**
   - **A dependency is a fixed last line.**
     When the order is known (the user names a task this one builds on, or capture creates tasks meant to run in sequence), the body ends with `After: <id>`, one id in the project's format, alone on its line; the binary parses it (`f10 start`, `f10 drive`, `f10 task children`) and ignores a line with anything after the id, so the word and the shape are fixed.
     No known order, no line.
     Tasks meant to run in sequence are created in that order, so each `After:` names an id that already exists.
   - **Other tasks named in the body** (a base task in prose, a parent, a subtask) carry their title, in the renderer's form (`conventions/context.md` → Task references); the `After:` line stays bare.
   - **Markup follows the tracker's renderer; markdown is not a safe default.**
     The create adapter says which renderer this is:
     - **GitHub / GitLab** - GFM, but **one line per paragraph, never hard-wrapped**: a single newline renders as `<br>`.
     - **Notion** - blocks, not a markdown string; the adapter takes structured content.
     - **Jira Cloud** - not markdown at all.
     - **Unknown** - plain paragraphs, and no markup that reads as noise unrendered.
3. **Create it** via the project's create adapter: a skill to invoke, or a CLI command such as `gh issue create` / `glab issue create`.
   Apply the project's declared default status/labels.
   A description that reads as a bug or a refactor also gets the tracker's label for it where one exists (`gh issue create --label bug`), so fetch settles the same kind later.
   A spike gets its label only when the user calls the task one, since the label alone makes ship skip the PR.
   A split creates the parent first, then links each subtask to it with the tracker's native parent link: the declared **parent** adapter, else the host's (GitHub sub-issues: `gh api repos/<owner>/<repo>/issues/<parent>/sub_issues -F sub_issue_id=<the child's numeric id>`; GitLab: `glab api` on the parent's child links).
   A tracker with no parent link gets a checklist of the subtasks in the parent's body, each with its title, edited in once they exist.
4. **Report** per `conventions/report.md`: a `task` row (the id in the project's format), a `title` row and a `url` row, for the fetch step; several tasks report as the convention's tree.
   Badge, in the same call: `f10-state.sh set capture done` and `f10-state.sh task <id> <url>`, the parent's id for a split.

Keep PII out of anything logged: ids, not names or emails.

**On failure:** the create adapter errors or is missing (`conventions/failure.md`).
