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

function StartForm() {
  const [project, setProject] = useState(projects.value[0]?.id ?? "");
  const [task, setTask] = useState("");
  const [mode, setMode] = useState(StartMode.SHIP);
  const [flow, setFlow] = useState("");
  const [pace, setPace] = useState("");
  const f = useForm();

  return (
    <form
      class="card form"
      onSubmit={(e) => {
        e.preventDefault();
        void f.run(async () => {
          const r = await api.runs.start({ project, task, mode, flow, pace });
          return `Started ${r.task} on ${r.branch}`;
        });
      }}
    >
      <h3>Start a task</h3>
      <ProjectPicker projects={projects.value} value={project} onChange={setProject} />
      <label>
        Task id
        <input value={task} onInput={field(setTask)} required />
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
      </div>
      {f.error && <p class="error">{f.error}</p>}
      {f.done && <p class="ok">{f.done}</p>}
      <button type="submit" disabled={f.busy}>
        {f.busy ? "Starting…" : "Start"}
      </button>
    </form>
  );
}

/** taskList reads ids separated by spaces or commas; `--` chains them. */
export function taskList(raw: string): { ids: string[]; chain: boolean } {
  const chain = /(^|\s)--(\s|$)/.test(raw);
  const ids = raw
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter((s) => s && s !== "--");
  return { ids, chain };
}

function DriveForm() {
  const [project, setProject] = useState(projects.value[0]?.id ?? "");
  const [tasks, setTasks] = useState("");
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
          const r = await api.runs.drive({
            project,
            tasks: ids.map((id) => ({ id })),
            chain,
            flow,
            pace,
            parallel: Number(parallel) || 0,
          });
          return r.attached ? `Joined drive ${r.drive}` : `Drive ${r.drive || ""} started`;
        });
      }}
    >
      <h3>Drive tasks</h3>
      <ProjectPicker projects={projects.value} value={project} onChange={setProject} />
      <label>
        Task ids
        <input value={tasks} onInput={field(setTasks)} placeholder="FT-1 FT-2, or FT-1 -- FT-2 to chain" required />
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
        {f.busy ? "Starting…" : "Drive"}
      </button>
    </form>
  );
}

export function Start() {
  const c = caller.value;
  return (
    <Section title="Start or drive">
      {can(c, P.start) && <StartForm />}
      {can(c, P.drive) && <DriveForm />}
    </Section>
  );
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
