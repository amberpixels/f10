---
visibility: stealth
storage: in-repo
tracker:
  kind: jira
  id: EV-###
flows:
  default: plan implement review:local
---
# notes · eval fixture

## Project

Go. A small HTTP server for text notes.

## Tracker

The project's driver (`.f10/driver`) serves tasks from `tasks/`.

## Verify

`just lint` / `just test`
