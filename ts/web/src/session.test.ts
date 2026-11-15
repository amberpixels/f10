import { create } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";

import { CallerSchema } from "../../gen/f10/v1/daemon_pb";
import { P } from "./api";
import { can } from "./session";

describe("can", () => {
  it("follows the procedures the daemon listed for the token", () => {
    const reader = create(CallerSchema, { token: "phone", procedures: [P.status, P.listAsks] });
    expect(can(reader, P.listAsks)).toBe(true);
    expect(can(reader, P.answerAsk)).toBe(false);
    expect(can(null, P.status)).toBe(false);
    expect(can(create(CallerSchema, { local: true }), P.answerAsk)).toBe(true);
  });
});
