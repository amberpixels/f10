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

JavaScript (Node, ES modules). A small library keeping text notes in a directory. No npm dependencies: `node:test` for tests.

## Tracker

The project's driver (`.f10/driver`) serves tasks from `tasks/`.

## Verify

`just lint` / `just test`
