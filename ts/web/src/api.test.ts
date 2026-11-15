import { Code, ConnectError } from "@connectrpc/connect";
import { describe, expect, it, vi } from "vitest";

import { bearer, P, proc } from "./api";
import { RunService } from "../../gen/f10/v1/runs_pb";

function call(interceptor: ReturnType<typeof bearer>, fail?: Error) {
  const header = new Headers();
  const next = vi.fn(async () => {
    if (fail) throw fail;
    return {} as never;
  });
  return { header, run: () => interceptor(next as never)({ header } as never), next };
}

describe("bearer", () => {
  it("adds the token to every call", async () => {
    const c = call(bearer(() => "f10_abc", () => {}));
    await c.run();
    expect(c.header.get("Authorization")).toBe("Bearer f10_abc");
  });

  it("signs out on a refused token, and only then", async () => {
    const refused = vi.fn();
    await expect(call(bearer(() => "x", refused), new ConnectError("no", Code.Unauthenticated)).run()).rejects.toThrow();
    expect(refused).toHaveBeenCalledOnce();

    await expect(call(bearer(() => "x", refused), new ConnectError("no", Code.PermissionDenied)).run()).rejects.toThrow();
    expect(refused).toHaveBeenCalledOnce();
  });
});

describe("proc", () => {
  it("names a procedure the way the server's scope table does", () => {
    expect(proc(RunService, "forward")).toBe("/f10.v1.RunService/Forward");
    expect(P.answerAsk).toBe("/agents.v1.AskService/AnswerAsk");
  });
});
