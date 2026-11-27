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
just eval -scenario review-planted -repeat 5 -out evals/reports/base   # five runs, into a set of their own
just eval compare evals/reports/base evals/reports/cut                 # two sets side by side
just eval show evals/reports/0.71.0/feature-small.json                 # one run's timeline
```

Each scenario runs `claude -p --plugin-dir <this checkout>` in a temporary copy of its fixture:

- **This checkout's plugin and binary run.** The runner first builds this checkout into the cache slot `bin/f10` uses (`bin/f10 --shim-rebuild`, as `just install` does), so the hooks, the skills and the lore check run the code the report's commit names, not the release. If the installed f10 plugin is also enabled, both plugins load: disable the installed one for the run, so its skills do not answer instead.
- **The run cannot reach your own state.** `F10_LEDGER`, `F10_STATE_DIR`, `F10_INBOX_DIR`, `F10_RUN_DIR` and `F10_USER_DIR` point inside the temp dir, so its hooks, badges and asks never reach your ledger, status line or inbox, and its subagents resolve their profiles against the fixture's `agents:` map alone, never your `~/.f10/agents.md`. Two variables are dropped from the environment of every `claude` call the runner makes, the grader's included. `CLAUDECODE` is dropped so a run started from inside a session is not refused as nested. `ANTHROPIC_API_KEY` is dropped so every run uses the logged-in subscription and never bills the API, which means evals need a logged-in `claude`.
- **Nothing is pushed.** The fixture repo has no `origin`, and its tracker is a local driver that reads `tasks/`.
- **Permissions.** A driven ship runs verify and commits. Under the default `-permission-mode acceptEdits` those commands may be refused. Choose the mode yourself: the run is a throwaway directory, but the flag is yours to pass.
- **The agent profile.** Each run starts `claude -p` on one profile (`docs/project-setup.md` → Agents): the ones the scenario's `agents` list names, one run each, else one run on the kind's default; `-agent <profile>` runs every scenario on that one profile instead. The profile's model and effort go on the command line (`--model`, `--effort`), and a profile the kind refuses fails before any spend.
- **Flags.** `-keep` keeps the temp dir and prints its path, so you can read what the run left. `-repeat N` runs every selected scenario N times on each of its profiles, one run after another, because parallel sessions share the subscription's rate limits and would skew `duration_ms`. `-out <dir>` writes the reports there instead of `evals/reports/<version>/`. A relative path resolves against the repo root. A run refuses an `-out` that already holds reports of a scenario it would run, because a compare reads every report in the directory. `-repeat` above 1 needs `-out`, so its set never lands beside a committed release report.
- **Output.** Each turn runs with `--output-format stream-json --verbose`, so the runner reads the session as it happens. On a terminal, a block under the finished runs is redrawn every 100ms. Its first line is the running run, with a spinner, its elapsed time, its cost so far, and the skill, step and tool it is on. Every later run follows as `queued`, cut short with a `+N queued` line when the block would not fit the screen. The cost so far is an estimate, shown as `~$0.42`. It comes from each message's token usage and a small price table in `cli/cmd/f10eval/stream.go`. The turn's result then replaces it with claude's own figure, and the report only ever holds that figure. A finished run prints its result line, which ends with the plumbing's share of the run's time (`plumbing 23% (41s, $0.31)`), and a line per check, with a failed check's detail on its line. A `report` line follows with the `file://` url of the run's page (**Report page**, below). A `steps` line then holds the `just eval show` command, with the report's path relative to the repo root, so it pastes as is. A footer gives the runs passed, the total cost and time, and the url of the report set's index page. When stdout is not a terminal, or `NO_COLOR` is set, the output is plain lines with no escape codes. Each run prints a `started` line, then its result lines.

## Reports

Every run writes `evals/reports/<version>/<scenario>.json`.
A run on a profile other than the kind's default writes `<scenario>.<profile>.json` instead, the profile's colons as hyphens (`feature-small.opus-medium.json`).
Each report holds:

- pass/fail for each check;
- `cost_usd` and `duration_ms`, from the `result` event of each turn of claude's stream;
- `grader_cost_usd`, the rubric check's spend, kept apart from `cost_usd` so the run's cost compares across releases with and without a rubric;
- for a `rubric` check, the grader's model and a pass or fail with a one-line reason for each rubric line;
- the profile the run started on, as `agent`;
- the plugin version and the commit, `-dirty` appended when the tree had uncommitted changes, so the commit never names code that did not run;
- `run_dir`, the run's folder, relative to the report's directory, so a committed report carries no local path;
- `run_dir_error`, when writing the folder failed after the checks ran. It leaves `pass` and `error` as the run decided them, because a local copy failing says nothing about the agent.
- `plumbing: {calls, total_calls, seconds, cost_usd, run_seconds}`, the part of the run spent on f10's own machinery rather than on the task (**Plumbing**, below). A report without a transcript, or from before the field, has none.

