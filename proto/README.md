# proto

The control-plane API: the contract the daemon serves over Connect and every client is built on, the `f10` CLI included.
The daemon serves it on its unix socket (`docs/cli.md`, **Daemon**).

## Packages

- **`agents.v1`** is the generic agent core: workspaces, agents, text sent to an agent, and the asks agents wait on.
  It knows nothing about tasks, so it can become a project of its own.
  An owner's context, such as the task f10 maps to an agent, goes in `Ask.labels`, never in a new field.
- **`f10.v1`** is the task flow: tasks, runs, start, drive, finish, the board, and the event stream, plus `DaemonService`, the daemon's own hello, status, stop and project watch.
  It imports `agents.v1`, and `agents.v1` never imports it.

`f10.v1` also carries `ArtifactService`, which reads a task's files from the storage root by kind and name, and `ProjectService`, which lists the projects the daemon knows.
The web client in `ts/web` is built on the TypeScript code; `ts/` is the TypeScript root, so `ts/gen` resolves its imports from `ts/node_modules`.

Each package lives in the directory its name gives (`agents/v1/`, `f10/v1/`), because buf's lint requires it.

## Generated code

Generated code is checked in, so a build needs no generator.

| output | where | import |
|---|---|---|
| Go messages | `cli/gen/<package path>/` | `github.com/amberpixels/f10/cli/gen/f10/v1` |
| Go Connect stubs | `cli/gen/<package path>/<pkg>connect/` | `github.com/amberpixels/f10/cli/gen/f10/v1/f10v1connect` |
| TypeScript (protobuf-es) | `ts/gen/<package path>/` | needs `@bufbuild/protobuf` at runtime, which `ts/package.json` pins |

The generators are pinned: buf and the Go plugins in `tools/go.mod`, `protoc-gen-es` in `package-lock.json`.
`just proto::tools` installs them into `.bin/` and `node_modules/`, so you need Go and Node, but no global buf.

## Recipes

- `just gen` (or `just proto::gen`) regenerates the Go and TypeScript code.
  Run it after any change here, and commit the result.
- `just proto::lint` runs `buf lint`, then generates into a temporary directory and fails when the checked-in code differs.
  `just lint` runs it.
- `just proto::breaking` fails on a breaking change against the default branch's `proto/` (`refs/remotes/origin/main`).
- `just proto::breaking-selftest` removes a field from a copy of this tree and fails unless `buf breaking` refuses it.

`just ci`, and so CI, runs all of them.

## Evolution rules

Clients built against `v1` keep working only if `v1` changes only by adding, so:

- **Within `v1`, a field is only added.** It is never renumbered, renamed or removed, and neither is a message, an enum value, a service or a method.
  `buf breaking` refuses each of these.
- **A field that must go is deprecated**, with `[deprecated = true]`, and stays.
  The `FILE` breaking rules refuse a deleted field even when its number and name are `reserved`, so removal waits for a new message or a `v2` package.
  There, `reserved` keeps a dropped field's number and name from being reused.
- **A new enum value is fine**, because clients must handle values they do not know.
  The zero value is always `*_UNSPECIFIED`.
- **A real break becomes a new message**, added beside the old one, or a `v2` package once enough breaks pile up.
  A `v2` is served beside `v1` until no client needs `v1`.

## Event payloads

`f10.v1.Event` is one ledger row, with a `oneof payload` case named after each ledger kind.
The nine `state_*` kinds share the `claim` case, so a client tells them apart by `Event.kind`, and applies them to the `Run` whose session `Event.session` names.
`WatchEvents` resumes from the `cursor` each response carries, never from `Event.id`, because rows land in the ledger out of id order: a spooled row is inserted after its id is minted, and other machines mint ids on their own clocks.
`ask_raised` and `ask_answered` are the asks inbox's, which records them in the ledger as asks are raised and answered.

The payload messages are the ledger's stored payloads: the ledger registers each kind against its message and stores it as protojson (`docs/ledger.md`).
So they follow the JSON the ledger stored before they existed, not API taste:

- **Field names are the stored keys** (`origin_task`, `cache_write`), and the ledger writes them with `UseProtoNames`.
- **Embedded structs are flattened**: `RunStarted` and `SessionStopped` carry the run context's fields directly, and `Usage` carries the four token counts itself.
- **Counts are `int32`**, because protojson writes `int64` as a JSON string, which `json_extract` would not sum.
- **Zero values are written**: the ledger stores with `EmitDefaultValues`, so `"input": 0` and `"capture": ""` stay in the row as they always were.

The evolution rules above therefore bind the ledger too: a payload field is only added, and a real break is a new message the ledger upcasts from the old one.
`TestEveryKindIsAnEventCase` in `cli/internal/ledger` fails when a ledger kind has no payload case, or when its stored message is not the case's message.
A new ledger kind therefore needs its message here in the same change.
