# The f10 driver contract

`f10 task` and `f10 start` need to reach your tracker, and the capture step needs to create tasks in it.
Where that tracker is the host's own issues, it already can: the git remote says GitHub or GitLab and `gh` / `glab` do the rest, with nothing to declare.
Where it is Notion, Jira, a wiki or a spreadsheet, there is no CLI to route to, and that is what a driver is for.

**f10 specifies this contract and implements none of it.**
What a driver talks to is your project's business, and so is the language it is written in - a shell script dispatching to what you already have is the usual shape. f10 fixes only the shape of the conversation.

[f10-drivers](https://github.com/amberpixels/f10-drivers) implements it for Notion, configured per project by a `driver.toml`, with a conformance suite any driver can run.

## Where it lives

An executable at `<storage root>/driver` - normally `.f10/driver`, or `~/.f10/<project>/driver` under out-of-tree storage. f10 runs it with the checkout root as the working directory.
A file that is missing, is a directory, or has no execute bit is simply "no driver", and `f10 task` falls back to the host's issues.

## Calling convention

```
<driver> <verb> [argument]
```

Eight verbs, each writing its answer to stdout.
Seven are **read verbs**:

| verb | argument | stdout |
|---|---|---|
| `read` | task number | the task as markdown, comments included |
| `url` | task number | one url, alone on a line |
| `search` | a query string | a JSON array of task rows |
| `branch` | task number | one branch name, alone on a line |
| `children` | task number | a JSON array of the task's subtasks |
| `index` | none | one url, the task list, alone on a line |
| `list` | none | a JSON array of task rows, the first page of open tasks |

One is a **write verb**:

| verb | input | stdout |
|---|---|---|
| `create` | the task as markdown on stdin, its properties as flags | the new task's id, alone on a line |

The argument for `read`, `url`, `branch` and `children` is the **number**, not the prefixed id: f10 has already parsed `ABC-1042` down to `1042`, so a driver never has to know the id format.
`index` and `list` take no argument: `index` is the page `f10 task open` with no id opens, `list` the rows `f10 task open --print` writes, in `search`'s row shape.
f10 ranks them by `priority` itself, so a driver returns them in its own order.
A driver without them gets the host's open issues.

`branch` is where a project encodes its branch shape - `feat/1042-short-slug`, or whatever `project.md` declares under Hosting & PR.
`f10 start` creates that branch; a driver without the verb gets the default, `<ID>/<slug>`, the slug cut from the title the host CLI returns.

A `search` row carries at least these four fields.
Extra fields are allowed and ignored:

```json
[{ "id": "ABC-1042", "title": "Fix order search timeout", "status": "In Progress", "url": "https://…" }]
```

A fifth, `"priority": "high"`, is optional: one of `urgent`, `high`, `medium`, `low`, in that order.
A driver maps its tracker's own values onto these (Jira's `Highest` becomes `urgent`); a row without the field has no priority and ranks after every row with one.
A value outside the vocabulary is dropped with a warning on stderr, never an error, so a driver that gets one word wrong still lists its tasks.
A `list` row may also carry `after` (the id of the task it waits for) or a `body` with an `After:` line, as a `children` row does: `f10 board` reads the dependency from it, and a row with neither has no base.
It may carry `assignee`, the tracker's name for who holds the task, which `f10 board` groups by.
It may carry `created`, when the tracker created the task as an RFC 3339 time, which puts the task under `new tasks` in `f10 board`'s feed for the window it falls in; a row without it never shows there.

A `children` row is one subtask linked to the task as its parent.
f10 drops rows whose `state` is `closed`, resolves `id` and `after` to the project's id format, and decides `finished` itself, so a driver returns every subtask it knows:

```json
[{ "id": "ABC-1043", "title": "Add the index", "url": "https://…", "state": "open", "labels": ["draft"], "after": "ABC-1042" }]
```

`after` is optional: a row with a `body` instead has its `After:` line read from it.
`[]` means the task has no subtasks.
Exit `3` declines the verb as for any other: f10 falls back to the host's issues, and where the tracker is not the host's, reads it as no parent link, so `/f10:drive` drives the id as given.

## Creating a task

