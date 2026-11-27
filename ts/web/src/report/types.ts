// The payload the eval runner injects into a page (cli/cmd/f10eval/page.go).
// A run's part is its report and run folder, nearly as the folder holds
// them. docs/evals.md owns that layout.

export interface RubricLine {
  line: string;
  pass: boolean;
  reason: string;
}

export interface Check {
  name: string;
  pass: boolean;
  detail?: string;
  rubric?: { model: string; cost_usd: number; lines: RubricLine[] | null };
  planted?: { found: number; total: number; outside: number };
}

/** The part of a run spent on f10's own machinery, as the runner measured it. */
export interface Plumbing {
  calls: number;
  total_calls: number;
  seconds: number;
  cost_usd: number;
}

export interface Report {
  scenario: string;
  agent?: string;
  version: string;
  commit: string;
  repeat?: number;
  at: string;
  pass: boolean;
  checks: Check[] | null;
  cost_usd: number;
  grader_cost_usd?: number;
  duration_ms: number;
  plumbing?: Plumbing;
  error?: string;
  run_dir?: string;
  run_dir_error?: string;
}

/** A file of a tree. `skip` says why `text` is absent, and neither is an empty file. */
export interface PageText {
  text?: string;
  size: number;
  skip?: "binary" | "large" | "link";
}

export type Files = Record<string, PageText>;

export interface RunPayload {
  report: Report;
  scenario?: Scenario;
  task?: string;
  project?: string;
  /** transcript.jsonl line for line. times, cost_usd and estimated align with it. */
  transcript: string[];
  times: number[];
  cost_usd: number[];
  estimated: boolean[];
  /** The ids of the calls the runner counts as f10 plumbing. */
  plumbing: string[];
  diff: string;
  before: Files;
  after: Files;
}

export interface SetRun {
  file: string;
  report: Report;
  page?: string;
}

export interface SetPayload {
  name: string;
  runs: SetRun[];
}

export type Payload = { kind: "run"; run: RunPayload } | { kind: "set"; set: SetPayload };

/** The parts of inputs/scenario.json the page shows. */
export interface Scenario {
  description?: string;
  task?: string;
  prompt?: string;
  prompts?: string[];
  verify?: string[];
  accept?: { run: string; want: string }[];
  planted?: { file: string; from: number; to: number; what: string }[];
  rubric?: { lines?: string[] };
}
