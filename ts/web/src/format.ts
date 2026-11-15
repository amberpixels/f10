import { timestampDate, type Timestamp } from "@bufbuild/protobuf/wkt";

import { PhaseStatus } from "../../gen/f10/v1/runs_pb";

export function ago(ts: Timestamp | undefined, now = Date.now()): string {
  if (!ts) return "";
  const s = Math.max(0, Math.round((now - timestampDate(ts).getTime()) / 1000));
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

/** phaseWord is a phase status as the status command prints it. */
export function phaseWord(p: PhaseStatus): string {
  return PhaseStatus[p]?.toLowerCase() ?? "unknown";
}

/**
 * projectLabel names the project a checkout belongs to: f10 names a task's
 * worktree `<main>.<task>-<slug>`, so the part before the first dot is the
 * main checkout's directory.
 */
export function projectLabel(root: string): string {
  const base = root.split("/").filter(Boolean).pop() ?? root;
  return base.split(".")[0] || base;
}
