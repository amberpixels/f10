# Prompt evals

Skills, steps, modes and conventions make up half of what f10 does, and `just test` checks none of it.
A model update or a prose edit can change what `plan`, `ship`, `review` or `judge` produce, and no test fails.
`just eval` runs real sessions against small fixture repos and checks the artifacts they leave behind, not their wording.

It costs model spend and several minutes per scenario. Run it on demand and before a release, never in CI.

## Running

```
just eval                                  # every scenario
just eval -scenario feature-small          # one
just eval -permission-mode bypassPermissions -max-budget-usd 3 -keep
```

Each scenario runs `claude -p --plugin-dir <this checkout>` in a temporary copy of its fixture:

- **This checkout's plugin and binary run.** The runner first builds this checkout into the cache slot `bin/f10` uses (`bin/f10 --shim-rebuild`, as `just install` does), so the hooks, the skills and the lore check run the code the report's commit names, not the release. If the installed f10 plugin is also enabled, both plugins load: disable the installed one for the run, so its skills do not answer instead.
- **The run cannot reach your own state.** `F10_LEDGER`, `F10_STATE_DIR`, `F10_ASKS_DIR` and `F10_RUN_DIR` point inside the temp dir, so its hooks, badges and asks never reach your ledger, status line or asks inbox.
- **Nothing is pushed.** The fixture repo has no `origin`, and its tracker is a local driver that reads `tasks/`.
- **Permissions.** A driven ship runs verify and commits. Under the default `-permission-mode acceptEdits` those commands may be refused. Choose the mode yourself: the run is a throwaway directory, but the flag is yours to pass.
- **Flags.** `-keep` keeps the temp dir and prints its path, so you can read what the run left.

## Reports

Every scenario writes `evals/reports/<version>/<scenario>.json` with:

- pass/fail for each check;
- `cost_usd` and `duration_ms`, from claude's JSON output;
- the plugin version and the commit.

Commit the reports with a release. Two releases then compare by diffing their files.

## Scenarios

`evals/scenarios/<name>/` holds:

- `scenario.json`: `task`, `prompt`, the `verify` commands, the `accept` commands with the stdout each must print, and the `checks` to run. It stays outside the fixture, so the run never sees what it is checked against;
- `repo/`: the fixture. Its `f10/` is copied to `.f10/`, because a `.f10/` inside this repo would be excluded along with f10's own.

The checks (`cli/cmd/f10eval/checks.go`):

| check | passes when |
|---|---|
| `plan` | `.f10/plans/<task>.md` has a `Roles:` line, a stage, `## Consequences` and `## Gaps` |
| `verify` | every `verify` command exits 0 on the result |
| `accept` | every `accept` command prints its `want` (verify alone passes on the untouched fixture) |
| `commit` | the repo has a commit past the fixture's initial one |
| `findings` | every `where:` in `.f10/reviews/<task>/*.md` names a file that exists and a line it has |
| `verdict` | each `.f10/verdicts/<task>/*.md` carries one of the four verdicts |
| `lore` | `f10 lore validate <task>` passes |

`feature-small` is the first scenario: it adds a flag to a one-file CLI through `/f10:plan T-1 && /f10:ship T-1 --driven` on a `plan implement commit` flow.
A bug scenario (a failing test to fix) and a spike scenario are the next ones to add. The runner needs no change for them.
`go test ./cmd/f10eval` covers the checks against a canned result, with no model call.
