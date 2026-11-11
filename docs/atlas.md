# The atlas, version 1

What each of your projects is and how they connect: one node per project, with what it is, whether it is active or parked, how long it has lived and how often it changes, and one edge per dependency or declared link between two of them.
`f10 atlas --json` prints the **Atlas JSON** this file specifies, `docs/atlas.schema.json` is its JSON Schema, and `~/.f10/atlas.md` is the **declared file** where you write what no checkout can say.
A renderer, such as a personal website, draws the document; f10 draws nothing.
Version 1 reads local checkouts only, with no host call, so a run takes about a second and a logged-out host CLI cannot fail it.

## Shape

```json
{
  "version": 1,
  "generated_at": "2026-11-11T12:00:00Z",
  "nodes": [
    {
      "id": "github.com/acme/gears",
      "kind": "public",
      "name": "gears",
      "role": "Go library",
      "status": "stable",
      "tagline": "Small reflection helpers for everyday Go.",
      "language": "go",
      "first_commit": "2026-08-03T09:14:00Z",
      "last_commit": "2026-10-28T17:40:00Z",
      "commits_per_month": [
        { "month": "2026-08", "commits": 31 },
        { "month": "2026-09", "commits": 12 },
        { "month": "2026-10", "commits": 4 },
        { "month": "2026-11", "commits": 0 }
      ],
      "latest_tag": { "name": "v0.3.0", "at": "2026-10-28T17:40:00Z" }
    },
    {
      "id": "github.com/acme/widgets",
      "kind": "public",
      "name": "widgets",
      "role": "CLI",
      "status": "active",
      "tagline": "Arranges widgets from the command line.",
      "language": "go",
      "first_commit": "2026-10-01T08:00:00Z",
      "last_commit": "2026-11-10T21:05:00Z",
      "commits_per_month": [
        { "month": "2026-10", "commits": 58 },
        { "month": "2026-11", "commits": 19 }
      ]
    },
    { "id": "anon/client-app", "kind": "anonymous", "alias": "client-app" }
  ],
  "edges": [
    { "from": "anon/client-app", "to": "github.com/acme/gears", "kind": "depends", "source": "manifest" },
    { "from": "github.com/acme/widgets", "to": "github.com/acme/gears", "kind": "depends", "source": "manifest", "manifest": "go.mod" },
    { "from": "github.com/acme/widgets", "to": "github.com/acme/gears", "kind": "link", "source": "declared", "relation": "showcase for" }
  ]
}
```

| field | meaning |
|---|---|
| `version` | `1`, this version of the spec |
| `generated_at` | when the atlas was derived |
| `nodes` | public nodes sorted by `name`, then anonymous nodes sorted by `alias` |
| `edges` | sorted by `from`, then `to`, then `kind`, then `manifest` or `relation`, so two runs over the same checkouts produce the same document |

**Identity.** A node's `id` is the key every edge uses to name it.
- A public node with an origin remote takes the normalised remote, as the board's `project.id` does (`github.com/org/repo`).
- A public node with no remote takes `local/<name>`, where `name` is the main checkout's basename.
- An anonymous node takes `anon/<alias>`, built from its alias alone, so its id carries nothing of the project's remote or path.

A linked worktree is never a node of its own: it is its main checkout.
Two checkouts in scope that give the same id, such as two clones of one remote or two remote-less checkouts with one basename, are an error naming both, because an edge could not tell them apart.

**A public node** is a project whose declared entry says `public: true`.
Each field comes from the declared file or from the checkout.
Only `id`, `kind` and `name` are always present; every other field is absent where its row says.

