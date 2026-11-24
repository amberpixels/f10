# EV-2: Delete a note

OPEN · created 2026-11-24

`DELETE /note?name=<name>` removes the note's file and answers 204, or 404 when the note does not exist. It joins the name onto the notes directory the way `read` does.

## Done when

- A test saves a note, deletes it and reads it back as 404.
