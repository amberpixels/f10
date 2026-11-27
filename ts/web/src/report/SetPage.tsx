import { duration, money } from "./load";
import type { SetPayload, SetRun } from "./types";

/** The order arms run and print in, as the runner's armOrder. */
const armOrder = ["raw", "native", "f10"];

/** SetPage lists a report set's runs, each linked to its page when it has one. An arms set groups them by arm. */
export function SetPage({ set }: { set: SetPayload }) {
  const passed = set.runs.filter((r) => r.report.pass).length;
  const groups = armGroups(set.runs);
  const leaks = [...new Set(set.runs.map((r) => r.report.isolation).filter((l): l is string => !!l))];
  return (
    <main class="set">
      <header class="head">
        <h1>{set.name}</h1>
        <p>
          {passed}/{set.runs.length} passed
        </p>
        {leaks.map((l) => (
          <p key={l} class="note">
            not isolated: {l}
          </p>
        ))}
      </header>
      <table>
        <thead>
          <tr>
            <th>run</th>
            <th>profile</th>
            <th>result</th>
            <th>checks</th>
            <th>cost</th>
            <th>time</th>
          </tr>
        </thead>
        {groups.map(([arm, runs]) => (
          <tbody key={arm}>
            {arm && (
              <tr class="arm">
                <th colSpan={6}>{arm}</th>
              </tr>
            )}
            {runs.map((r) => (
              <Row key={r.file} r={r} />
            ))}
          </tbody>
        ))}
      </table>
    </main>
  );
}

/** armGroups is the runs under one group per arm, in run order; a set with no arms is one unnamed group. */
function armGroups(runs: SetRun[]): [string, SetRun[]][] {
  if (!runs.some((r) => r.report.arm)) return [["", runs]];
  const arms = [...new Set(runs.map((r) => r.report.arm ?? ""))];
  arms.sort((a, b) => rank(a) - rank(b));
  return arms.map((a) => [a, runs.filter((r) => (r.report.arm ?? "") === a)]);
}

function rank(arm: string): number {
  const i = armOrder.indexOf(arm);
  return i < 0 ? armOrder.length : i;
}

function Row({ r }: { r: SetRun }) {
  const name =
    r.report.scenario + (r.report.repeat ? ` #${r.report.repeat}` : "");
  return (
    <tr>
      <td>{r.page ? <a href={r.page}>{name}</a> : name}</td>
      <td>{r.report.agent || "default"}</td>
      <td class={r.report.pass ? "ok" : "bad"}>
        {r.report.pass ? "pass" : "FAIL"}
      </td>
      <td class="marks">
        {(r.report.checks ?? []).map((c) => (
          <span
            key={c.name}
            class={c.pass ? "ok" : "bad"}
            title={c.detail || c.name}
          >
            {c.pass ? "✓" : "✗"} {c.name}
          </span>
        ))}
        {r.report.error && <span class="bad">error: {r.report.error}</span>}
      </td>
      <td>{money(r.report.cost_usd)}</td>
      <td>{duration(r.report.duration_ms)}</td>
    </tr>
  );
}
