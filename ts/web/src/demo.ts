/**
 * inlineImages rewrites each relative image source in a demo report to the
 * URL resolve gives for it, so the report renders from memory: a frame
 * cannot send the device token, so it cannot load the files itself.
 * Absolute, data and blob sources stay as they are; a source resolve gives
 * no URL for loses it, so the frame does not try to load it.
 */
export async function inlineImages(
  html: string,
  resolve: (name: string) => Promise<string | null>,
  parallel = 4,
): Promise<string> {
  const doc = new DOMParser().parseFromString(html, "text/html");
  const imgs = Array.from(doc.querySelectorAll("img[src], source[srcset]"));
  // a few at a time, so a long report does not fire every read at once
  let next = 0;
  const worker = async () => {
    while (next < imgs.length) {
      const el = imgs[next++];
      const attr = el.tagName === "IMG" ? "src" : "srcset";
      const src = el.getAttribute(attr) ?? "";
      if (!isRelative(src)) continue;
      const url = await resolve(normalize(src));
      if (url) {
        el.setAttribute(attr, url);
      } else {
        el.removeAttribute(attr);
        el.setAttribute("alt", "image not shown");
      }
    }
  };
  await Promise.all(Array.from({ length: Math.max(1, parallel) }, worker));
  doc.querySelectorAll("script").forEach((s) => s.remove());
  return "<!doctype html>" + doc.documentElement.outerHTML;
}

export function isRelative(src: string): boolean {
  return src !== "" && !/^([a-z][a-z0-9+.-]*:|\/\/|\/|#)/i.test(src);
}

/** normalize is a relative source as a name inside the demo directory. */
export function normalize(src: string): string {
  const out: string[] = [];
  for (const part of decodeURIComponent(src.split(/[?#]/)[0]).split("/")) {
    if (part === "" || part === ".") continue;
    if (part === "..") out.pop();
    else out.push(part);
  }
  return out.join("/");
}