### Plumbing

A tool call is plumbing when it is one of these:

- a Bash command whose every chained command is `f10-load-*.sh`, `f10-state.sh`, `f10 help`, `f10 forward`, `f10 lore` or `f10 trace`. Commands are split at `;`, `&&`, `||` and newlines outside quotes. A capture such as `trailers="$(f10 trace trailers ...)"` counts as the command inside it.
- a `Read` of a file under a `modes/`, `steps/` or `conventions/` directory outside the run's repo, which is the plugin's prose.
- a `Skill` call that loads an f10 skill.

Every other call is work.
A Bash call that chains plumbing and work, or runs a pipeline, counts as work, because its time cannot be split.
So the call count is a lower bound.
The seconds and the cost can go either way, because a message whose calls are all plumbing also owns its thinking and text.

Each transcript line owns the time since the line before it and the running cost estimate's growth over that time.
A model message counts as plumbing when all its calls are, and a tool result counts as plumbing when its call is.
This way parallel calls are never counted twice.
The share is `seconds` over `run_seconds`, the transcript's span on the same clock, so a turn killed before its result still counts in both.
`cost_usd` is the estimate's share, so it leaves out each turn's correction to claude's exact cost.
The rules live in `cli/cmd/f10eval/plumbing.go`, and a change to them changes every later number, so compare across such a change only against a fresh baseline.

Two profiles of one scenario sit side by side under one version, so a cheaper profile is justified by diffing its report against the dearer one on the same checks.

With `-repeat N` above 1, each run's file name carries its index before `.json`, as in `review-planted.3.json` or `feature-small.opus-medium.2.json`.
The report carries it too, as a `repeat` field.
A `planted` check also writes its counts as numbers, `planted: {found, total, outside}`, beside the detail sentence.

Commit the reports with a release. Sets made to compare a change stay uncommitted.
The run folders stay out of git (`.gitignore`), because a transcript runs to hundreds of KB and is full of absolute local paths.

### Run folder

Beside each `<report>.json` the runner writes `<report>/`, named like the report without `.json`. It is the input of the report page and of any later page that shows a run, so a change to this layout is a change to them:

- `transcript.jsonl`: claude's stream-json output, line for line as printed, every turn of the run. Each turn ends with its `result` event.
- `transcript.times`: one integer per transcript line, the milliseconds between the run's first turn starting and that line arriving. The stream's lines carry no time of their own.
- `diff.patch`: `git diff --binary` from the repo's initial commit to the tree the run left, untracked files included. A review scenario's planted change is in it too. `.f10/` is not, because it is excluded from the repo. It is in `after/` instead. Git's global and system config are ignored, so the patch has one format on every machine.
- `before/`: the repo as the run was handed it, without `.git`. The fixture, the history, the branch and the change are already in it.
- `after/`: the repo as the run left it, `.f10/` included, without `.git`.
- `inputs/scenario.json`: the scenario file as written, with the prompts, the verify and the hidden `accept` commands, the rubric lines and the planted defects. `inputs/task.md` and `inputs/project.md` are the fixture's `tasks/<task>.md` and `f10/instructions/project.md`, where it has them.
- `index.html`: the report page, written from the rest of the folder and the report. It is the one file here that is rebuilt, never read.

A run that errors in setup still writes the folder, with its inputs at least.
A rerun into the same directory replaces the folder with its report.

`just eval show <report.json>` prints the run's timeline from the folder.
It prints a heading per turn and per skill, then one line per tool call with its elapsed time and the cost so far.
A subagent's call is marked `sub`, and a call whose result was an error is marked `✗`.
Each turn ends with its exact cost, its duration and the first line of its reply.
A report written before run folders existed has no `run_dir`, and `show` says so.

### Report page

Each run folder gets an `index.html` that opens from `file://` with no server and no network.
It follows the system's light or dark theme.
The page is one file, because the runner inlines its script, its stylesheet and the run's data as JSON.
The script and stylesheet are built from `ts/web/src/report` into `cli/cmd/f10eval/page/`.
The data is the report and the folder nearly as written, and the page derives the rest.
That way a later page that shows a run can read the same folder.

