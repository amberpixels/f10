Roles: senior JavaScript engineer
Size: small - one method on Notes, one file and its test

# EV-1: Search notes by text

Add `search(term)` to the `Notes` class in `notes.js`, which today only saves and lists.

## Stage 1 - search

- `notes.js`: `search(term)` returns `[]` for an undefined or blank term. Otherwise it walks `names()`, reads each note's file through `path`, and keeps the names whose text contains the term, compared case-insensitively (`toLowerCase` on both sides). `names()` is already sorted, so the result is too.
- `notes.test.js`: one test per case, each in its own temp dir: a match across case, a miss, and a blank term.

## History

No history for these files.

## Consequences - none

## Gaps - none
