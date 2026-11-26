import { duration, money } from "./load";
import type { SetPayload } from "./types";

/** SetPage lists a report set's runs, each linked to its page when it has one. */
export function SetPage({ set }: { set: SetPayload }) {
  const passed = set.runs.filter((r) => r.report.pass).length;
  return (
    <main class="set">
      <header class="head">
        <h1>{set.name}</h1>
        <p>
          {passed}/{set.runs.length} passed
        </p>
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
        <tbody>
          {set.runs.map((r) => {
            const name = r.report.scenario + (r.report.repeat ? ` #${r.report.repeat}` : "");
            return (
              <tr key={r.file}>
                <td>{r.page ? <a href={r.page}>{name}</a> : name}</td>
                <td>{r.report.agent || "default"}</td>
                <td class={r.report.pass ? "ok" : "bad"}>{r.report.pass ? "pass" : "FAIL"}</td>
                <td class="marks">
                  {(r.report.checks ?? []).map((c) => (
                    <span key={c.name} class={c.pass ? "ok" : "bad"} title={c.detail || c.name}>
                      {c.pass ? "✓" : "✗"} {c.name}
                    </span>
                  ))}
                  {r.report.error && <span class="bad">error: {r.report.error}</span>}
                </td>
                <td>{money(r.report.cost_usd)}</td>
                <td>{duration(r.report.duration_ms)}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </main>
  );
}
