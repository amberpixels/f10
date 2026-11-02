# EV-2: Note endpoints

OPEN · created 2026-10-30

The server stores notes as `<name>.txt` files in its notes directory.

- `GET /notes?page=N` lists the notes, sorted by name, 10 per page. Pages start at 1, and a page past the end is empty. Each line is the note's name and its first line.
- `GET /note?name=<name>` returns one note's text, and 404 for a note that does not exist. Only notes in the notes directory are readable.
- `POST /note?name=<name>` stores the request body as the note. A body over 1 MiB is refused with 413.

## Done when

- The three endpoints work as described.
- A test saves a note and reads it back.
