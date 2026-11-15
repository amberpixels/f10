import { useState } from "preact/hooks";

import { message } from "../api";
import { keepToken } from "../session";
import { api } from "../store";

export function Login() {
  const [value, setValue] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e: Event) {
    e.preventDefault();
    const t = value.trim();
    if (!t) return;
    setBusy(true);
    setError("");
    try {
      // the token is kept only once the daemon accepts it
      const res = await api.daemon.hello({}, { headers: { Authorization: `Bearer ${t}` } });
      if (!res.caller) throw new Error("the daemon did not say who this token is");
      keepToken(t, res.caller);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <main class="login">
      <form onSubmit={submit} class="card">
        <h1>f10</h1>
        <p class="muted">
          Paste a device token. Make one on the machine with{" "}
          <code>f10 remote token add &lt;name&gt; --scope read,answer</code>.
        </p>
        <label>
          Device token
          <input
            type="password"
            autocomplete="current-password"
            value={value}
            onInput={(e) => setValue((e.target as HTMLInputElement).value)}
            placeholder="f10_…"
            required
          />
        </label>
        {error && <p class="error">{error}</p>}
        <button type="submit" disabled={busy}>
          {busy ? "Checking…" : "Sign in"}
        </button>
      </form>
    </main>
  );
}
