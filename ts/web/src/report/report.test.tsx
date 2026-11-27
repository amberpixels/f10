import { render } from "preact";
import { describe, expect, it } from "vitest";

// the Go tests write this payload from the canned run folder beside it, so
// both sides of the page read one fixture
import golden from "../../../../cli/cmd/f10eval/testdata/runfolder/payload.json";
import { load } from "./load";
import { shellChanged, stateAt, status } from "./replay";
import { RunPage } from "./RunPage";
import { SetPage } from "./SetPage";
import type { RunPayload } from "./types";

const run = golden as unknown as RunPayload;
const model = load(run);
const call = (name: string, path?: string) => model.calls.find((c) => c.name === name && c.path === path)!;

describe("load", () => {
  it("splits turns at each result and sections at each skill", () => {
    expect(model.cwd).toBe("/repo");
    expect(model.turns).toHaveLength(2);
    expect(model.turns[0]!.prompt).toBe("/f10:plan EV-1 && /f10:ship EV-1 --driven");
    expect(model.turns.map((t) => t.sections.map((s) => s.skill))).toEqual([["/f10:plan"], ["/f10:ship"]]);
    expect(model.turns[0]!.end).toMatchObject({ cost: 0.5, durationMs: 60000, result: "Plan saved." });
    expect(model.reply).toContain("Shipped EV-1.");
  });

  it("pairs each call with its result, path, time and running cost", () => {
    const write = call("Write", "main.go");
    expect(write).toMatchObject({ done: true, error: false, at: 12000, cost: 0.512, est: true, subject: "main.go" });
    expect(write.output).toContain("has been updated");

    const failed = model.calls.find((c) => c.name === "Edit" && c.error)!;
    expect(failed.output).toContain("String to replace not found");

    const read = call("Read", "go.mod");
    expect(read.sub).toBe(true);
    expect(read.output).toBe("module greet\n\ngo 1.26\n");

    expect(model.calls.find((c) => c.name === "Write" && c.outside)?.subject).toBe("/tmp/scratch.txt");
    // the first turn's exact cost replaces the estimate
    expect(call("Skill").cost).toBe(0.006);
    expect(model.turns[1]!.sections[0]!.calls[0]!.cost).toBe(0.506);
    expect(model.calls.filter((c) => c.plumbing).map((c) => c.id)).toEqual(["t1", "t3"]);
  });
});

describe("replay", () => {
  it("shows main.go as each step left it", () => {
    const write = call("Write", "main.go");
    const afterWrite = stateAt(model, run.before, write.index).get("main.go")!;
    expect(afterWrite).toContain(`shout := flag.Bool("shout"`);
    expect(afterWrite).not.toContain(`"strings"`);
    expect(stateAt(model, run.before, write.index - 1).get("main.go")).toBe(run.before["main.go"]!.text);

    const edit = model.calls.find((c) => c.name === "Edit" && !c.error)!;
    expect(stateAt(model, run.before, edit.index).get("main.go")).toBe(run.after["main.go"]!.text);
  });

  it("skips a failed edit and leaves the shell's changes to after/", () => {
    const last = stateAt(model, run.before, model.calls.length);
    expect(last.get("main.go")).toBe(run.after["main.go"]!.text);
    expect(last.has("logo.png")).toBe(false);
    expect([...shellChanged(model, run.before, run.after)]).toEqual(["NOTES.md"]);
  });

  it("marks files added, modified and untouched", () => {
    expect(Object.fromEntries(status(run.before, run.after))).toEqual({
      "go.mod": "untouched",
      "logo.png": "untouched",
      "main.go": "modified",
      "NOTES.md": "added",
      "docs/EV-1.md": "added",
    });
  });
});

function mount(node: preact.ComponentChild) {
  const root = document.createElement("div");
  document.body.append(root);
  render(node, root);
  return root;
}

describe("RunPage", () => {
  it("renders the header, checks, panes and reply, and opens a Write at its step", async () => {
    const root = mount(<RunPage run={run} />);
    expect(root.querySelector("h1")?.textContent).toContain("feature-small");
    expect(root.querySelector(".checks")?.textContent).toContain("the reply names --shout");
    expect(root.querySelector(".inputs")?.textContent).toContain("never shown to the agent");
    expect(root.querySelectorAll(".panes .pane")).toHaveLength(3);
    expect(root.querySelector(".reply")?.textContent).toContain("prints the greeting in capitals");
    expect(root.querySelector(".facts .plumbing")?.textContent).toBe("19% of time · 2/9 calls · $0.01");
    expect([...root.querySelectorAll(".call.plumbing .name")].map((n) => n.textContent)).toEqual(["Skill", "Skill"]);

    const row = [...root.querySelectorAll<HTMLButtonElement>(".call .row")].find(
      (b) => b.textContent?.includes("Write") && b.textContent.includes("main.go"),
    )!;
    row.click();
    await new Promise((r) => setTimeout(r));
    expect(root.querySelector(".viewer h2")?.textContent).toBe("main.go after Write at 0:12");
    expect(root.querySelector(".call.current .io")?.textContent).toContain("/repo/main.go");
    expect(root.querySelector(".viewer .cm-editor")?.textContent).toContain("shout");
  });
});

describe("SetPage", () => {
  it("links runs that have a page and lists the rest", () => {
    const root = mount(
      <SetPage
        set={{
          name: "x",
          runs: [
            { file: "a.json", report: run.report, page: "a/index.html" },
            { file: "b.json", report: { ...run.report, scenario: "old", pass: true, checks: null } },
          ],
        }}
      />,
    );
    const links = [...root.querySelectorAll("a")].map((a) => a.getAttribute("href"));
    expect(links).toEqual(["a/index.html"]);
    expect(root.querySelectorAll("tbody tr")).toHaveLength(2);
    expect(root.querySelector(".head p")?.textContent).toBe("1/2 passed");
    expect(root.querySelectorAll("tr.arm")).toHaveLength(0);
  });

  it("groups an arms set by arm, the plainest first", () => {
    const r = (arm: string, file: string) => ({ file, report: { ...run.report, arm, isolation: "user plugins loaded: x" } });
    const root = mount(<SetPage set={{ name: "x", runs: [r("f10", "c.json"), r("raw", "a.json"), r("native", "b.json")] }} />);
    expect([...root.querySelectorAll("tr.arm")].map((t) => t.textContent)).toEqual(["raw", "native", "f10"]);
    expect(root.querySelectorAll(".head .note")).toHaveLength(1);
  });
});
