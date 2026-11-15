import { describe, expect, it } from "vitest";

import { inlineImages, isRelative, normalize } from "./demo";
import { renderMarkdown } from "./markdown";
import { projectLabel } from "./format";

describe("renderMarkdown", () => {
  it("escapes raw HTML", () => {
    const html = renderMarkdown("# Plan\n\n<script>alert(1)</script><img src=x onerror=alert(1)>");
    expect(html).toContain("<h1>Plan</h1>");
    expect(html).not.toContain("<script>");
    expect(html).not.toContain("<img");
  });

  it("opens links in a new tab", () => {
    expect(renderMarkdown("[pr](https://example.com/1)")).toContain('target="_blank"');
  });
});

describe("inlineImages", () => {
  it("rewrites relative sources only, and drops scripts", async () => {
    const asked: string[] = [];
    const out = await inlineImages(
      '<img src="shots/a.png"><img src="./b.png?x=1"><img src="https://x.test/c.png"><img src="data:image/png;base64,AA=="><script>1</script>',
      async (n) => {
        asked.push(n);
        return `data:image/png;base64,${n}`;
      },
    );
    expect(asked.sort()).toEqual(["b.png", "shots/a.png"]);
    expect(out).toContain('src="https://x.test/c.png"');
    expect(out).not.toContain("<script>");
  });

  it("reads a few images at a time and drops a source it cannot inline", async () => {
    let active = 0;
    let peak = 0;
    const html = Array.from({ length: 10 }, (_, i) => `<img src="s${i}.png">`).join("");
    const out = await inlineImages(
      html,
      async (n) => {
        active++;
        peak = Math.max(peak, active);
        await new Promise((r) => setTimeout(r, 5));
        active--;
        return n === "s3.png" ? null : `data:image/png;base64,${n}`;
      },
      4,
    );
    expect(peak).toBe(4);
    expect(out).not.toContain('src="s3.png"');
    expect(out).toContain('alt="image not shown"');
  });

  it("names sources inside the demo directory", () => {
    expect(normalize("../../etc/passwd")).toBe("etc/passwd");
    expect(normalize("shots/./a%20b.png")).toBe("shots/a b.png");
    expect(isRelative("/abs.png")).toBe(false);
    expect(isRelative("blob:x")).toBe(false);
    expect(isRelative("a.png")).toBe(true);
  });
});

describe("projectLabel", () => {
  it("names a worktree by its main checkout", () => {
    expect(projectLabel("/code/f10.FT-536-build-a-client")).toBe("f10");
    expect(projectLabel("/code/widgets")).toBe("widgets");
  });
});
