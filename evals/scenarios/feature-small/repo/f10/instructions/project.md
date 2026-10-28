---
visibility: stealth
storage: in-repo
tracker:
  kind: jira
  id: T-###
flows:
  default: plan implement commit
---
# greet · eval fixture

## Project

Go. A one-file CLI.

## Tracker

The project's driver (`.f10/driver`) serves tasks from `tasks/`.

## Verify

`just lint` / `just test`
