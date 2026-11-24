import { useState } from "preact/hooks";

import { StartMode } from "../../../gen/f10/v1/runs_pb";
import { message, P } from "../api";
import { caller, can } from "../session";
import { api, projects, refreshRuns } from "../store";
import { ProjectPicker, Section } from "./common";

function useForm() {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  async function run(f: () => Promise<string>) {
    setBusy(true);
    setError("");
    setDone("");
    try {
      setDone(await f());
      void refreshRuns();
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }
  return { busy, error, done, run };
}

const field = (set: (v: string) => void) => (e: Event) => set((e.target as HTMLInputElement).value);

/** taskList reads ids separated by spaces or commas; `--` chains them. */
export function taskList(raw: string): { ids: string[]; chain: boolean } {
  const chain = /(^|\s)--(\s|$)/.test(raw);
  const ids = raw
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter((s) => s && s !== "--");
  return { ids, chain };
}

/** startReply is what a start says it did. */
export function startReply(r: { batch: number; attached: boolean; task: string; branch: string }, typed: string) {
  if (r.attached) return `Joined batch ${r.batch}`;
  return r.batch ? `Batch ${r.batch} runs ${typed}` : `Started ${r.task} on ${r.branch}`;
}

// StartForm is f10 start's: one id or several, a range with --, and how far
// the agents run. A flow start runs as a batch, which opens its tasks after
// the call. A discussion opens one agent.
function StartForm() {
  const [project, setProject] = useState(projects.value[0]?.id ?? "");
  const [tasks, setTasks] = useState("");
  const [mode, setMode] = useState(StartMode.SHIP);
  const [flow, setFlow] = useState("");
  const [pace, setPace] = useState("");
  const [parallel, setParallel] = useState("");
  const f = useForm();

  return (
    <form
      class="card form"
      onSubmit={(e) => {
        e.preventDefault();
        void f.run(async () => {
          const { ids, chain } = taskList(tasks);
          const r = await api.runs.start({
            project,
            token: ids[0] ?? "",
            tasks: ids.slice(1),
            chain,
            mode,
            flow,
            pace,
            parallel: Number(parallel) || 0,
          });
          return startReply(r, ids.join(chain ? " -- " : " "));
        });
      }}
    >
      <h3>Start tasks</h3>
      <ProjectPicker projects={projects.value} value={project} onChange={setProject} />
      <label>
        Task ids
        <input value={tasks} onInput={field(setTasks)} placeholder="FT-1 FT-2, or FT-1 -- FT-2 for a range" required />
      </label>
      <label>
        Mode
        <select value={mode} onChange={(e) => setMode(Number((e.target as HTMLSelectElement).value))}>
          <option value={StartMode.SHIP}>ship</option>
          <option value={StartMode.PLAN}>plan</option>
          <option value={StartMode.DISCUSS}>discuss</option>
        </select>
      </label>
      <div class="row">
        <label>
          Flow
          <input value={flow} onInput={field(setFlow)} placeholder="project default" />
        </label>
        <label>
          Pace
          <input value={pace} onInput={field(setPace)} placeholder="default" />
        </label>
        <label>
          Parallel
          <input value={parallel} inputMode="numeric" onInput={field(setParallel)} />
        </label>
      </div>
      {f.error && <p class="error">{f.error}</p>}
      {f.done && <p class="ok">{f.done}</p>}
      <button type="submit" disabled={f.busy}>
        {f.busy ? "Starting…" : "Start"}
      </button>
    </form>
  );
}

export function Start() {
  const c = caller.value;
  return <Section title="Start">{can(c, P.start) && <StartForm />}</Section>;
}

export function Forward(props: { root: string; task: string }) {
  const [text, setText] = useState("");
  const f = useForm();
  return (
    <form
      class="card form"
      onSubmit={(e) => {
        e.preventDefault();
        void f.run(async () => {
          const r = await api.runs.forward({ project: props.root, task: props.task, text });
          setText("");
          return r.note ? `Sent. ${r.note}` : "Sent to the agent.";
        });
      }}
    >
      <label>
        Tell the agent
        <textarea
          value={text}
          rows={3}
          onInput={(e) => setText((e.target as HTMLTextAreaElement).value)}
          placeholder={`/f10:ship ${props.task}, or a note`}
          required
        />
      </label>
      {f.error && <p class="error">{f.error}</p>}
      {f.done && <p class="ok">{f.done}</p>}
      <button type="submit" disabled={f.busy || !text.trim()}>
        {f.busy ? "Sending…" : "Send"}
      </button>
    </form>
  );
}
