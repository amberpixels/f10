import type { ComponentChildren } from "preact";

import type { KnownProject } from "../../../gen/f10/v1/projects_pb";
import { PhaseStatus, type RunStatus } from "../../../gen/f10/v1/runs_pb";
import { phaseWord } from "../format";

export function Phases({ status }: { status: RunStatus }) {
  const r = status.run;
  if (!r) return null;
  const phases: [string, PhaseStatus][] = [
    ["capture", r.capture],
    ["plan", r.plan],
    ["ship", r.ship],
  ];
  return (
    <span class="phases" aria-label="phases">
      {phases.map(([name, p]) => (
        <span key={name} class={`phase ${phaseWord(p)}`} title={`${name}: ${phaseWord(p)}`}>
          {name[0]}
        </span>
      ))}
    </span>
  );
}

/** stopped says why a run stopped and what unblocks it, empty while it runs. */
export function Stopped({ status }: { status: RunStatus }) {
  const r = status.run;
  if (!r || !(r.note || r.ask || r.wait)) return null;
  return (
    <div class="stopped">
      {r.ask && <p class="ask-line">Asks: {r.ask}</p>}
      {r.wait && <p>Waiting on {r.wait}</p>}
      {r.note && <p>{r.note}</p>}
      {r.next && <p class="muted">Next: {r.next}</p>}
    </div>
  );
}

export function Checks({ status }: { status: RunStatus }) {
  const s = status.shipment;
  if (!s?.pr) return null;
  const c = s.checks;
  return (
    <div class="checks">
      <a href={s.pr} target="_blank" rel="noopener noreferrer">
        {s.pr.replace(/^https?:\/\/[^/]+\//, "")}
      </a>
      {s.prState && <span class="tag">{s.prState.toLowerCase()}</span>}
      {c?.state && <span class={`tag checks-${c.state}`}>checks {c.state}</span>}
      {c?.failed.map((f) => (
        <div key={f.name} class="failed-check">
          ✕{" "}
          {f.url ? (
            <a href={f.url} target="_blank" rel="noopener noreferrer">
              {f.name}
            </a>
          ) : (
            f.name
          )}
        </div>
      ))}
    </div>
  );
}

export function ProjectPicker(props: {
  projects: KnownProject[];
  value: string;
  onChange: (v: string) => void;
}) {
  return (
    <label>
      Project
      <input
        list="known-projects"
        value={props.value}
        onInput={(e) => props.onChange((e.target as HTMLInputElement).value)}
        placeholder="github.com/owner/repo"
        required
      />
      <datalist id="known-projects">
        {props.projects.map((p) => (
          <option key={p.id} value={p.id}>
            {p.name}
          </option>
        ))}
      </datalist>
    </label>
  );
}

export function Section(props: { title: string; children: ComponentChildren; aside?: ComponentChildren }) {
  return (
    <section class="section">
      <header class="section-head">
        <h2>{props.title}</h2>
        {props.aside}
      </header>
      {props.children}
    </section>
  );
}
