import { signal } from "@preact/signals";

export type Route = { path: string; q: URLSearchParams };

function current(): Route {
  return { path: location.pathname, q: new URLSearchParams(location.search) };
}

export const route = signal<Route>(current());

addEventListener("popstate", () => {
  route.value = current();
});

/** href builds an app link with its query, skipping empty values. */
export function href(path: string, q: Record<string, string | undefined> = {}): string {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(q)) if (v) p.set(k, v);
  const s = p.toString();
  return s ? `${path}?${s}` : path;
}

export function navigate(to: string) {
  history.pushState(null, "", to);
  route.value = current();
  scrollTo(0, 0);
}

/** redirect replaces an old address with its new one, keeping the query. */
export function redirect(to: string) {
  history.replaceState(null, "", to + location.search);
  route.value = current();
}

/** onLink lets a plain <a> navigate inside the app. */
export function onLink(e: MouseEvent) {
  const a = (e.target as Element).closest("a");
  if (!a || a.target || e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
  const url = new URL(a.href);
  if (url.origin !== location.origin) return;
  e.preventDefault();
  navigate(url.pathname + url.search);
}
