# Prompt evals

Skills, steps, modes and conventions make up half of what f10 does, and `just test` checks none of it.
A model update or a prose edit can change what `plan`, `ship`, `review` or `judge` produce, and no test fails.
`just eval` runs real sessions against small fixture repos and checks the artifacts they leave behind, not their wording.
The one exception is the `rubric` check, in which a pinned model grades the wording against lines the scenario states.
It covers output that is prose, such as a brainstorm reply or a plan's quality.

It costs model spend and several minutes per scenario. Run it on demand and before a release, never in CI.

## Running

```
just eval                                  # every scenario
just eval -scenario feature-small          # one
just eval -permission-mode bypassPermissions -max-budget-usd 3 -keep
just eval -scenario feature-small -agent opus:medium   # one profile, instead of the ones the scenario lists
```

Each scenario runs `claude -p --plugin-dir <this checkout>` in a temporary copy of its fixture:

- **This checkout's plugin and binary run.** The runner first builds this checkout into the cache slot `bin/f10` uses (`bin/f10 --shim-rebuild`, as `just install` does), so the hooks, the skills and the lore check run the code the report's commit names, not the release. If the installed f10 plugin is also enabled, both plugins load: disable the installed one for the run, so its skills do not answer instead.
- **The run cannot reach your own state.** `F10_LEDGER`, `F10_STATE_DIR`, `F10_INBOX_DIR`, `F10_RUN_DIR` and `F10_USER_DIR` point inside the temp dir, so its hooks, badges and asks never reach your ledger, status line or inbox, and its subagents resolve their profiles against the fixture's `agents:` map alone, never your `~/.f10/agents.md`. Two variables are dropped from the environment of every `claude` call the runner makes, the grader's included. `CLAUDECODE` is dropped so a run started from inside a session is not refused as nested. `ANTHROPIC_API_KEY` is dropped so every run uses the logged-in subscription and never bills the API, which means evals need a logged-in `claude`.
- **Nothing is pushed.** The fixture repo has no `origin`, and its tracker is a local driver that reads `tasks/`.
- **Permissions.** A driven ship runs verify and commits. Under the default `-permission-mode acceptEdits` those commands may be refused. Choose the mode yourself: the run is a throwaway directory, but the flag is yours to pass.
- **The agent profile.** Each run starts `claude -p` on one profile (`docs/project-setup.md` → Agents): the ones the scenario's `agents` list names, one run each, else one run on the kind's default; `-agent <profile>` runs every scenario on that one profile instead. The profile's model and effort go on the command line (`--model`, `--effort`), and a profile the kind refuses fails before any spend.
- **Flags.** `-keep` keeps the temp dir and prints its path, so you can read what the run left.

## Reports

Every run writes `evals/reports/<version>/<scenario>.json`.
A run on a profile other than the kind's default writes `<scenario>.<profile>.json` instead, the profile's colons as hyphens (`feature-small.opus-medium.json`).
Each report holds:

- pass/fail for each check;
- `cost_usd` and `duration_ms`, from claude's JSON output;
- `grader_cost_usd`, the rubric check's spend, kept apart from `cost_usd` so the run's cost compares across releases with and without a rubric;
- for a `rubric` check, the grader's model and a pass or fail with a one-line reason for each rubric line;
- the profile the run started on, as `agent`;
- the plugin version and the commit, `-dirty` appended when the tree had uncommitted changes, so the commit never names code that did not run.

Two profiles of one scenario sit side by side under one version, so a cheaper profile is justified by diffing its report against the dearer one on the same checks.

Commit the reports with a release. Two releases then compare by diffing their files.

## Scenarios

`evals/scenarios/<name>/` holds:

