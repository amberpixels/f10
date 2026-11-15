import { useEffect, useState } from "preact/hooks";

import { message, P } from "./api";
import { enableNotifications, notifyState } from "./notify";
import { onLink, route } from "./router";
import { caller, can, forgetToken, token } from "./session";
import { api, live, openAsks, start } from "./store";
import { ArtifactView } from "./views/Artifact";
import { Asks } from "./views/Asks";
import { Board } from "./views/Board";
import { Capture } from "./views/Capture";
import { Login } from "./views/Login";
import { Start } from "./views/Start";
import { Status } from "./views/Status";
import { TaskView } from "./views/Task";

type Tab = { path: string; label: string; show: boolean; badge?: number };

function tabs(): Tab[] {
  const c = caller.value;
  return [
    { path: "/", label: "Runs", show: can(c, P.status) },
    { path: "/asks", label: "Asks", show: can(c, P.listAsks), badge: openAsks.value?.length },
    { path: "/board", label: "Board", show: can(c, P.board) },
    { path: "/start", label: "Start", show: can(c, P.start) || can(c, P.drive) },
    { path: "/capture", label: "Capture", show: can(c, P.createTask) },
  ].filter((t) => t.show);
}

function Page() {
  const { path, q } = route.value;
  const get = (k: string) => q.get(k) ?? "";
  switch (path) {
    case "/asks":
      return <Asks />;
    case "/board":
      return <Board project={get("p")} />;
    case "/start":
      return <Start />;
    case "/capture":
      return <Capture />;
    case "/task":
      return <TaskView root={get("root")} task={get("t")} />;
    case "/artifact":
      return <ArtifactView root={get("root")} task={get("t")} kind={get("k")} name={get("n")} />;
    default:
      return <Status />;
  }
}

function Nav() {
  const here = route.value.path;
  return (
    <nav class="tabs">
      {tabs().map((t) => (
        <a key={t.path} href={t.path} class={here === t.path ? "on" : ""}>
          {t.label}
          {!!t.badge && <span class="badge">{t.badge}</span>}
        </a>
      ))}
    </nav>
  );
}

function Shell() {
  useEffect(() => start(), []);
  const c = caller.value;
  return (
    <div class="shell" onClick={onLink}>
      <header class="top">
        <a href="/" class="brand">
          f10
        </a>
        <span class={`live ${live.value}`} title={`events: ${live.value}`} />
        <span class="muted small who">{c?.local ? "local" : c?.token}</span>
        {notifyState.value === "default" && (
          <button class="link" onClick={() => void enableNotifications()}>
            Notify me
          </button>
        )}
        <button class="link" onClick={forgetToken}>
          Sign out
        </button>
      </header>
      <aside class="side">
        <Nav />
      </aside>
      <main class="main">
        <Page />
      </main>
      <footer class="bottom">
        <Nav />
      </footer>
    </div>
  );
}

/** App asks who the stored token is before anything else loads. */
export function App() {
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    if (!token.value || caller.value) return;
    setError("");
    api.daemon
      .hello({})
      .then((res) => {
        caller.value = res.caller ?? null;
      })
      .catch((e) => {
        // a refused token signs out through the interceptor; anything else keeps it
        setError(message(e));
      });
  }, [token.value, attempt]);

  if (!token.value) return <Login />;
  if (!caller.value) {
    return (
      <main class="login">
        <div class="card">
          <p class="muted">{error ? `The daemon did not answer: ${error}` : "Connecting…"}</p>
          {error && <button onClick={() => setAttempt(attempt + 1)}>Try again</button>}
        </div>
      </main>
    );
  }
  return <Shell />;
}
