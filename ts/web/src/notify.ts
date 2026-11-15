import { signal } from "@preact/signals";

import type { Ask } from "../../gen/agents/v1/asks_pb";

function permission(): NotificationPermission | "unsupported" {
  return typeof Notification === "undefined" ? "unsupported" : Notification.permission;
}

export const notifyState = signal(permission());

/** enableNotifications asks once; a browser only allows it from a tap. */
export async function enableNotifications() {
  if (typeof Notification === "undefined") return;
  notifyState.value = await Notification.requestPermission();
}

/**
 * notifyAsk shows a new ask through the service worker, so it also shows
 * from a background tab and opens the asks screen when tapped.
 */
export async function notifyAsk(ask: Ask) {
  if (permission() !== "granted") return;
  const title = ask.labels.task ? `${ask.labels.task} asks` : "An agent asks";
  const opts: NotificationOptions = {
    body: ask.questions.map((q) => q.text).join("\n") || ask.text,
    tag: `ask-${ask.id}`,
    data: { url: "/asks" },
  };
  const reg = "serviceWorker" in navigator ? await navigator.serviceWorker.getRegistration() : undefined;
  if (reg) await reg.showNotification(title, opts);
  else new Notification(title, opts);
}
