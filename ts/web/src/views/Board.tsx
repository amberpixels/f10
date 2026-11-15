import { useEffect, useState } from "preact/hooks";

import { BoardView, type Board as BoardMsg } from "../../../gen/f10/v1/board_pb";
import { message } from "../api";
import { ago } from "../format";
import { api, projects } from "../store";
import { ProjectPicker, Section } from "./common";

export function Board(props: { project: string }) {
  const [project, setProject] = useState(props.project || projects.value[0]?.id || "");
  const [view, setView] = useState(BoardView.STATE);
  const [board, setBoard] = useState<BoardMsg | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!project.trim()) return;
    let gone = false;
    setBusy(true);
    const t = setTimeout(async () => {
      try {
        const res = await api.board.getBoard({ project, view });
        if (!gone) {
          setBoard(res.board ?? null);
          setError("");
        }
      } catch (e) {
        if (!gone) setError(message(e));
      } finally {
        if (!gone) setBusy(false);
      }
    }, 300);
    return () => {
      gone = true;
      clearTimeout(t);
    };
  }, [project, view]);

  return (
    <Section
      title="Board"
      aside={
        <div class="toggle">
          <button class={view === BoardView.STATE ? "on" : ""} onClick={() => setView(BoardView.STATE)}>
            State
          </button>
          <button class={view === BoardView.CHANGES ? "on" : ""} onClick={() => setView(BoardView.CHANGES)}>
            Changes
          </button>
        </div>
      }
    >
      <div class="card form">
        <ProjectPicker projects={projects.value} value={project} onChange={setProject} />
      </div>
      {error && <p class="error">{error}</p>}
      {busy && !board && <p class="muted">Loading…</p>}
      {board && view === BoardView.STATE && (
        <div class="table">
          {board.rows.length === 0 && <p class="muted">No open tasks.</p>}
          {board.rows.map((r) => (
            <div key={r.key} class="card row-card">
              <div class="run-head">
                <strong>{r.task?.ref?.id}</strong>
                <span class="tag">{r.stage}</span>
                <span class="muted small">{ago(r.lastActivity)}</span>
              </div>
              <div class="run-title">{r.task?.ref?.title}</div>
              {r.where?.pr && (
                <a href={r.where.pr.url} target="_blank" rel="noopener noreferrer">
                  #{r.where.pr.number} {r.where.pr.checks && `· checks ${r.where.pr.checks}`}
                </a>
              )}
              {r.blockedBy.length > 0 && <p class="muted small">blocked by {r.blockedBy.join(", ")}</p>}
              {r.next?.text && <p class="muted small">Next: {r.next.text}</p>}
            </div>
          ))}
        </div>
      )}
      {board && view === BoardView.CHANGES && (
        <div class="table">
          {(board.feed?.rows.length ?? 0) === 0 && <p class="muted">Nothing changed in this window.</p>}
          {board.feed?.rows.map((r) => (
            <div key={r.key} class="card row-card">
              <div class="run-head">
                <strong>{r.task?.ref?.id ?? (r.pr ? `#${r.pr.number}` : r.key)}</strong>
                <span class="tag">{r.group}</span>
                <span class="muted small">{ago(r.at)}</span>
              </div>
              <div class="run-title">{r.task?.ref?.title ?? r.pr?.title}</div>
              {r.summary && <p class="small">{r.summary}</p>}
            </div>
          ))}
          {board.feed?.quiet && board.feed.quiet.count > 0 && (
            <p class="muted small">{board.feed.quiet.count} tasks had no change.</p>
          )}
        </div>
      )}
    </Section>
  );
}
