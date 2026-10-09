# Step · capture - from a description to a task

Role: a **senior engineer** capturing a well-scoped task.
Input: a free-text feature/bug description (may be terse).
Context: `project.md → Tracker` (the **create adapter** and **id format**).

1. **Scope check.** Ask the user only if the description is too thin to scope; otherwise infer
   the scope yourself.
2. **Draft the task.**
   - **Title:** concise, imperative, at every size.
   - **Body: size it by what a reader needs to act**, not by how big the change is. A heading
     appears only over something the reader cannot infer from the sentences above, never over a
     restatement or an "n/a"; a task whose prose makes the scope obvious has none.
     - **small** - one place, nothing to decide: a couple of paragraphs, no headings.
     - **normal** - several places, or one real decision: the problem, the scope, an _Areas_
       note. Acceptance criteria only where the scope does not already imply them.
     - **large** - cross-cutting, or with a boundary worth fencing: add _Acceptance criteria_
       and _Out of scope_.

     The _Areas_ note names the parts of the codebase likely affected (user-facing UI, schema,
     auth, …) plainly, since later steps key conditional roles off it. A small body carries no
     note; `fetch` derives the areas from the code.
   - **Altitude: write for the engineer who will pick this up, not a stranger to the codebase.**
     - **No code walkthroughs, no pasted diffs.** A snippet only when the shape *is* the ask (a
       required output format, a wire payload). Name the file and say what is wrong with it.
     - **Repro detail stops at what reproduces it**, not the session that found it.
     - **No effort or time estimates.** No hours, no story points, no sizing.
     - **No acceptance criteria that restate the scope** in the future tense; keep the scope.
   - **A dependency is a fixed last line.** When the order is known (the user names a task this
     one builds on, or capture creates tasks meant to run in sequence), the body ends with
     `After: <id>`, one id in the project's format, alone on its line. `f10 start` takes it as
     the default `--after` and `f10 drive` checks it against its list, so the word and the
     shape are fixed. No known order, no line.
   - **Markup follows the tracker's renderer; markdown is not a safe default.** The create
     adapter says which renderer this is:
     - **GitHub / GitLab** - GFM, but **one line per paragraph, never hard-wrapped**: a single
       newline renders as `<br>`.
     - **Notion** - blocks, not a markdown string; the adapter takes structured content.
     - **Jira Cloud** - not markdown at all.
     - **Unknown** - plain paragraphs, and no markup that reads as noise unrendered.
3. **Create it** via the project's create adapter: a skill to invoke, or a CLI command such as
   `gh issue create` / `glab issue create`. Apply the project's declared default status/labels.
4. **Report** per `conventions/report.md`: a `task` row (the id in the project's format) and a
   `url` row, for the fetch step. Badge, in the same call: `f10-state.sh set capture done` and
   `f10-state.sh task <id> <url>`.

Keep PII out of anything logged: ids, not names or emails.

**On failure:** the create adapter errors or is missing: report the drafted title and body
verbatim, and create the task by no other route (`conventions/failure.md`).
