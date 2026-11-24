import { useState } from "preact/hooks";

import { AnswerSource, type Ask } from "../../../gen/agents/v1/asks_pb";
import { message, P } from "../api";
import { ago } from "../format";
import { caller, can } from "../session";
import { answeredAsks, api, asksError, openAsks, refreshAsks, refreshRuns } from "../store";
import { Section } from "./common";

/** initialAnswers preselects each question's default, its first option. */
export function initialAnswers(ask: Ask): Record<number, string> {
  const out: Record<number, string> = {};
  for (const q of ask.questions) out[q.n] = q.options[0] ?? "";
  return out;
}

export function AskForm(props: {
  ask: Ask;
  canAnswer: boolean;
  send: (id: string, answers: { n: number; value: string }[]) => Promise<boolean>;
}) {
  const { ask } = props;
  const [values, setValues] = useState(() => initialAnswers(ask));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit(e: Event) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const answers = ask.questions.map((q) => ({ n: q.n, value: values[q.n] ?? "" }));
      await props.send(ask.id, answers);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  // an ask raised before the label was renamed carries it as drive
  const batch = ask.labels.batch ?? ask.labels.drive;
  const where = [ask.labels.task, ask.labels.project, batch && `batch ${batch}`]
    .filter(Boolean)
    .join(" · ");

  return (
    <form class="card ask" onSubmit={submit}>
      <div class="run-head">
        <strong>{where || ask.agent}</strong>
        <span class="muted small">{ago(ask.askedAt)}</span>
      </div>
      {ask.questions.length === 0 && <p>{ask.text}</p>}
      {ask.questions.map((q) => (
        <fieldset key={q.n} disabled={!props.canAnswer || busy}>
          <legend>
            {q.n}. {q.text}
          </legend>
          {q.options.length > 0 ? (
            <div class="options" role="radiogroup">
              {q.options.map((o, i) => (
                <label key={o} class={`option ${values[q.n] === o ? "chosen" : ""}`}>
                  <input
                    type="radio"
                    name={`${ask.id}-${q.n}`}
                    value={o}
                    checked={values[q.n] === o}
                    onChange={() => setValues((v) => ({ ...v, [q.n]: o }))}
                  />
                  {o}
                  {i === 0 && <span class="muted small"> default</span>}
                </label>
              ))}
            </div>
          ) : (
            <textarea
              value={values[q.n]}
              onInput={(e) => {
                const value = (e.target as HTMLTextAreaElement).value;
                setValues((v) => ({ ...v, [q.n]: value }));
              }}
              rows={2}
            />
          )}
        </fieldset>
      ))}
      {error && <p class="error">{error}</p>}
      {props.canAnswer ? (
        <button type="submit" disabled={busy}>
          {busy ? "Sending…" : "Answer"}
        </button>
      ) : (
        <p class="muted small">This device's token cannot answer asks.</p>
      )}
    </form>
  );
}

async function send(id: string, answers: { n: number; value: string }[]) {
  const res = await api.asks.answerAsk({ id, answers });
  await Promise.all([refreshAsks(), refreshRuns()]);
  return res.delivered;
}

export function Inbox() {
  const open = openAsks.value;
  const canAnswer = can(caller.value, P.answerAsk);
  return (
    <>
      <Section title="Open asks">
        {asksError.value && <p class="error">{asksError.value}</p>}
        {open === null && <p class="muted">Loading…</p>}
        {open?.length === 0 && <p class="muted">No agent is waiting on you.</p>}
        {open?.map((a) => (
          <AskForm key={a.id} ask={a} canAnswer={canAnswer} send={send} />
        ))}
      </Section>
      {answeredAsks.value.length > 0 && (
        <Section title="Answered today">
          {answeredAsks.value.map((a) => (
            <div key={a.id} class="card answered">
              <div class="run-head">
                <strong>{a.labels.task || a.agent}</strong>
                <span class="muted small">
                  {AnswerSource[a.answeredBy]?.toLowerCase()} · {ago(a.answeredAt)}
                </span>
              </div>
              <p>{a.answer}</p>
              {!a.deliveredAt && <p class="muted small">Not delivered to the agent yet.</p>}
            </div>
          ))}
        </Section>
      )}
    </>
  );
}
