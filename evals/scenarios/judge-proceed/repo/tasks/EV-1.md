# EV-1: Refuse to start without the notes directory

OPEN · created 2026-11-24

The server starts even when its `-dir` does not exist, and then answers every note request with a 404 or a 500, so a typo in the flag looks like an empty or broken store rather than a bad start. `main` checks the directory with `os.Stat` before it listens, and exits non-zero with an error naming the path when it is missing or is not a directory.

## Done when

- `notes -dir /no/such/dir` exits non-zero with a message naming `/no/such/dir`.
- A test covers a missing path and a path that is a file.
