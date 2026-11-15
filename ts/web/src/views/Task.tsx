import { useEffect, useState } from "preact/hooks";

import { ArtifactKind, type Artifact } from "../../../gen/f10/v1/artifacts_pb";
import type { RunStatus } from "../../../gen/f10/v1/runs_pb";
import { message, P } from "../api";
import { ago } from "../format";
import { href } from "../router";
import { caller, can } from "../session";
import { api, runs } from "../store";
import { Checks, Phases, Section, Stopped } from "./common";
import { Forward } from "./Start";

export function kindWord(k: ArtifactKind): string {
  return ArtifactKind[k]?.toLowerCase() ?? "file";
}

/** shown leaves out the demo's supporting files, which the report shows itself. */
export function shown(list: Artifact[]): Artifact[] {
  return list.filter((a) => a.kind !== ArtifactKind.DEMO || a.name === "report.html");
}

export function TaskView(props: { root: string; task: string }) {
  const { root, task } = props;
  const [status, setStatus] = useState<RunStatus | null>(null);
  const [artifacts, setArtifacts] = useState<Artifact[] | null>(null);
  const [error, setError] = useState("");
  const c = caller.value;

  // the run list moves with live events; the task's own read follows it
  const tick = runs.value;

  useEffect(() => {
    let gone = false;
    (async () => {
      try {
        const [st, arts] = await Promise.all([
          api.runs.getRunStatus({ project: root, task }),
          can(c, P.artifacts) ? api.artifacts.listArtifacts({ project: root, task }) : null,
        ]);
        if (gone) return;
        setStatus(st.runs[0] ?? null);
        setArtifacts(arts?.artifacts ?? null);
        setError("");
      } catch (e) {
        if (!gone) setError(message(e));
      }
    })();
    return () => {
      gone = true;
    };
  }, [root, task, tick]);

  const r = status?.run;
  return (
    <>
      <Section title={task} aside={status && <Phases status={status} />}>
        {error && <p class="error">{error}</p>}
        {status?.shipment?.title && <p class="run-title">{status.shipment.title}</p>}
        {status?.shipment?.url && (
          <p>
            <a href={status.shipment.url} target="_blank" rel="noopener noreferrer">
              Open the task
            </a>
          </p>
        )}
        {r && (
          <p class="muted small">
            {r.skill} {r.leaf && `at ${r.leaf}`} · {ago(r.updated)}
          </p>
        )}
        {status && <Stopped status={status} />}
        {status && <Checks status={status} />}
        {r?.ask && (
          <p>
            <a href="/asks">Answer the ask</a>
          </p>
        )}
      </Section>
      {status && status.gates.length > 0 && (
        <Section title="Flow">
          <ol class="gates">
            {status.gates.map((g, i) => (
              <li key={i} class={`gate ${g.state}`}>
                <span>{g.label || g.step}</span>
                <span class="muted small">
                  {g.state}
                  {g.outcome?.verdict && ` · ${g.outcome.verdict}`}
                </span>
              </li>
            ))}
          </ol>
        </Section>
      )}
      {can(c, P.forward) && (
        <Section title="Steer">
          <Forward root={root} task={task} />
        </Section>
      )}
      {artifacts && (
        <Section title="Files">
          {shown(artifacts).length === 0 && <p class="muted">No plan, report or findings yet.</p>}
          <ul class="list">
            {shown(artifacts).map((a) => (
              <li key={`${a.kind}-${a.name}`}>
                <a href={href("/artifact", { root, t: task, k: kindWord(a.kind), n: a.name })}>{a.title}</a>
                <span class="muted small">
                  {" "}
                  {kindWord(a.kind)} · {ago(a.modified)}
                </span>
              </li>
            ))}
          </ul>
        </Section>
      )}
    </>
  );
}
