import { signal } from "@preact/signals";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";

import type { Ask } from "../../gen/agents/v1/asks_pb";
import type { KnownProject } from "../../gen/f10/v1/projects_pb";
import type { RunStatus } from "../../gen/f10/v1/runs_pb";
import type { Event } from "../../gen/f10/v1/events_pb";
import { clients, message, P, type Clients } from "./api";
import { debounced, runLive, type LiveState } from "./live";
import { notifyAsk } from "./notify";
import { caller, can, forgetToken, token } from "./session";

export const api: Clients = clients(() => token.value, forgetToken);

export const runs = signal<RunStatus[] | null>(null);
export const runsError = signal("");
export const openAsks = signal<Ask[] | null>(null);
export const answeredAsks = signal<Ask[]>([]);
export const asksError = signal("");
export const projects = signal<KnownProject[]>([]);
export const live = signal<LiveState>("connecting");

export async function refreshRuns() {
  if (!can(caller.value, P.status)) return;
  try {
    runs.value = (await api.runs.getRunStatus({ all: true })).runs;
    runsError.value = "";
  } catch (e) {
    runsError.value = message(e);
  }
}

export async function refreshAsks() {
  if (!can(caller.value, P.listAsks)) return;
  try {
    const since = timestampFromDate(new Date(Date.now() - 24 * 3600 * 1000));
    const res = await api.asks.listAsks({ answeredSince: since });
    openAsks.value = res.open;
    answeredAsks.value = res.answered;
    asksError.value = "";
  } catch (e) {
    asksError.value = message(e);
  }
}

export async function refreshProjects() {
  if (!can(caller.value, P.projects)) return;
  try {
    projects.value = (await api.projects.listProjects({})).projects;
  } catch {
    // the pickers fall back to a typed project id
  }
}

const later = debounced(1000, (key) => {
  if (key === "asks") void refreshAsks();
  else void refreshRuns();
});

/** onEvent refetches what an event changed, rather than folding it in. */
export function onEvent(e: Event) {
  if (e.kind.startsWith("ask_")) {
    later("asks");
    if (e.payload.case === "askRaised" && e.payload.value.ask) void notifyAsk(e.payload.value.ask);
  }
  later("runs");
}

/** start loads every list and follows the event stream until stop. */
export function start(): () => void {
  const ctl = new AbortController();
  void refreshRuns();
  void refreshAsks();
  void refreshProjects();
  if (can(caller.value, P.events)) {
    void runLive(
      {
        watch: (after, tail, signal) => api.events.watchEvents({ after, tail }, { signal }),
        onEvent,
        onState: (s) => {
          live.value = s;
        },
        onRefused: forgetToken,
      },
      ctl.signal,
    );
  }
  return () => ctl.abort();
}
