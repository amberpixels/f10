import type { Call, Model } from "./load";
import type { Files } from "./types";

export type Tree = Map<string, string>;

/** textTree is the files of a tree whose text the payload carries. */
export function textTree(files: Files): Tree {
  const t: Tree = new Map();
  for (const [path, f] of Object.entries(files)) {
    if (!f.skip) t.set(path, f.text ?? "");
  }
  return t;
}

const editTools = new Set(["Write", "Edit", "MultiEdit"]);

/** edits says whether a call changes a file of the repo when it succeeds. */
export function edits(call: Call): boolean {
  return editTools.has(call.name) && call.path !== undefined;
}

interface EditPair {
  old_string?: unknown;
  new_string?: unknown;
  replace_all?: unknown;
}

function applyEdit(text: string, e: EditPair): string | undefined {
  if (typeof e.old_string !== "string" || typeof e.new_string !== "string") return undefined;
  if (e.old_string === "" || !text.includes(e.old_string)) return undefined;
  return e.replace_all === true
    ? text.split(e.old_string).join(e.new_string)
    : text.replace(e.old_string, () => e.new_string as string);
}

/**
 * apply replays one call onto tree. A call whose result was an error, or
 * that has no result, changed nothing, because the tool refused or never ran.
 */
export function apply(tree: Tree, call: Call): void {
  if (!edits(call) || !call.done || call.error) return;
  const path = call.path!;
  const input = call.input;

  if (call.name === "Write") {
    if (typeof input.content === "string") tree.set(path, input.content);
    return;
  }

  const text = tree.get(path);
  if (text === undefined) return;
  const pairs = call.name === "MultiEdit" ? (Array.isArray(input.edits) ? input.edits : []) : [input];
  let next: string | undefined = text;
  for (const p of pairs as EditPair[]) {
    next = next === undefined ? undefined : applyEdit(next, p);
  }
  // MultiEdit is all or nothing, as the tool applies it
  if (next !== undefined) tree.set(path, next);
}

/** stateAt is the repo after call `upto` (a call index), -1 being before any. */
export function stateAt(model: Model, before: Files, upto: number): Tree {
  const tree = textTree(before);
  for (const call of model.calls) {
    if (call.index > upto) break;
    apply(tree, call);
  }
  return tree;
}

/**
 * shellChanged is the files whose replayed last state differs from after/:
 * a shell command changed them, which the replay cannot see.
 */
export function shellChanged(model: Model, before: Files, after: Files): Set<string> {
  const replayed = stateAt(model, before, model.calls.length);
  const out = new Set<string>();
  const paths = new Set([...Object.keys(before), ...Object.keys(after), ...replayed.keys()]);
  for (const p of paths) {
    const a = after[p];
    const b = before[p];
    if (a?.skip || b?.skip) continue;
    const want = a ? (a.text ?? "") : undefined;
    if (replayed.get(p) !== want) out.add(p);
  }
  return out;
}

export type Status = "added" | "modified" | "deleted" | "untouched";

/** status is how each file of either tree compares across the run. */
export function status(before: Files, after: Files): Map<string, Status> {
  const out = new Map<string, Status>();
  for (const p of new Set([...Object.keys(before), ...Object.keys(after)])) {
    const b = before[p];
    const a = after[p];
    if (!b) out.set(p, "added");
    else if (!a) out.set(p, "deleted");
    else if (b.size !== a.size || b.skip !== a.skip || (b.text ?? "") !== (a.text ?? "")) out.set(p, "modified");
    else out.set(p, "untouched");
  }
  return out;
}
