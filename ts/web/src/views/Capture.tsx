import { useState } from "preact/hooks";

import type { TaskRef } from "../../../gen/f10/v1/tasks_pb";
import { message } from "../api";
import { api, projects } from "../store";
import { ProjectPicker, Section } from "./common";

export function Capture() {
  const [project, setProject] = useState(projects.value[0]?.id ?? "");
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [made, setMade] = useState<TaskRef | null>(null);

  async function submit(e: Event) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const res = await api.tasks.createTask({ project, title, body });
      setMade(res.task ?? null);
      setTitle("");
      setBody("");
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Section title="Capture a task">
      {made && (
        <p class="ok">
          Created{" "}
          {made.url ? (
            <a href={made.url} target="_blank" rel="noopener noreferrer">
              {made.id}
            </a>
          ) : (
            made.id
          )}
          {made.title && `: ${made.title}`}
        </p>
      )}
      <form class="card form" onSubmit={submit}>
        <ProjectPicker projects={projects.value} value={project} onChange={setProject} />
        <label>
          Title
          <input value={title} onInput={(e) => setTitle((e.target as HTMLInputElement).value)} required />
        </label>
        <label>
          Body (markdown)
          <textarea value={body} rows={8} onInput={(e) => setBody((e.target as HTMLTextAreaElement).value)} />
        </label>
        {error && <p class="error">{error}</p>}
        <button type="submit" disabled={busy || !title.trim() || !project.trim()}>
          {busy ? "Creating…" : "Create task"}
        </button>
      </form>
    </Section>
  );
}