| field | from | meaning |
|---|---|---|
| `id` | checkout | the node's key, as **Identity** gives it |
| `kind` | - | `public` |
| `name` | checkout | the main checkout's basename |
| `role` | declared | what the project is, in a few words (`Go library`, `CLI`, `plugin`); absent when the entry gives none |
| `status` | declared | `active`, `stable`, `parked` or `archived`; absent when the entry gives none |
| `tagline` | declared | one sentence on what it does; absent when the entry gives none |
| `language` | checkout | `go`, `ruby` or `javascript`, from the manifest the checkout holds (`go.mod`, `Gemfile` or a gemspec, `package.json`), as the project loader infers a stack; absent with no manifest |
| `first_commit`, `last_commit` | checkout | the committer dates of the first and the last commit on the default branch; absent when it has no commits, or the read failed before them |
| `commits_per_month` | checkout | the commits on the default branch per calendar month (UTC), as `{month: "YYYY-MM", commits: n}`, from the first commit's month to `generated_at`'s, months with no commits included, so a renderer draws the rhythm without filling gaps; absent when `first_commit` is |
| `latest_tag` | checkout | `name` and `at` (its commit's committer date) of the newest tag reachable from the default branch; several tags on one commit are ordered by name and the last wins; absent with no tag |
| `error` | checkout | why reading the checkout failed, with the checkout's path removed from the text; the declared fields still print and the checkout fields read before the failure are kept; absent when the read succeeded |

**An anonymous node** is a private project whose declared entry gives an `alias`.
It carries exactly `id`, `kind` (`anonymous`) and `alias`, and its edges.
It has no name, role, tagline, language, date, commit count or tag, because a commit rhythm, a start date or a language can identify a private project to someone who knows its author.

**An edge** says that `from` depends on, or relates to, `to`.

| field | meaning |
|---|---|
| `from`, `to` | node ids |
| `kind` | `depends`, a dependency read from a manifest, or `link`, a relation the declared file states |
| `source` | where the edge came from: `manifest` for `depends`, `declared` for `link` |
| `manifest` | on a `depends` edge, the file it was read from: `go.mod`, `package.json`, `gemspec` or `Gemfile`; absent when `from` is an anonymous node |
| `dev` | on a `depends` edge, `true` when only development needs the dependency: a `devDependencies` entry, a Gemfile `development` or `test` group, `add_development_dependency`, or a go.mod `tool` requirement; absent otherwise, and always absent when `from` is an anonymous node |
| `relation` | on a `link` edge, the declared file's own words for it (`plugin for`, `replaces`) |

The edge rules:
- A depended-on project is matched by the name its own manifest declares, never by its directory name, because the two can differ: the checkout `jsonish` may be the module `github.com/acme/json`.
- An indirect Go requirement (`// indirect`) is not an edge, because the direct requirement that pulls it in is an edge of its own when both ends are in scope.
- A go.mod `replace` whose target is a project in scope, a fork or a local path, is an edge to that project.
- One manifest naming the same project twice is one edge.
- A `depends` edge from an anonymous node carries no `manifest` and no `dev`, because the manifest's file names the private project's language.
  Several manifests of one anonymous node naming the same project are then one edge.
- A `depends` edge and a `link` edge between the same two nodes are two edges.
- An edge is printed only when both its ends are nodes, so an edge to a dropped project, or to one outside the run's scope, is left out.
- An edge between two anonymous nodes is dropped, because it describes nothing public.

## The declared file

`~/.f10/atlas.md` is YAML frontmatter then free prose, as `~/.f10/board.md` is, and an unknown key is an error.
It has one key, `projects`, a map from a project key to that project's entry:

```yaml
---
projects:
  gears:
    role: Go library
    status: stable
    tagline: Small reflection helpers for everyday Go.
    public: true
  widgets:
    role: CLI
    status: active
    tagline: Arranges widgets from the command line.
    public: true
    links:
      - {to: gears, relation: showcase for}
  github.com/acme/client-app:
    alias: client-app
---
```

| key | meaning |
|---|---|
| `role`, `status`, `tagline` | the public node's fields of the same name |
| `public` | `true` exports the project in full; absent or `false` keeps it private |
| `alias` | the name a private project is exported under as an anonymous node: lowercase letters, digits and hyphens, starting with a letter or digit |
| `links` | a list of `{to: <project key>, relation: <words>}`, each a `link` edge from this project |

The rules:
- A project key is the checkout's name (the main checkout's basename) or its `id`.
  A key that matches two checkouts is an error naming both, and the `id` form tells them apart.
- A project in scope with no entry is private with no alias, so it is dropped.
- An entry naming no project in this run's scope is skipped, so one file written for `--all` also serves a run over one checkout.
  With `F10_ROOTS` set, a key or a link's `to` that matches no checkout under any root is an error naming the entry, because it is a typo rather than a project out of scope.
- An unknown status is an error naming the entry.
- Two entries with the same alias are an error naming both.
- An alias on a public entry is an error, because the entry asks both to name the project and to hide it.

## The privacy rule

Every later change keeps this rule:

1. A public project is exported in full.
2. A private project with a declared alias is exported as an anonymous node: its alias, its kind and its edges, the edges stripped of which manifest they came from.
3. A private project with no alias is dropped, with every edge that touches it.
4. f10 never assigns an alias, because an assigned one would change when a project is added.

Nothing in an anonymous node, its id included, comes from the project's name, remote, path or history.
Nothing in the atlas names a checkout path, a plan or a storage path.

## Conformance

The JSON example under **Shape** fits the schema, and the YAML example under **The declared file** fits its `Declared` definition.
`cli/internal/atlas/testdata/<name>.atlas.json` are documents that fit the schema, and `<name>.declared.md` are declared files whose frontmatter fits `Declared`.
`cli/internal/atlas/schema_test.go` checks all of them against the schema, and checks the rules the schema cannot express on the JSON example and the documents: the node and edge order, and no `manifest` or `dev` on an edge from an anonymous node.
It also checks that the schema rejects an anonymous node carrying any field beyond its three.

## Changing this spec

A field's meaning, an edge rule or the privacy rule changes here and in the fixtures first, with a version bump at the top, then in the implementation.
An optional field added with its meaning is written here and in the schema, the version kept.
Host facts (stars, open requests, releases) and delivery facts from the ledger join later as optional fields of a public node, and never of an anonymous one.
