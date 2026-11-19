import { describe, expect, it } from "vitest";

import { redirect, route } from "./router";

describe("redirect", () => {
  it("moves an old address to its new one, keeping the query and adding no history entry", () => {
    history.pushState(null, "", "/asks?x=1");
    const depth = history.length;

    redirect("/inbox");

    expect(location.pathname).toBe("/inbox");
    expect(route.value.path).toBe("/inbox");
    expect(route.value.q.get("x")).toBe("1");
    expect(history.length).toBe(depth);
  });
});
