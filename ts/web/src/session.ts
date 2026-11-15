import { signal } from "@preact/signals";

import type { Caller } from "../../gen/f10/v1/daemon_pb";

const KEY = "f10.token";

// Storage can throw in a private window; the page still works for this visit.
function read(): string | null {
  try {
    return localStorage.getItem(KEY);
  } catch {
    return null;
  }
}

function write(value: string | null) {
  try {
    if (value === null) localStorage.removeItem(KEY);
    else localStorage.setItem(KEY, value);
  } catch {
    // kept in memory only
  }
}

export const token = signal<string | null>(read());
export const caller = signal<Caller | null>(null);

export function keepToken(t: string, c: Caller) {
  write(t);
  token.value = t;
  caller.value = c;
}

export function forgetToken() {
  write(null);
  token.value = null;
  caller.value = null;
}

/**
 * can says whether the caller may call procedure. It hides controls only:
 * the server still decides, and its refusal is shown when it comes.
 */
export function can(c: Caller | null, procedure: string): boolean {
  return !!c && (c.local || c.procedures.includes(procedure));
}
