import type { RunStatus } from "../../../gen/f10/v1/runs_pb";
import { ago, projectLabel } from "../format";
import { href } from "../router";
import { runs, runsError } from "../store";
import { Checks, Phases, Section, Stopped } from "./common";

/** groups orders runs by project, then the runs waiting on someone first. */
export function groups(list: RunStatus[]): [string, RunStatus[]][] {
  const by = new Map<string, RunStatus[]>();
  for (const s of list) {
    const key = projectLabel(s.run?.root ?? "");
    by.set(key, [...(by.get(key) ?? []), s]);
  }
  const waiting = (s: RunStatus) => (s.run?.ask ? 0 : s.run?.note ? 1 : 2);
  return [...by.entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([k, v]) => [k, v.sort((a, b) => waiting(a) - waiting(b))]);
}

export function RunCard({ status }: { status: RunStatus }) {
  const r = status.run;
  const task = r?.task || status.shipment?.task || "";
  const title = status.shipment?.title;
  return (
    <div class={`card run ${r?.ask ? "has-ask" : ""}`}>
      <div class="run-head">
        <a class="run-link" href={href("/task", { root: r?.root, t: task })}>
          <strong>{task || r?.skill || "run"}</strong>
        </a>
        <Phases status={status} />
        <span class="muted small">{ago(r?.updated)}</span>
      </div>
      {title && <div class="run-title">{title}</div>}
      {r?.leaf && (
        <div class="muted small">
          at {r.leaf}
          {r.pipeline.length > 0 && r.at > 0 && ` (${r.at}/${r.pipeline.length})`}
        </div>
      )}
      <Stopped status={status} />
      <Checks status={status} />
    </div>
  );
}

export function Status() {
  const list = runs.value;
  return (
    <Section title="Runs">
      {runsError.value && <p class="error">{runsError.value}</p>}
      {list === null && <p class="muted">Loading…</p>}
      {list?.length === 0 && <p class="muted">No live runs on this machine.</p>}
      {list &&
        groups(list).map(([project, items]) => (
          <div key={project} class="group">
            <h3>{project}</h3>
            {items.map((s) => (
              <RunCard key={s.run?.session ?? s.shipment?.task} status={s} />
            ))}
          </div>
        ))}
    </Section>
  );
}
