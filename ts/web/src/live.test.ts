import { Code, ConnectError } from "@connectrpc/connect";
import { describe, expect, it, vi } from "vitest";

import { debounced, runLive, type LiveDeps } from "./live";
import type { Event } from "../../gen/f10/v1/events_pb";

const ev = (id: string) => ({ id, kind: "step_entered" }) as unknown as Event;

describe("runLive", () => {
  it("starts at the tail, then resumes from the last cursor after a drop", async () => {
    const ctl = new AbortController();
    const calls: [string, boolean][] = [];
    const seen: string[] = [];

    const deps: LiveDeps = {
      watch: (after, tail) => {
        calls.push([after, tail]);
        return (async function* () {
          if (calls.length === 1) {
            yield { event: ev("a"), cursor: "7" };
            throw new ConnectError("dropped", Code.Unavailable);
          }
          yield { event: ev("b"), cursor: "8" };
          ctl.abort();
        })();
      },
      onEvent: (e) => seen.push(e.id),
      onState: () => {},
      onRefused: () => {},
      sleep: async () => {},
    };

    await runLive(deps, ctl.signal);
    expect(calls).toEqual([
      ["", true],
      ["7", false],
    ]);
    expect(seen).toEqual(["a", "b"]);
  });

  it("stops and signs out on a refused token", async () => {
    const refused = vi.fn();
    const watch = vi.fn(() =>
      (async function* () {
        throw new ConnectError("revoked", Code.Unauthenticated);
      })(),
    );
    await runLive(
      { watch, onEvent: () => {}, onState: () => {}, onRefused: refused, sleep: async () => {} },
      new AbortController().signal,
    );
    expect(refused).toHaveBeenCalledOnce();
    expect(watch).toHaveBeenCalledOnce();
  });
});

describe("debounced", () => {
  it("calls once per key in the window", () => {
    vi.useFakeTimers();
    const fn = vi.fn();
    const d = debounced(1000, fn);
    d("runs");
    d("runs");
    d("asks");
    vi.advanceTimersByTime(1000);
    expect(fn.mock.calls.map((c) => c[0]).sort()).toEqual(["asks", "runs"]);
    d("runs");
    vi.advanceTimersByTime(1000);
    expect(fn).toHaveBeenCalledTimes(3);
    vi.useRealTimers();
  });
});
