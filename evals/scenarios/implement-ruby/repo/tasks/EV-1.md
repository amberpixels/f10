# EV-1: Search notes by text

OPEN · created 2026-11-24

`Notes#search(term)` returns the names of the notes whose text contains `term`, ignoring case, sorted by name. A blank term returns no names.

## Done when

- `search('HELLO')` finds a note holding `hello there`.
- A test covers a match, a miss and a blank term.
