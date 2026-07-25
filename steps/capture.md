# Step · capture - description → task

Role: a **senior engineer** capturing a well-scoped ticket.
Input: a free-text feature/bug description (may be terse).
Context: load per `conventions/context.md` - the tracker's **create adapter** and **id format** come from
`.f10/instructions/project.md` (+ `.f10/instructions/capture.md` overlay if present).

1. **Scope check.** Clarify with the user only if the description is too thin to write
   acceptance criteria; otherwise infer the scope yourself as a senior engineer.
2. **Draft the task.**
   - Title: concise, imperative.
   - Body (markdown): _Context / Problem_, _Proposed scope_, _Acceptance criteria_,
     _Out of scope_, and a note on the areas of the codebase likely affected.
3. **Create it** via the project's create adapter - a skill to invoke, or a CLI command such
   as `gh issue create` / `glab issue create`. Apply the project's default status/labels if it
   declares any.
4. **Output** the task id (in the project's id format) and url so the fetch step can pick it up.

Keep PII out of anything logged - use ids, not names/emails.

**On failure:** the create adapter errors or is missing - report the drafted title and body
verbatim so the wording survives, and create the task by no other route. See
`conventions/failure.md`.
