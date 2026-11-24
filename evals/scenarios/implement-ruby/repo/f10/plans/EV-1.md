Roles: senior Ruby engineer
Size: small - one method on Notes, one file and its test

# EV-1: Search notes by text

Add `Notes#search(term)` to `lib/notes.rb`, which today only saves and lists.

## Stage 1 - search

- `lib/notes.rb`: `search(term)` returns `[]` for a nil or blank term. Otherwise it walks `names`, reads each note's file through the private `path`, and keeps the names whose text contains the term, compared case-insensitively (`downcase` on both sides). `names` is already sorted, so the result is too.
- `test/notes_test.rb`: one test per case, each in its own `Dir.mktmpdir`: a match across case, a miss, and a blank term.

## History

No history for these files.

## Consequences - none

## Gaps - none
