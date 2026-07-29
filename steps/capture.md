# Step · capture - description → task

Role: a **senior engineer** capturing a well-scoped task.
Input: a free-text feature/bug description (may be terse).
Context: load per `conventions/context.md` - the tracker's **create adapter** and **id format** come from
`.f10/instructions/project.md` (+ `.f10/instructions/capture.md` overlay if present).
Badge (`conventions/report.md`): `f10-state.sh set capture running` on entry;
`f10-state.sh set capture done` and `f10-state.sh task <id> <url>` once the task exists.

1. **Scope check.** Clarify with the user only if the description is too thin to scope;
   otherwise infer the scope yourself as a senior engineer.
2. **Draft the task.**
   - **Title:** concise, imperative - the same at every size.
   - **Body: size it to the task.** A heading is **earned**: it appears only when what sits under
     it is something the reader cannot already infer from the sentences above. Never write one to
     hold a restatement or an "n/a". Judge the size by what a reader needs in order to act, not by
     how big the change will be.
     - **small** - one place, nothing to decide: a couple of paragraphs, no headings at all.
     - **normal** - several places, or one real decision: the problem, the scope, an _Areas_ note.
       Acceptance criteria only where the scope does not already imply them.
     - **large** - cross-cutting, or with a boundary worth fencing: add _Acceptance criteria_ and
       _Out of scope_.

     Those name what a body **carries**, not how many headings it wears: a normal-sized task whose
     prose already makes the scope obvious needs no heading sitting over it.

     The _Areas_ note names the parts of the codebase likely affected (user-facing UI, schema,
     auth, …) - later steps key conditional roles off it, so name them plainly rather than
     describing them. A small body carries no note and `fetch` derives the areas from the code.
   - **Altitude - write for the engineer who will pick this task up, not for a stranger to the
     codebase:**
     - **No code walkthroughs, no pasted diffs.** A snippet earns its place only when the shape
       *is* the ask - a required output format, a wire payload. Naming the file and saying what is
       wrong with it beats quoting it.
     - **Repro detail stops at what reproduces it** - not the session that found it.
     - **No effort or time estimates.** No hours, no story points, no sizing.
     - **No acceptance criteria that restate the scope** in the future tense. Criteria that only
       echo the bullets above them are noise: drop them and keep the scope.
     - **Never pad an unearned section.** Nothing to say under a heading means the heading goes,
       not that you find filler for it.
   - **Markup follows the tracker's renderer** - it belongs to the tracker, not to you, and
     **markdown is not a safe default**. The create adapter says which one this is:
     - **GitHub / GitLab** - GFM, but **one line per paragraph, never hard-wrapped**: a single
       newline renders as `<br>`.
     - **Notion** - blocks, not a markdown string; the adapter takes structured content.
     - **Jira Cloud** - not markdown at all.
     - **Unknown** - plain paragraphs, and no markup that reads as noise unrendered.
3. **Create it** via the project's create adapter - a skill to invoke, or a CLI command such
   as `gh issue create` / `glab issue create`. Apply the project's default status/labels if it
   declares any.
4. **Report** per `conventions/report.md` - a `task` row (the id in the project's format) and a
   `url` row, so the fetch step can pick the id up from here.

Keep PII out of anything logged - use ids, not names/emails.

**On failure:** the create adapter errors or is missing - report the drafted title and body
verbatim so the wording survives, and create the task by no other route. See
`conventions/failure.md`.
