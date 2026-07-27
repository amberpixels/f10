# Step · capture - description → task

Role: a **senior engineer** capturing a well-scoped task.
Input: a free-text feature/bug description (may be terse).
Context: load per `conventions/context.md` - the tracker's **create adapter** and **id format** come from
`.f10/instructions/project.md` (+ `.f10/instructions/capture.md` overlay if present).

1. **Scope check.** Clarify with the user only if the description is too thin to write
   acceptance criteria; otherwise infer the scope yourself as a senior engineer.
2. **Draft the task.**
   - Title: concise, imperative.
   - Body (markdown): _Context / Problem_, _Proposed scope_, _Acceptance criteria_,
     _Out of scope_, and an _Areas_ note naming the parts of the codebase likely affected
     (user-facing UI, schema, auth, …). Later steps key conditional roles off this note, so
     name the areas plainly rather than describing them.
3. **Create it** via the project's create adapter - a skill to invoke, or a CLI command such
   as `gh issue create` / `glab issue create`. Apply the project's default status/labels if it
   declares any.
4. **Report** per `conventions/report.md` - a `task` row (the id in the project's format) and a
   `url` row, so the fetch step can pick the id up from here.

Keep PII out of anything logged - use ids, not names/emails.

**On failure:** the create adapter errors or is missing - report the drafted title and body
verbatim so the wording survives, and create the task by no other route. See
`conventions/failure.md`.
