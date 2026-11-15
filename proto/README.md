# proto

The control-plane API: the contract the daemon serves over Connect and every client is built on, the `f10` CLI included.
Nothing serves it yet.

## Packages

- **`agents.v1`** is the generic agent core: workspaces, agents, text sent to an agent, and the asks agents wait on.
  It knows nothing about tasks, so it can become a project of its own.
  An owner's context, such as the task f10 maps to an agent, goes in `Ask.labels`, never in a new field.
- **`f10.v1`** is the task flow: tasks, runs, start, drive, finish, the board, and the event stream.
  It imports `agents.v1`, and `agents.v1` never imports it.

Each package lives in the directory its name gives (`agents/v1/`, `f10/v1/`), because buf's lint requires it.

## Generated code

Generated code is checked in, so a build needs no generator.

| output | where | import |
|---|---|---|
| Go messages | `cli/gen/<package path>/` | `github.com/amberpixels/f10/cli/gen/f10/v1` |
| Go Connect stubs | `cli/gen/<package path>/<pkg>connect/` | `github.com/amberpixels/f10/cli/gen/f10/v1/f10v1connect` |
| TypeScript (protobuf-es) | `ts/gen/<package path>/` | needs `@bufbuild/protobuf` at runtime |

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
  If it must be deleted, its number and name are `reserved`, so neither is reused.
- **A new enum value is fine**, because clients must handle values they do not know.
  The zero value is always `*_UNSPECIFIED`.
- **A real break becomes a new message**, added beside the old one, or a `v2` package once enough breaks pile up.
  A `v2` is served beside `v1` until no client needs `v1`.

## Event payloads

`f10.v1.Event` is one ledger row, with a `oneof payload` case named after each ledger kind.
The nine `state_*` kinds share the `claim` case, so a client tells them apart by `Event.kind`.
`ask_raised` and `ask_answered` belong to the asks inbox, which the ledger does not record yet.

The payload messages are meant to become the ledger's own stored payloads, so they follow the JSON the ledger already stores, not API taste:

- **Field names are the stored keys** (`origin_task`, `cache_write`), so protojson with `UseProtoNames` writes the same keys.
- **Embedded structs are flattened**: `RunStarted` and `SessionStopped` carry the run context's fields directly, and `Usage` carries the four token counts itself.
- **Counts are `int32`**, because protojson writes `int64` as a JSON string, which `json_extract` would not sum.
- **Zero values must be written**: protojson leaves them out by default, while the ledger stores keys like `"input": 0` and `"capture": ""`.
  Storing these messages takes `EmitDefaultValues` along with `UseProtoNames`.

`TestProtoPayloadParity` in `cli/internal/ledger` fails when a ledger kind has no payload case, or when a kind's current Go struct and its message differ in a key or a value's type.
A new ledger kind therefore needs its message here in the same change.
