---
visibility: stealth
storage: in-repo
tracker:
  kind: jira
  id: EV-###
flows:
  default: plan implement commit
---
# notes · eval fixture

## Project

Go. A small HTTP server for text notes.

## Tracker

The project's driver (`.f10/driver`) serves and creates tasks in `tasks/`.

## Verify

`just lint` / `just test`