The page shows:

- A header with the scenario, profile, version, commit, pass or fail, cost and time, and the plumbing's share of the time, its calls and its cost. A run that errored also shows its error.
- The checks, with each check's detail, the rubric reasons and the planted counts.
- The inputs: the prompts, the verify, the task and `project.md`. The `accept` commands and planted defects are marked as never shown to the agent.
- Three linked panes:
  - The timeline has a heading per turn and per skill, and a row per tool call with its elapsed time and running cost. Plumbing rows are muted, so the eye lands on the work. A row opens to show the call's input and output.
  - The file tree marks each file added (`A`), modified (`M`) or deleted (`D`), with a before and after toggle.
  - The viewer shows the chosen file, or its diff against `before/`.
- A click on a `Write` or `Edit` row shows that file as the step left it. The page rebuilds it by replaying the transcript's `Write` bodies and `Edit` pairs over `before/`. A failed call changes nothing. Shell commands are not replayed, so the after tree always comes from `after/`. A file that a shell command changed carries a note saying so.
- The final reply.

A file that is not UTF-8 text, or a symlink, is listed with its size and no text.
So is a file over 512KB, and any file past the first 8MB of a tree's text, which keeps the page small enough to open.
The set's directory gets an `index.html` too, with a row per report showing its checks, cost and time.
Each row links to its run's page when the run has one.

`just eval html <dir>` rebuilds every run page in a set from its folder, then the index, with no model call. Run it after a change to the page, or on a set from before pages existed.

## Comparing

One run is not a measurement, because a rerun of the same prompt can find a different number of planted defects.
So a prompt change is judged on several runs of each side:

1. On `main`: `just eval -scenario <name> -repeat 5 -out evals/reports/base`.
2. On the branch with the change: the same command with `-out evals/reports/<name>`.
   Each side runs in its own checkout, because a run loads the plugin of the checkout it runs from.
   The branch does not bump the version, so without `-out` the second set would overwrite the first.
3. `just eval compare <baseline-dir> <variant-dir>`. Give the baseline's path as an absolute path when it lives in the other checkout.

The command groups each set's reports by scenario and profile, read from the reports themselves, not their file names.
For each group it prints a row per metric, with the baseline and the variant side by side:

- `errors`: the runs that stopped with an error, as `k/N`. A run that errored in setup returned before its checks ran, so it counts in this row and in no other. A turn error, such as a spent budget, still ran the checks and spent money, so that run counts in every row.
- `pass` and each `check <name>`: passes as `k/N`.
- `planted found`, `planted outside`, `rubric lines passed`, `cost`, `duration`, `plumbing calls`, `plumbing time` and `plumbing cost`: the mean, then the min-max range and the sample count (`3.4 [3-4] n=5`).

A row is marked `better` or `worse` only when the difference exceeds the spread across runs:

- A numeric row is marked when the two ranges do not overlap, and each side has at least 2 samples. With 3 to 5 runs, ranges that do not overlap are a claim a reader can check by eye.
- A `k/N` row is marked when the rates differ by at least two runs of the smaller side, and each side has at least 3 samples. A range cannot mark it, because one pass and one fail already span the whole range. So 5/5 against 2/5 is marked, and 5/5 against 4/5 is not.

Higher is better for passes, planted found and rubric lines. Lower is better for errors, planted outside, cost, duration and the plumbing rows.
A report from before the `plumbing` field adds no sample, so an old baseline prints `-` in those rows and they are not marked.
`cost` is the run's own spend, without the grader's.
A group in only one set is listed as not compared.
The header warns when a group has one run, and when a set holds reports from more than one commit, which usually means two sets were mixed in one directory.
On a terminal `better` prints green and `worse` red.
The command exits 0 whatever it finds, because the comparison informs a decision and gates nothing.

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
`go test ./cmd/f10eval` makes no model call.
It covers the checks against a canned result and the rubric check against a canned grader.
It covers the stream reader, the run folder, the live view and `show` against a canned stream in `cli/cmd/f10eval/testdata/stream/`.
It covers the report page and `html` against a canned run folder in `cli/cmd/f10eval/testdata/runfolder/`.
The page's data loading and step replay have their own tests in `ts/web/src/report`, run by `just web::test`.
They read the same folder's `payload.json`, which `go test ./cmd/f10eval -run Golden -update` rewrites.
