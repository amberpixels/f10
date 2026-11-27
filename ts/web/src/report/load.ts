import type { RunPayload, Scenario } from "./types";

/** A call is one tool call: a tool_use paired with its tool_result by id. */
export interface Call {
  index: number;
  id: string;
  name: string;
  input: Record<string, unknown>;
  output: string;
  done: boolean;
  error: boolean;
  sub: boolean;
  at: number;
  cost: number;
  est: boolean;
  subject: string;
  /** The runner counts the call as f10 plumbing rather than work on the task. */
  plumbing: boolean;
  /** The file a file tool names, relative to the run's repo. */
  path?: string;
  /** The file a file tool names lies outside the run's repo. */
  outside?: boolean;
}

export interface Section {
  skill: string;
  calls: Call[];
}

export interface TurnEnd {
  cost: number;
  durationMs: number;
  result: string;
  error: boolean;
}

export interface Turn {
  n: number;
  prompt: string;
  sections: Section[];
  end?: TurnEnd;
}

export interface Model {
  cwd: string;
  turns: Turn[];
  calls: Call[];
  reply: string;
}

interface Block {
  type?: string;
  id?: string;
  name?: string;
  input?: Record<string, unknown>;
  tool_use_id?: string;
  content?: unknown;
  is_error?: boolean;
}

interface Event {
  type?: string;
  subtype?: string;
  cwd?: string;
  parent_tool_use_id?: string | null;
  message?: { content?: Block[] };
  total_cost_usd?: number;
  duration_ms?: number;
  result?: string;
  is_error?: boolean;
}

// the input fields that say what a call is about, the first present winning,
// as the runner's tool line picks them
const subjectKeys = ["file_path", "path", "pattern", "command", "skill", "url", "description", "prompt"];
const fileTools = new Set(["Write", "Edit", "MultiEdit", "Read", "NotebookEdit"]);

export function prompts(s: Scenario | undefined): string[] {
  if (s?.prompts?.length) return s.prompts;
  return s?.prompt ? [s.prompt] : [];
}

/** A slash command a prompt starts with reaches the stream as no Skill call. */
function promptSkill(prompt: string): string {
  const cmd = prompt.split(" ")[0] ?? "";
  return cmd.startsWith("/") ? cmd : "";
}

function outputText(content: unknown): string {
  if (typeof content === "string") return content;
  if (Array.isArray(content)) {
    return content
      .map((b: { type?: string; text?: string }) => (b?.type === "text" ? (b.text ?? "") : ""))
      .join("\n");
  }
  return "";
}

/** load turns a run's payload into turns, skill sections and calls. */
export function load(run: RunPayload): Model {
  const turnPrompts = prompts(run.scenario);
  const model: Model = { cwd: "", turns: [], calls: [], reply: "" };
  const byId = new Map<string, Call>();
  const plumbing = new Set(run.plumbing);
  let turn: Turn | undefined;
  let section: Section | undefined;

  const startTurn = () => {
    const prompt = turnPrompts[model.turns.length] ?? "";
    turn = { n: model.turns.length + 1, prompt, sections: [] };
    section = { skill: promptSkill(prompt), calls: [] };
    turn.sections.push(section);
    model.turns.push(turn);
  };

  run.transcript.forEach((line, i) => {
    let e: Event;
    try {
      e = JSON.parse(line) as Event;
    } catch {
      return;
    }
    if (!e || typeof e !== "object") return;

    if (e.type === "system" && e.subtype === "init" && e.cwd) model.cwd = e.cwd;
    // a turn opens on its first event after the last one's result
    if (!turn || turn.end) startTurn();

    if (e.type === "assistant") {
      for (const b of e.message?.content ?? []) {
        if (b.type !== "tool_use") continue;
        const input = b.input ?? {};
        if (b.name === "Skill" && typeof input.skill === "string") {
          const skill = "/" + input.skill.replace(/^\//, "");
          if (skill !== section!.skill) {
            section = { skill, calls: [] };
            turn!.sections.push(section);
          }
        }
        const call: Call = {
          index: model.calls.length,
          id: b.id ?? "",
          name: b.name ?? "",
          input,
          output: "",
          done: false,
          error: false,
          sub: !!e.parent_tool_use_id,
          at: run.times[i] ?? 0,
          cost: run.cost_usd[i] ?? 0,
          est: run.estimated[i] ?? false,
          subject: "",
          plumbing: plumbing.has(b.id ?? ""),
        };
        Object.assign(call, subjectOf(call, model.cwd));
        model.calls.push(call);
        section!.calls.push(call);
        if (call.id) byId.set(call.id, call);
      }
    } else if (e.type === "user") {
      for (const b of e.message?.content ?? []) {
        const call = b.type === "tool_result" ? byId.get(b.tool_use_id ?? "") : undefined;
        if (!call) continue;
        call.done = true;
        call.error = !!b.is_error;
        call.output = outputText(b.content);
      }
    } else if (e.type === "result") {
      turn!.end = {
        cost: e.total_cost_usd ?? 0,
        durationMs: e.duration_ms ?? 0,
        result: e.result ?? "",
        error: !!e.is_error,
      };
      model.reply = e.result ?? "";
    }
  });

  // drop the empty opening section a turn gets when its first call is a Skill
  for (const t of model.turns) {
    t.sections = t.sections.filter((s, i) => s.calls.length > 0 || (i === 0 && t.sections.length === 1));
  }
  return model;
}

function subjectOf(call: Call, cwd: string): Pick<Call, "subject" | "path" | "outside"> {
  const out: Pick<Call, "subject" | "path" | "outside"> = { subject: "" };
  for (const k of subjectKeys) {
    const v = call.input[k];
    if (typeof v !== "string" || !v.trim()) continue;
    let s = v.trim().split("\n")[0]!;
    if (cwd) s = s.split(cwd + "/").join("");
    out.subject = s;
    break;
  }
  const file = call.input.file_path;
  if (fileTools.has(call.name) && typeof file === "string") {
    const rel = repoPath(file, cwd);
    if (rel === undefined) out.outside = true;
    else out.path = rel;
  }
  return out;
}

/** repoPath is file relative to the run's repo, undefined when it lies outside. */
export function repoPath(file: string, cwd: string): string | undefined {
  if (!file.startsWith("/")) return file.replace(/^\.\//, "");
  if (cwd && file.startsWith(cwd + "/")) return file.slice(cwd.length + 1);
  return undefined;
}

export function money(usd: number, est = false): string {
  return (est ? "~$" : "$") + usd.toFixed(2);
}

/** clock is milliseconds as m:ss. */
export function clock(ms: number): string {
  const s = Math.floor(ms / 1000);
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}

/** duration is milliseconds as the runner prints a rounded Go duration. */
export function duration(ms: number): string {
  const s = Math.round(ms / 1000);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  if (h) return `${h}h${m}m${sec}s`;
  if (m) return `${m}m${sec}s`;
  return `${sec}s`;
}
