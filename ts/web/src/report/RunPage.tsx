import { useMemo, useState } from "preact/hooks";

import { renderMarkdown } from "../markdown";
import { Code } from "./Code";
import { clock, duration, load, money, prompts, type Call, type Model } from "./load";
import { edits, shellChanged, stateAt, status, type Status } from "./replay";
import type { Check, Report, RunPayload } from "./types";

type Side = "before" | "after";

/** A selection is a file in the viewer, as a step left it when `at` names a call. */
interface Selection {
  path: string;
  at?: number;
}

export function RunPage({ run }: { run: RunPayload }) {
  const model = useMemo(() => load(run), [run]);
  const changes = useMemo(() => status(run.before, run.after), [run]);
  const shell = useMemo(() => shellChanged(model, run.before, run.after), [model, run]);
  const [side, setSide] = useState<Side>("after");
  const [diff, setDiff] = useState(true);
  const [sel, setSel] = useState<Selection | undefined>(() => {
    const first = [...changes].sort().find(([, s]) => s !== "untouched");
    return first && { path: first[0] };
  });

  return (
    <main class="run">
      <Header report={run.report} />
      <Checks checks={run.report.checks ?? []} />
      <Inputs run={run} />
      <section class="panes">
        <Timeline model={model} selected={sel} onSelect={setSel} />
        <FileTree
          run={run}
          changes={changes}
          side={side}
          setSide={setSide}
          selected={sel?.at === undefined ? sel?.path : undefined}
          onSelect={(path) => setSel({ path })}
        />
        <Viewer run={run} model={model} sel={sel} side={side} diff={diff} setDiff={setDiff} shell={shell} />
      </section>
      <Reply text={model.reply} />
    </main>
  );
}

function Mark({ pass }: { pass: boolean }) {
  return <span class={pass ? "ok" : "bad"}>{pass ? "✓" : "✗"}</span>;
}

function Header({ report }: { report: Report }) {
  const grader = report.grader_cost_usd ? ` + ${money(report.grader_cost_usd)} grader` : "";
  return (
    <header class="head">
      <h1>
        <Mark pass={report.pass} /> {report.scenario}
        {report.repeat ? ` #${report.repeat}` : ""}
      </h1>
      <dl class="facts">
        <dt>profile</dt>
        <dd>{report.agent || "default"}</dd>
        <dt>result</dt>
        <dd class={report.pass ? "ok" : "bad"}>{report.pass ? "pass" : "FAIL"}</dd>
        <dt>cost</dt>
        <dd>
          {money(report.cost_usd)}
          {grader}
        </dd>
        <dt>time</dt>
        <dd>{duration(report.duration_ms)}</dd>
        <dt>version</dt>
        <dd>{report.version}</dd>
        <dt>commit</dt>
        <dd>
          <code>{report.commit}</code>
        </dd>
        <dt>at</dt>
        <dd>{report.at}</dd>
      </dl>
      {report.error && <p class="error">error: {report.error}</p>}
      {report.run_dir_error && <p class="note">run folder: {report.run_dir_error}. Parts of this page may be missing.</p>}
    </header>
  );
}