`f10 task create` runs `create` with the task on stdin: `# <title>` on the first line, the body after it, in markdown.
A tracker whose API takes a markdown string (Notion's `POST /v1/pages` with `markdown`) gets it whole.
The task's properties arrive as flags, and only the ones the caller set:

```
<driver> create [--status <s>] [--priority <p>] [--label <l>]... [--parent <n>] [--after <n>]
```

- `--status` is the tracker's own word for a status, passed as given.
- `--priority` is one of the contract's `urgent`, `high`, `medium`, `low`; f10 has already rejected any other value, so a driver maps it onto its tracker's own.
- `--label` repeats, once per label.
- `--parent` is the task to link the new one under as a subtask, and `--after` the task it waits for, read from the body's `After:` line, which stays in the body.
  Both are numbers, as every other verb's argument is.

A property the tracker has no field for is dropped with a warning on stderr; f10 shows a successful `create`'s stderr to the user.
Print the new task's id alone on stdout, the number or the prefixed id: f10 prints it back in the project's id format.

## Exit codes

| code | meaning |
|---|---|
| `0` | it worked; stdout is the answer |
| `3` | this driver does not implement that verb |
| anything else | it failed; stderr is shown to the user verbatim |

Code `3` is a normal answer, not an error.
A driver may implement `read` without `search`, and f10 falls back to the host's issues rather than failing; a driver without `branch` gets the default `<ID>/<slug>` branch name.
On a tracker that is not the host's issues (Notion, Jira), a declined `read`, `url`, `index`, `list` or `search` has no fallback: f10 fails, naming your driver, the verb and the command that needs it.
A declined `branch` there names the branch by the bare id, and a declined `children` reads as no parent link.
A declined `create`, on any tracker, makes `f10 task create` exit 3 too, and the capture step creates the task through the Create adapter in `project.md`, as it does for a project with no driver.
Every other non-zero exit is a real failure, and f10 surfaces your stderr rather than paraphrasing a tracker error it cannot interpret.

## Rules worth knowing

**Read verbs change nothing; write verbs change only the tracker.**
Neither kind acts on the local machine.
A driver prints a url; f10 opens it.
A driver prints a branch name; f10 creates the branch.
That separation is what lets `open` mean the same thing for a task as it does for a pull request, where no driver is involved at all - and it keeps `f10 task open --link` pipeable.
Creating a task is a side effect only the tracker can perform, so it belongs to the driver; opening and branching are the machine's, so they stay with f10.

**Write verbs are not safe to retry blindly.**
A read can be retried after a 5xx; a `create` retried after a 5xx or a timeout can make the task twice.
Retry only a failure the tracker reports as rejected before acting, such as a 429.
Treat any other failure as an unknown outcome and say so on stderr, so the user checks the tracker before running it again.
f10 never retries a write verb, and its own error after a failed `create` says the task may exist.

**Print the whole task.**
`read` includes the task's comments after its body, as the host path does: an agent reads the task through this verb alone, and a decision made in a comment is part of what it asks.
Put the task's labels or type (a Jira issue type, a Notion select) near the top, as the host path puts them beside the state: fetch reads a bug, a refactor or a spike from them.

**Print markdown, not a rendering.**
`read` writes plain markdown and stops. f10 decides presentation from whether it is writing to a terminal, so the same command serves a person at a prompt and an agent capturing stdout.
A driver that pipes through `glow` or a pager itself breaks both.

**Link images, never inline them.**
Print each image as a link (markdown image syntax, an HTML `<img>`, or the bare url), signed urls included.
f10 downloads every linked image itself and names the local paths after the document, so a driver that downloads or inlines image data duplicates that work and hides the images from it.

## A driver in full

```sh
#!/usr/bin/env bash
set -euo pipefail
here="$(cd "$(dirname "$0")/.." && pwd)"

case "${1:-}" in
  read)   exec bash "$here/scripts/notion-read.sh" "${2:-}" ;;
  url)    exec bash "$here/scripts/notion-url.sh" "${2:-}" ;;
  search) exec bash "$here/scripts/notion-search.sh" "${2:-}" ;;
  branch) echo "ABC-${2:-}/$(bash "$here/scripts/notion-slug.sh" "${2:-}")" ;;
  create) shift; exec bash "$here/scripts/notion-create.sh" "$@" ;;  # stdin passes through
  *)      exit 3 ;;
esac
```

The `*)` arm is the contract's whole growth story: every verb you have not written yet, read or write, declines correctly, so a driver is useful from its first line.