- `scenario.json`: `task`, `prompt`, the `verify` commands, the `accept` commands with the stdout each must print, the `checks` to run, and the `agents` profiles it runs on (none: one run on the kind's default). It stays outside the fixture, so the run never sees what it is checked against;
- `repo/`: the fixture. Its `f10/` is copied to `.f10/`, because a `.f10/` inside this repo would be excluded along with f10's own.

A review scenario adds three fields to `scenario.json`:

- `change`: a patch, relative to the scenario's directory. It is committed on a branch `<task>/change` after the initial commit, so the run has a branch to review against `main`.
- `planted`: the defects the change carries on purpose, each `{file, from, to, what}`. They stay outside the fixture, so the reviewer never sees them.
- `max_extra`: how many findings may land outside every planted defect, 2 when absent, because a real nit is not noise.

`fixture` names another scenario whose `repo/` to run in, so two scenarios review one fixture with different prompts.

A fixture may also hold a saved plan under `f10/plans/<task>.md`, which lands in `.f10/plans/`.
`/f10:ship` reuses a saved plan, so the run grades implement alone, and an edit to the plan step cannot move its result.

A multi-turn scenario runs one conversation, the way a person would answer between skills:

- `prompts`: the turns, in place of `prompt`. The first runs as `prompt` does. Each later one runs `claude -p --resume <session>` on the session the turn before printed. The report sums the turns' cost and duration. A rubric grades the last turn's reply. A turn that fails ends the run, and the error names its index. `-max-budget-usd` caps each turn, so a scenario of five turns may spend five times it.
- `branch`: a branch checked out after the initial commit, so the commits the run makes have `main` to be reviewed against. A scenario with a `change` has its own branch, so it never names one.

A capture scenario runs on a fixture tracker that creates tasks.
The `capture-simple` fixture's driver writes `tasks/EV-<n>.md`, numbered past the highest one there.
A subtask's file has a `Parent: EV-<m>` line under the status line.
It also serves `children` and `update --parent` / `--after`, the verbs a split needs.

- `captured`: the shape the run's tasks must have, `{tasks, parents, subtasks, after}`. It counts every task file the fixture's initial commit did not hold, so a task a later turn committed still counts. Each `After:` must name a sibling under the same parent. That sibling must wait on nothing, which is the shape of a split three levels deep.
- The check and the field come together, or the scenario fails to load.

A judge scenario names the verdict its subject deserves:

- `want_verdict`: one of the four verdicts. The `verdict` check then also fails unless the newest verdict file, by its number, carries it.
- The subject is a task, so the verdict file is keyed by the task id the check reads, because an idea's file is keyed by a slug no scenario can predict.

Brainstorm and capture run under `claude -p`, which answers no question.
So each prompt gives the step everything it needs, and a run that asks anyway fails its check, which is the signal wanted.

A history scenario gives the run a past to plan against:

- `history`: commits made on `main` after the initial one, each `{message, file, append}`, the message carrying the `Task:` trailer of the task it stands for.
- `reviews`: review rounds seeded into the run's ledger, under the project the run records itself as. Each entry is `{task, rounds, categories}`. Each round is round `1`, then `2`, at a reviewed sha of its own.
- `history_cites`: the ids the plan's `## History` section must name.

A rubric scenario lists `rubric` in its `checks` and grades prose against lines it states:

- `rubric`: `{lines, reply, files}`. `lines` are the statements the output must meet, each graded on its own. `reply: true` grades the run's final reply. `files` grades files by their path in the fixture repo, such as `.f10/plans/EV-1.md`. A scenario needs at least one of the two.
- The runner loads a scenario only when the check and the `rubric` field come together. So a rubric that would never run fails before any spend, and so does a check with nothing to grade.
- The grader is one `claude -p` call on a pinned model, `graderModel` in `cli/cmd/f10eval/rubric.go`. It is a full id rather than an alias, so a model release never changes a grade without a diff. It runs in an empty directory with no tools, settings, MCP servers or skills, and its own system prompt. Its prompt holds only the rubric lines and the graded output, so a change in the grader's context never reads as a change in the skill. `--bare` would isolate it further, but it signs in only with an API key.
- A missing reply or file fails the check by name, without a grader call.
- Unlike every other check, a rubric can pass one run and fail the next on the same output. Read the reasons in the report before calling a flipped line a regression.

```json
"checks": ["plan", "rubric"],
"rubric": {
  "files": [".f10/plans/EV-1.md"],
  "lines": ["Each stage names the files it changes", "The plan says why the flag defaults to off"]
}
```

The checks (`cli/cmd/f10eval/checks.go`):

| check | passes when |
|---|---|
| `plan` | `.f10/plans/<task>.md` has a `Roles:` line, a stage, `## Consequences` and `## Gaps`, which a plan of every size carries |
| `intent` | `.f10/lore/<task>/` holds an `intent` entry, the one plan writes |
| `verify` | every `verify` command exits 0 on the result |
| `accept` | every `accept` command prints its `want` (verify alone passes on the untouched fixture) |
| `commit` | the repo has a commit past the fixture's initial one |
| `findings` | every `where:` in `.f10/reviews/<task>/*.md` names a file that exists and a line it has |
| `verdict` | each `.f10/verdicts/<task>/*.md` carries one of the four verdicts, and the newest carries `want_verdict` when the scenario names one |
| `lore` | `f10 lore validate <task>` passes |
| `history` | the plan has a `## History` section naming every id in `history_cites` |
| `rubric` | the grader passes every rubric line on the reply and files the `rubric` field names |
| `captured` | the tasks the run added to `tasks/` have the shape `captured` names, and every `After:` waits on a sibling that waits on nothing; the detail gives the counts found on a pass too |
| `planted` | every planted defect has a finding whose `where:` falls in its lines, and at most `max_extra` findings fall outside them; the detail gives both counts on a pass too |

`feature-small` is the first scenario: it adds a flag to a one-file CLI through `/f10:plan EV-1 && /f10:ship EV-1 --driven` on a `plan implement commit` flow.
It runs once at `opus:high` and once at `opus:medium`, so the two reports compare cost and duration on the same checks.
`review-planted` reviews a branch that plants four defects in a small HTTP server: an off-by-one page, a file never closed, a path traversal and an unbounded body read. It runs `/f10:review EV-2 --driven`, the blind reviewer.
Three of its four defects fall in security and performance, which keeps three findings.
So a miss there can mean the reviewer ranked another real finding above a planted one.
Read the findings file before blaming the reviewer.
`review-planted-host` runs the same fixture and change through `/f10:review EV-2 host --driven`, the host engine, so the two reports compare the reviewers finding for finding on the `planted` check.
`plan-history` plans `feature-small`'s task after three earlier tasks touched `main.go`, each with two review rounds raising a tests-and-docs finding, through `/f10:plan EV-1 --driven`. Its `history` check passes when the plan's `## History` names all three, which `f10 history` printed as a recurring finding and a median of 2 rounds.

Each skill has scenarios of its own on fixed inputs, so a failing report names the stage that broke:

- **brainstorm**: `brainstorm-three` puts three ideas for the notes server in one prompt. `rubric` grades the first reply on three points:
  - Graceful shutdown is built as asked.
  - Search comes back as two or more shapes with their trade-offs.
  - A health endpoint gets "don't build", because the server already has `GET /health`.
- **capture**: these run on the `capture-simple` tracker, which starts empty. `capture-simple` must make one task. `capture-moderate` must make one task, and `rubric` grades its body for the problem, the scope and an _Areas_ note. `capture-split` must make a parent with four subtasks, two of them `After:` the other two. The parent is created first, so `rubric` grades `tasks/EV-1.md` as the parent.
- **implement**: `implement-ruby` and `implement-js` ship a saved plan for a search method on an `implement commit` flow. Hidden `accept` commands grade them. `feature-small` is the Go one.
- **review**: `review-planted-ruby` and `review-planted-js` plant the same four kinds of defect as `review-planted`, each in its own language. They run in the implement fixtures' repos.
- **judge**: `judge-proceed`, `judge-changes`, `judge-rethink` and `judge-stop` each judge one task in a copy of the notes server. Each task is written so its right verdict is not close to the next one:
  - proceed: a correct startup check.
  - proceed with changes: a delete that joins the name unchecked, where `save` validates it.
  - rethink: a lower size limit for memory spikes, whose cause is reading the whole body first.
  - stop: a health endpoint that already exists.

`chain-smoke` is a release smoke test of the hand-offs, not of their quality.
It runs five turns of one conversation on a branch: brainstorm, an agreed shape, capture, plan and ship, then review.
The tracker starts empty, so the captured task is `EV-1`, and the later turns name it.
A good run's settled statement, task file and plan can be frozen as the fixed inputs of the per-stage scenarios.
The implement scenarios already take a saved plan as their input in the same way.

A bug scenario (a failing test to fix) and a spike scenario are the next ones to add. The runner needs no change for them.
`go test ./cmd/f10eval` covers the checks against a canned result, and the rubric check against a canned grader, with no model call.
