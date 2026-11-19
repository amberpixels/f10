import { create } from "@bufbuild/protobuf";
import { render } from "preact";
import { describe, expect, it, vi } from "vitest";

import { AskSchema, QuestionSchema } from "../../../gen/agents/v1/asks_pb";
import { RunSchema, RunStatusSchema } from "../../../gen/f10/v1/runs_pb";
import { AskForm, initialAnswers } from "./Inbox";
import { startReply, taskList } from "./Start";
import { groups } from "./Status";

const ask = create(AskSchema, {
  id: "k1",
  agent: "claude-ft-1",
  labels: { task: "FT-1" },
  questions: [
    create(QuestionSchema, { n: 1, text: "Reuse the plan?", options: ["reuse", "re-plan"] }),
    create(QuestionSchema, { n: 2, text: "What did you see?", options: [] }),
  ],
});

function mount(node: preact.ComponentChild) {
  const root = document.createElement("div");
  document.body.append(root);
  render(node, root);
  return root;
}

describe("AskForm", () => {
  it("preselects each default and sends answers by number", async () => {
    expect(initialAnswers(ask)).toEqual({ 1: "reuse", 2: "" });

    const send = vi.fn(async () => true);
    const root = mount(<AskForm ask={ask} canAnswer send={send} />);

    const checked = root.querySelector<HTMLInputElement>("input[type=radio]:checked");
    expect(checked?.value).toBe("reuse");

    const text = root.querySelector("textarea")!;
    text.value = "a red build";
    text.dispatchEvent(new Event("input", { bubbles: true }));

    root.querySelector<HTMLInputElement>("input[value=re-plan]")!.click();
    await new Promise((r) => setTimeout(r));

    root.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await new Promise((r) => setTimeout(r));

    expect(send).toHaveBeenCalledWith("k1", [
      { n: 1, value: "re-plan" },
      { n: 2, value: "a red build" },
    ]);
  });

  it("offers no Answer button to a token that cannot answer", () => {
    const root = mount(<AskForm ask={ask} canAnswer={false} send={vi.fn()} />);
    expect(root.querySelector("button")).toBeNull();
    expect(root.textContent).toContain("cannot answer");
  });
});

describe("groups", () => {
  it("puts a run that asks first in its project", () => {
    const s = (task: string, root: string, ask = "") =>
      create(RunStatusSchema, { run: create(RunSchema, { task, root, ask, session: task }) });
    const out = groups([s("FT-1", "/c/f10.FT-1-x"), s("FT-2", "/c/f10.FT-2-y", "1. go? [yes | no]"), s("R-1", "/c/r3")]);
    expect(out.map(([k, v]) => [k, v.map((x) => x.run?.task)])).toEqual([
      ["f10", ["FT-2", "FT-1"]],
      ["r3", ["R-1"]],
    ]);
  });
});

describe("taskList", () => {
  it("reads ids and a chain", () => {
    expect(taskList("FT-1, FT-2")).toEqual({ ids: ["FT-1", "FT-2"], chain: false });
    expect(taskList("FT-1 -- FT-2")).toEqual({ ids: ["FT-1", "FT-2"], chain: true });
  });
});

describe("startReply", () => {
  it("tells a joined drive from a started one and a discussion", () => {
    const r = { drive: 4, attached: true, task: "", branch: "" };
    expect(startReply(r, "FT-1")).toBe("Joined drive 4");
    expect(startReply({ ...r, attached: false }, "FT-1 FT-2")).toBe("Drive 4 runs FT-1 FT-2");
    expect(startReply({ drive: 0, attached: false, task: "FT-1", branch: "FT-1/x" }, "FT-1")).toBe(
      "Started FT-1 on FT-1/x",
    );
  });
});