function Checks({ checks }: { checks: Check[] }) {
  if (!checks.length) return null;
  return (
    <section class="checks">
      <h2>Checks</h2>
      <ul>
        {checks.map((c) => (
          <li key={c.name}>
            <Mark pass={c.pass} /> <b>{c.name}</b>
            {c.detail && <span class="detail"> {c.detail}</span>}
            {c.planted && (
              <span class="detail">
                {" "}
                found {c.planted.found}/{c.planted.total}, {c.planted.outside} outside
              </span>
            )}
            {c.rubric?.lines && (
              <ul class="rubric">
                {c.rubric.lines.map((l, i) => (
                  <li key={i}>
                    <Mark pass={l.pass} /> {l.line}
                    <div class="detail">{l.reason}</div>
                  </li>
                ))}
              </ul>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}

function Inputs({ run }: { run: RunPayload }) {
  const s = run.scenario;
  return (
    <details class="inputs">
      <summary>Inputs</summary>
      {s?.description && <p>{s.description}</p>}
      <h3>Prompts</h3>
      <ol>
        {prompts(s).map((p, i) => (
          <li key={i}>
            <code>{p}</code>
          </li>
        ))}
      </ol>
      {!!s?.verify?.length && (
        <>
          <h3>Verify</h3>
          <pre>{s.verify.join("\n")}</pre>
        </>
      )}
      {!!s?.accept?.length && (
        <>
          <h3>
            Accept <span class="hidden-mark">never shown to the agent</span>
          </h3>
          <ul>
            {s.accept.map((a, i) => (
              <li key={i}>
                <code>{a.run}</code> wants <code>{a.want}</code>
              </li>
            ))}
          </ul>
        </>
      )}
      {!!s?.planted?.length && (
        <>
          <h3>
            Planted defects <span class="hidden-mark">never shown to the agent</span>
          </h3>
          <ul>
            {s.planted.map((p, i) => (
              <li key={i}>
                <code>
                  {p.file}:{p.from}-{p.to}
                </code>{" "}
                {p.what}
              </li>
            ))}
          </ul>
        </>
      )}
      {run.task && (
        <>
          <h3>Task</h3>
          <div class="md" dangerouslySetInnerHTML={{ __html: renderMarkdown(run.task) }} />
        </>
      )}
      {run.project && (
        <>
          <h3>project.md</h3>
          <pre>{run.project}</pre>
        </>
      )}
    </details>
  );
}

function Timeline({
  model,
  selected,
  onSelect,
}: {
  model: Model;
  selected?: Selection;
  onSelect: (s: Selection) => void;
}) {
  const [open, setOpen] = useState<Set<number>>(new Set());
  const toggle = (c: Call) => {
    const next = new Set(open);
    if (next.has(c.index)) next.delete(c.index);
    else next.add(c.index);
    setOpen(next);
    if (edits(c)) onSelect({ path: c.path!, at: c.index });
  };

  return (
    <section class="pane timeline">
      <h2>Timeline</h2>
      {model.turns.map((t) => (
        <div class="turn" key={t.n}>
          <h3>
            turn {t.n} {t.prompt && <code>{t.prompt}</code>}
          </h3>
          {t.sections.map((s, i) => (
            <div class="section" key={i}>
              {s.skill && <h4>{s.skill}</h4>}
              {s.calls.map((c) => (
                <CallRow
                  key={c.index}
                  call={c}
                  open={open.has(c.index)}
                  current={selected?.at === c.index}
                  onClick={() => toggle(c)}
                />
              ))}
            </div>
          ))}
          {t.end && (
            <div class="end">
              end {money(t.end.cost)} {duration(t.end.durationMs)}{" "}
              <span class={t.end.error ? "bad" : ""}>{t.end.result.trim().split("\n")[0]}</span>
            </div>
          )}
        </div>
      ))}
    </section>
  );
}

function CallRow({ call, open, current, onClick }: { call: Call; open: boolean; current: boolean; onClick: () => void }) {
  return (
    <div class={`call${current ? " current" : ""}`}>
      <button type="button" class="row" onClick={onClick} aria-expanded={open}>
        <span class="at">{clock(call.at)}</span>
        <span class="cost">{money(call.cost, call.est)}</span>
        {call.sub && <span class="sub">sub</span>}
        <span class="name">{call.name}</span>
        <span class="subject">{call.subject}</span>
        {call.error && <span class="bad">✗</span>}
        {call.outside && <span class="muted">outside the repo</span>}
      </button>
      {open && (
        <div class="io">
          <h5>input</h5>
          <pre>{inputText(call)}</pre>
          <h5>output{call.done ? "" : " (none)"}</h5>
          <pre class={call.error ? "bad" : ""}>{call.output}</pre>
        </div>
      )}
    </div>
  );
}

function inputText(call: Call): string {
  const { command, content } = call.input;
  if (call.name === "Bash" && typeof command === "string") return command;
  if (call.name === "Write" && typeof content === "string") return `${call.input.file_path}\n\n${content}`;
  return JSON.stringify(call.input, null, 2);
}

const statusMark: Record<Status, string> = { added: "A", modified: "M", deleted: "D", untouched: "" };

function FileTree({
  run,
  changes,
  side,
  setSide,
  selected,
  onSelect,
}: {
  run: RunPayload;
  changes: Map<string, Status>;
  side: Side;
  setSide: (s: Side) => void;
  selected?: string;
  onSelect: (path: string) => void;
}) {
  // the after view keeps deleted files, so a removal shows where it happened
  const paths = (side === "after" ? [...changes.keys()] : Object.keys(run.before)).sort();
  return (
    <section class="pane tree">
      <h2>Files</h2>
      <div class="toggle" role="group">
        {(["before", "after"] as const).map((s) => (
          <button type="button" key={s} aria-pressed={side === s} onClick={() => setSide(s)}>
            {s}
          </button>
        ))}
      </div>
      <ul>
        {paths.map((p) => {
          const st = changes.get(p)!;
          const slash = p.lastIndexOf("/");
          return (
            <li key={p}>
              <button
                type="button"
                class={`file ${st}${selected === p ? " current" : ""}`}
                onClick={() => onSelect(p)}
              >
                <span class="st">{side === "after" ? statusMark[st] : ""}</span>
                {slash >= 0 && <span class="muted">{p.slice(0, slash + 1)}</span>}
                {p.slice(slash + 1)}
              </button>
            </li>
          );
        })}
      </ul>
    </section>
  );
}

interface Shown {
  title: string;
  doc?: string;
  original?: string;
  notes: string[];
}

function shown(run: RunPayload, model: Model, sel: Selection, side: Side, diff: boolean, shell: Set<string>): Shown {
  const notes: string[] = [];
  if (shell.has(sel.path)) {
    notes.push("A shell command changed this file too. The step view replays only Write and Edit calls.");
  }

  if (sel.at !== undefined) {
    const call = model.calls[sel.at]!;
    const now = stateAt(model, run.before, sel.at).get(sel.path);
    const prev = stateAt(model, run.before, sel.at - 1).get(sel.path);
    const title = `${sel.path} after ${call.name} at ${clock(call.at)}`;
    if (call.error) notes.push("This call failed, so the file stayed as it was.");
    if (now === undefined) return { title, notes: [...notes, "The replay has no text for this file."] };
    return { title, doc: now, original: diff ? (prev ?? "") : undefined, notes };
  }

  const f = (side === "after" ? run.after : run.before)[sel.path];
  const title = `${sel.path} (${side})`;
  if (!f) return { title, notes: [...notes, side === "after" ? "The run deleted this file." : "Not in this tree."] };
  if (f.skip) return { title, notes: [...notes, `A ${f.skip} file of ${f.size} bytes, not shown.`] };
  const base = run.before[sel.path];
  const original = diff && side === "after" && !base?.skip ? (base?.text ?? "") : undefined;
  return { title, doc: f.text ?? "", original, notes };
}

function Viewer({
  run,
  model,
  sel,
  side,
  diff,
  setDiff,
  shell,
}: {
  run: RunPayload;
  model: Model;
  sel?: Selection;
  side: Side;
  diff: boolean;
  setDiff: (d: boolean) => void;
  shell: Set<string>;
}) {
  const view = sel && shown(run, model, sel, side, diff, shell);
  return (
    <section class="pane viewer">
      <h2>{view ? view.title : "Viewer"}</h2>
      <div class="toggle" role="group">
        <button type="button" aria-pressed={!diff} onClick={() => setDiff(false)}>
          file
        </button>
        <button type="button" aria-pressed={diff} onClick={() => setDiff(true)}>
          diff
        </button>
      </div>
      {!view && <p class="muted">Pick a file, or a Write or Edit call in the timeline.</p>}
      {view?.notes.map((n) => (
        <p class="note" key={n}>
          {n}
        </p>
      ))}
      {view?.doc !== undefined && <Code path={sel!.path} doc={view.doc} original={view.original} />}
    </section>
  );
}

function Reply({ text }: { text: string }) {
  if (!text) return null;
  return (
    <section class="reply">
      <h2>Final reply</h2>
      <div class="md" dangerouslySetInnerHTML={{ __html: renderMarkdown(text) }} />
    </section>
  );
}
