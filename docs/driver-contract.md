# The f10 driver contract

`f10 task` needs to reach your tracker. Where that tracker is the host's own issues, it already
can: the git remote says GitHub or GitLab and `gh` / `glab` do the rest, with nothing to declare.
Where it is Notion, Jira, a wiki or a spreadsheet, there is no CLI to route to, and that is what a
driver is for.

**f10 specifies this contract and implements none of it.** What a driver talks to is your
project's business, and so is the language it is written in - a shell script dispatching to what
you already have is the usual shape. f10 fixes only the shape of the conversation.

## Where it lives

An executable at `<storage root>/driver` - normally `.f10/driver`, or `~/.f10/<project>/driver`
under out-of-tree storage. f10 runs it with the checkout root as the working directory. A file
that is missing, is a directory, or has no execute bit is simply "no driver", and `f10 task`
falls back to the host's issues.

## Calling convention

```
<driver> <verb> [argument]
```

Three verbs, each writing its answer to stdout:

| verb | argument | stdout |
|---|---|---|
| `read` | task number | the task as markdown |
| `url` | task number | one url, alone on a line |
| `search` | a query string | a JSON array of task rows |

The argument for `read` and `url` is the **number**, not the prefixed id: f10 has already parsed
`WS-2703` down to `2703`, so a driver never has to know the id format.

A `search` row carries at least these four fields. Extra fields are allowed and ignored:

```json
[{ "id": "WS-2703", "title": "Fix patient search timeout", "status": "In Progress", "url": "https://…" }]
```

## Exit codes

| code | meaning |
|---|---|
| `0` | it worked; stdout is the answer |
| `3` | this driver does not implement that verb |
| anything else | it failed; stderr is shown to the user verbatim |

Code `3` is a normal answer, not an error. A driver may implement `read` without `search`, and
f10 falls back to the host's issues rather than failing. Every other non-zero exit is a real
failure, and f10 surfaces your stderr rather than paraphrasing a tracker error it cannot
interpret.

## Two rules worth knowing

**Return data, never act.** A driver prints a url; f10 opens it. That separation is what lets
`open` mean the same thing for a task as it does for a pull request, where no driver is involved
at all - and it keeps `f10 task open --print` pipeable.

**Print markdown, not a rendering.** `read` writes plain markdown and stops. f10 decides
presentation from whether it is writing to a terminal, so the same command serves a person at a
prompt and an agent capturing stdout. A driver that pipes through `glow` or a pager itself breaks
both.

## A driver in full

```sh
#!/usr/bin/env bash
set -euo pipefail
here="$(cd "$(dirname "$0")/.." && pwd)"

case "${1:-}" in
  read)   exec bash "$here/scripts/notion-read.sh" "${2:-}" ;;
  url)    exec bash "$here/scripts/notion-url.sh" "${2:-}" ;;
  search) exec bash "$here/scripts/notion-search.sh" "${2:-}" ;;
  *)      exit 3 ;;
esac
```

The `*)` arm is the contract's whole growth story: every verb you have not written yet declines
correctly, so a driver is useful from its first line.
