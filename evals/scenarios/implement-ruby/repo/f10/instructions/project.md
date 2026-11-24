---
visibility: stealth
storage: in-repo
tracker:
  kind: jira
  id: EV-###
flows:
  default: implement commit
---
# notes · eval fixture

## Project

Ruby. A small library keeping text notes in a directory. No gems beyond the standard library.

## Tracker

The project's driver (`.f10/driver`) serves tasks from `tasks/`.

## Verify

`just lint` / `just test`
