# EV-2: Page, read and import notes

OPEN · created 2026-11-24

- `Notes#page(n)` lists the notes sorted by name, 10 per page, each as `"<name>: <first line>"`. Pages start at 1, and a page past the end is empty.
- `Notes#read(name)` returns one note's text, and raises `Notes::NotFound` for a note that does not exist. Only notes in the notes directory are readable.
- `Notes#import(name, io)` stores what `io` yields as the note. A note over 1 MiB is refused with `Notes::TooLarge`, and nothing is written.

## Done when

- The three methods work as described.
- A test imports a note and reads it back.
