import {
  Code,
  ConnectError,
  createClient,
  type Client,
  type Interceptor,
  type Transport,
} from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import type { DescService } from "@bufbuild/protobuf";

import { InboxService } from "../../gen/agents/v1/asks_pb";
import { ArtifactService } from "../../gen/f10/v1/artifacts_pb";
import { BoardService } from "../../gen/f10/v1/board_pb";
import { DaemonService } from "../../gen/f10/v1/daemon_pb";
import { EventService } from "../../gen/f10/v1/events_pb";
import { ProjectService } from "../../gen/f10/v1/projects_pb";
import { RunService } from "../../gen/f10/v1/runs_pb";
import { TaskService } from "../../gen/f10/v1/tasks_pb";

/** proc is a method's Connect procedure, the name the server's scopes use. */
export function proc(service: DescService, method: string): string {
  const m = service.methods.find((x) => x.localName === method || x.name === method);
  if (!m) throw new Error(`${service.typeName} has no method ${method}`);
  return `/${service.typeName}/${m.name}`;
}

/** The procedures the screens gate on. */
export const P = {
  status: proc(RunService, "getRunStatus"),
  forward: proc(RunService, "forward"),
  start: proc(RunService, "start"),
  startBatch: proc(RunService, "startBatch"),
  createTask: proc(TaskService, "createTask"),
  listAsks: proc(InboxService, "listAsks"),
  answerAsk: proc(InboxService, "answerAsk"),
  artifacts: proc(ArtifactService, "listArtifacts"),
  board: proc(BoardService, "getBoard"),
  projects: proc(ProjectService, "listProjects"),
  events: proc(EventService, "watchEvents"),
};

/**
 * bearer adds the device token to every call, and reports a refused token,
 * so a revoked device returns to the login screen.
 */
export function bearer(token: () => string | null, refused: () => void): Interceptor {
  return (next) => async (req) => {
    const t = token();
    if (t) req.header.set("Authorization", `Bearer ${t}`);
    try {
      return await next(req);
    } catch (e) {
      if (ConnectError.from(e).code === Code.Unauthenticated) refused();
      throw e;
    }
  };
}

export type Clients = {
  daemon: Client<typeof DaemonService>;
  runs: Client<typeof RunService>;
  tasks: Client<typeof TaskService>;
  board: Client<typeof BoardService>;
  events: Client<typeof EventService>;
  artifacts: Client<typeof ArtifactService>;
  projects: Client<typeof ProjectService>;
  asks: Client<typeof InboxService>;
};

export function clientsOver(transport: Transport): Clients {
  return {
    daemon: createClient(DaemonService, transport),
    runs: createClient(RunService, transport),
    tasks: createClient(TaskService, transport),
    board: createClient(BoardService, transport),
    events: createClient(EventService, transport),
    artifacts: createClient(ArtifactService, transport),
    projects: createClient(ProjectService, transport),
    asks: createClient(InboxService, transport),
  };
}

/** clients talks to the daemon that served this page. */
export function clients(token: () => string | null, refused: () => void): Clients {
  return clientsOver(
    createConnectTransport({
      baseUrl: location.origin,
      interceptors: [bearer(token, refused)],
    }),
  );
}

/** message is an error as one line for the screen. */
export function message(e: unknown): string {
  const ce = ConnectError.from(e);
  return ce.code === Code.PermissionDenied ? `Not allowed: ${ce.rawMessage}` : ce.rawMessage;
}
