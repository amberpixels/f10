import { useEffect, useState } from "preact/hooks";

import { ArtifactKind } from "../../../gen/f10/v1/artifacts_pb";
import { message } from "../api";
import { inlineImages } from "../demo";
import { renderMarkdown } from "../markdown";
import { href } from "../router";
import { api } from "../store";
import { Section } from "./common";

type Shown =
  | { kind: "markdown"; html: string }
  | { kind: "report"; html: string }
  | { kind: "image"; url: string }
  | { kind: "text"; text: string };

// what one report inlines, so a long report cannot exhaust a phone's tab
const maxInlined = 24 << 20;

export function dataURL(bytes: Uint8Array, type: string): string {
  let bin = "";
  for (let i = 0; i < bytes.length; i += 0x8000) {
    bin += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  }
  return `data:${type};base64,${btoa(bin)}`;
}

function kindOf(word: string): ArtifactKind {
  const k = ArtifactKind[word.toUpperCase() as keyof typeof ArtifactKind];
  return typeof k === "number" ? k : ArtifactKind.UNSPECIFIED;
}

export function ArtifactView(props: { root: string; task: string; kind: string; name: string }) {
  const { root, task, kind, name } = props;
  const [shown, setShown] = useState<Shown | null>(null);
  const [title, setTitle] = useState(name);
  const [error, setError] = useState("");

  useEffect(() => {
    const urls: string[] = [];
    let gone = false;
    const blob = (bytes: Uint8Array, type: string) => {
      const url = URL.createObjectURL(new Blob([bytes as BlobPart], { type }));
      urls.push(url);
      return url;
    };

    (async () => {
      try {
        const res = await api.artifacts.getArtifact({ project: root, task, kind: kindOf(kind), name });
        const type = res.artifact?.contentType ?? "";
        setTitle(res.artifact?.title || name);
        const text = () => new TextDecoder().decode(res.content);
        let out: Shown;
        if (type.startsWith("text/markdown")) {
          out = { kind: "markdown", html: renderMarkdown(text()) };
        } else if (type.startsWith("text/html")) {
          const base = name.includes("/") ? name.slice(0, name.lastIndexOf("/") + 1) : "";
          let inlined = 0;
          const html = await inlineImages(text(), async (src) => {
            if (inlined >= maxInlined) return null;
            try {
              const img = await api.artifacts.getArtifact({ project: root, task, kind: kindOf(kind), name: base + src });
              inlined += img.content.length;
              if (inlined > maxInlined) return null;
              // a data URL, since the sandboxed frame has no origin a blob URL would resolve in
              return dataURL(img.content, img.artifact?.contentType ?? "application/octet-stream");
            } catch {
              return null;
            }
          });
          out = { kind: "report", html };
        } else if (type.startsWith("image/")) {
          out = { kind: "image", url: blob(res.content, type) };
        } else {
          out = { kind: "text", text: text() };
        }
        if (!gone) setShown(out);
      } catch (e) {
        if (!gone) setError(message(e));
      }
    })();

    return () => {
      gone = true;
      urls.forEach((u) => URL.revokeObjectURL(u));
    };
  }, [root, task, kind, name]);

  return (
    <Section title={title} aside={<a href={href("/task", { root, t: task })}>{task}</a>}>
      {error && <p class="error">{error}</p>}
      {!shown && !error && <p class="muted">Loading…</p>}
      {shown?.kind === "markdown" && <article class="prose" dangerouslySetInnerHTML={{ __html: shown.html }} />}
      {shown?.kind === "report" && (
        // no scripts and no same-origin access: the report is a document to read
        <iframe class="report" sandbox="allow-popups allow-popups-to-escape-sandbox" srcdoc={shown.html} title={title} />
      )}
      {shown?.kind === "image" && <img class="shot" src={shown.url} alt={name} />}
      {shown?.kind === "text" && <pre class="prose">{shown.text}</pre>}
    </Section>
  );
}
