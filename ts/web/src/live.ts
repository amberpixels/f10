import { Code, ConnectError } from "@connectrpc/connect";

import type { Event } from "../../gen/f10/v1/events_pb";

export type LiveState = "connecting" | "live" | "down";

export type Watch = (
  after: string,
  tail: boolean,
  signal: AbortSignal,
) => AsyncIterable<{ event?: Event; cursor: string }>;

export type LiveDeps = {
  watch: Watch;
  onEvent: (e: Event) => void;
  onState: (s: LiveState) => void;
  onRefused: () => void;
  // onReconnect runs when a stream opens after one dropped, because a stream
  // that had no cursor yet resumes from the tail and skips the gap
  onReconnect?: () => void;
  sleep?: (ms: number, signal: AbortSignal) => Promise<void>;
};

const maxBackoff = 30_000;

function sleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const t = setTimeout(resolve, ms);
    signal.addEventListener("abort", () => {
      clearTimeout(t);
      resolve();
    });
  });
}

/**
 * runLive follows the event stream until signal aborts. The first stream
 * starts at the tail, because current state comes from the list calls; a
 * reconnect resumes from the last cursor, or calls onReconnect so the lists
 * are read again when there was none yet.
 */
export async function runLive(deps: LiveDeps, signal: AbortSignal): Promise<void> {
  const wait = deps.sleep ?? sleep;
  let cursor = "";
  let backoff = 1000;
  let dropped = false;

  while (!signal.aborted) {
    deps.onState("connecting");
    try {
      const stream = deps.watch(cursor, cursor === "", signal);
      deps.onState("live");
      if (dropped) deps.onReconnect?.();
      for await (const res of stream) {
        backoff = 1000;
        cursor = res.cursor;
        if (res.event) deps.onEvent(res.event);
      }
    } catch (e) {
      if (signal.aborted) return;
      if (ConnectError.from(e).code === Code.Unauthenticated) {
        deps.onRefused();
        return;
      }
    }
    if (signal.aborted) return;
    dropped = true;
    deps.onState("down");
    await wait(backoff, signal);
    backoff = Math.min(backoff * 2, maxBackoff);
  }
}

/** debounced calls fn once per key at most every ms, with the last call's key. */
export function debounced(ms: number, fn: (key: string) => void): (key: string) => void {
  const timers = new Map<string, ReturnType<typeof setTimeout>>();
  return (key) => {
    if (timers.has(key)) return;
    timers.set(
      key,
      setTimeout(() => {
        timers.delete(key);
        fn(key);
      }, ms),
    );
  };
}
