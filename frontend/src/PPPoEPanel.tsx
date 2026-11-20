import { useEffect, useState, type FormEvent } from "react";

type PPPoEStatus = {
  state:
    | "disconnected"
    | "connecting"
    | "connected"
    | "auth_failed"
    | "no_peer"
    | "reconnecting";
  interface: string;
  address: string;
  peer_address: string;
};
type Transaction = { transaction_id: string; expires_at: string };

const labels: Record<PPPoEStatus["state"], string> = {
  disconnected: "Disconnected",
  connecting: "Connecting…",
  connected: "Connected",
  auth_failed: "Authentication failed. Check the PPPoE login and password.",
  no_peer: "No PPPoE server answered on the WAN cable.",
  reconnecting: "Reconnecting…",
};

function object(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object";
}

async function request(path: string, body: unknown): Promise<Response> {
  return fetch(path, {
    method: "POST",
    credentials: "same-origin",
    cache: "no-store",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify(body),
  });
}

export default function PPPoEPanel() {
  const [authenticated, setAuthenticated] = useState(false);
  const [login, setLogin] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [status, setStatus] = useState<PPPoEStatus | null>(null);
  const [pending, setPending] = useState<Transaction | null>(null);
  const [remaining, setRemaining] = useState(0);
  const [busy, setBusy] = useState(false);
  const [feedback, setFeedback] = useState("");

  useEffect(() => {
    let active = true;
    void fetch("/api/auth/session", {
      credentials: "same-origin",
      cache: "no-store",
    })
      .then(async (response) => {
        const value: unknown = await response.json();
        if (active)
          setAuthenticated(
            response.ok &&
              object(value) &&
              value.authenticated === true &&
              value.must_change_password === false,
          );
      })
      .catch(() => undefined);
    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    if (!authenticated) return;
    let active = true;
    const refresh = () => {
      void fetch("/api/network/wan/pppoe", {
        credentials: "same-origin",
        cache: "no-store",
      })
        .then(async (response) => {
          const value: unknown = await response.json();
          if (
            active &&
            response.ok &&
            object(value) &&
            typeof value.state === "string" &&
            value.state in labels &&
            typeof value.address === "string" &&
            typeof value.peer_address === "string"
          ) {
            setStatus(value as PPPoEStatus);
          }
        })
        .catch(() => undefined);
    };
    refresh();
    const timer = window.setInterval(refresh, 3000);
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, [authenticated]);

  useEffect(() => {
    if (!pending) return;
    const tick = () => {
      const seconds = Math.max(
        0,
        Math.ceil((Date.parse(pending.expires_at) - Date.now()) / 1000),
      );
      setRemaining(seconds);
      if (seconds === 0) {
        setPending(null);
        setFeedback(
          "Confirmation expired. The previous WAN configuration should be restored.",
        );
      }
    };
    tick();
    const timer = window.setInterval(tick, 1000);
    return () => window.clearInterval(timer);
  }, [pending]);

  async function signIn(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setFeedback("");
    try {
      const response = await request("/api/auth/login", { password: login });
      const value: unknown = await response.json();
      if (!response.ok || !object(value) || value.must_change_password === true)
        throw new Error(
          "Sign in or change the initial administrator password first.",
        );
      setLogin("");
      setAuthenticated(true);
    } catch (error) {
      setFeedback(error instanceof Error ? error.message : "Sign-in failed.");
    } finally {
      setBusy(false);
    }
  }

  async function apply(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setFeedback("");
    try {
      const response = await request("/api/network/wan/pppoe", {
        username,
        password,
      });
      const value: unknown = await response.json();
      if (
        !response.ok ||
        !object(value) ||
        typeof value.transaction_id !== "string" ||
        typeof value.expires_at !== "string"
      )
        throw new Error("PPPoE could not be applied.");
      setPassword("");
      setPending({
        transaction_id: value.transaction_id,
        expires_at: value.expires_at,
      });
      setFeedback(
        "PPPoE is temporary. Confirm only after the session connects; otherwise restore WAN.",
      );
    } catch (error) {
      setFeedback(
        error instanceof Error ? error.message : "PPPoE could not be applied.",
      );
    } finally {
      setBusy(false);
    }
  }

  async function finish(confirm: boolean) {
    if (!pending) return;
    setBusy(true);
    setFeedback("");
    try {
      const path = `/api/network/wan/pppoe/${confirm ? "confirm" : "rollback"}`;
      const response = await request(path, {
        transaction_id: pending.transaction_id,
      });
      if (!response.ok)
        throw new Error(
          confirm
            ? "Could not confirm PPPoE. Automatic rollback remains armed."
            : "Could not restore WAN yet.",
        );
      setPending(null);
      setFeedback(
        confirm
          ? "PPPoE settings confirmed."
          : "Previous WAN configuration restored.",
      );
    } catch (error) {
      setFeedback(
        error instanceof Error ? error.message : "WAN transaction failed.",
      );
    } finally {
      setBusy(false);
    }
  }

  async function reconnect() {
    setBusy(true);
    setFeedback("");
    try {
      const response = await request("/api/network/wan/pppoe/reconnect", {});
      if (!response.ok) throw new Error("PPPoE reconnect failed.");
      setFeedback("Reconnect requested.");
    } catch (error) {
      setFeedback(
        error instanceof Error ? error.message : "PPPoE reconnect failed.",
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="static-wan-card" aria-labelledby="pppoe-title">
      <p className="eyebrow">WAN CONFIGURATION</p>
      <h2 id="pppoe-title">PPPoE</h2>
      <p className="port-description">
        Connect the WAN port using your provider's PPPoE account.
      </p>
      {!authenticated ? (
        <form
          className="static-wan-form"
          onSubmit={(event) => void signIn(event)}
        >
          <label className="static-wan-field">
            Administrator password
            <input
              type="password"
              autoComplete="current-password"
              value={login}
              onChange={(event) => setLogin(event.target.value)}
              required
            />
          </label>
          <div className="static-wan-actions">
            <button className="primary-button" disabled={busy}>
              Sign in
            </button>
          </div>
        </form>
      ) : (
        <>
          <p className="port-feedback" role="status">
            Session: {status ? labels[status.state] : "Reading status…"}
            {status?.state === "connected" ? ` (${status.address})` : ""}
          </p>
          <form
            className="static-wan-form"
            onSubmit={(event) => void apply(event)}
          >
            <div className="static-wan-fields">
              <label className="static-wan-field">
                PPPoE login
                <input
                  autoComplete="off"
                  value={username}
                  onChange={(event) => setUsername(event.target.value)}
                  disabled={busy || pending !== null}
                  required
                />
              </label>
              <label className="static-wan-field">
                PPPoE password
                <input
                  type="password"
                  autoComplete="new-password"
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                  disabled={busy || pending !== null}
                  required
                />
              </label>
            </div>
            <p className="static-wan-note">
              Settings affect <strong>enp1s0</strong>. A 120-second rollback
              timer restores the previous WAN settings unless you confirm a
              connected session.
            </p>
            {pending ? (
              <div className="static-wan-pending" aria-live="polite">
                <p>
                  Automatic rollback in{" "}
                  <strong>
                    {Math.floor(remaining / 60)}:
                    {String(remaining % 60).padStart(2, "0")}
                  </strong>
                </p>
                <div className="static-wan-actions">
                  <button
                    type="button"
                    className="primary-button"
                    disabled={busy || status?.state !== "connected"}
                    onClick={() => void finish(true)}
                  >
                    Confirm settings
                  </button>
                  <button
                    type="button"
                    className="danger-button"
                    disabled={busy}
                    onClick={() => void finish(false)}
                  >
                    Restore previous settings
                  </button>
                </div>
              </div>
            ) : (
              <div className="static-wan-actions">
                <button className="primary-button" disabled={busy}>
                  Apply temporarily
                </button>
              </div>
            )}
          </form>
          <button
            type="button"
            className="refresh-button"
            disabled={
              busy || pending !== null || status?.state === "disconnected"
            }
            onClick={() => void reconnect()}
          >
            Reconnect PPPoE
          </button>
        </>
      )}
      {feedback && (
        <p className="static-wan-feedback" role="status">
          {feedback}
        </p>
      )}
    </section>
  );
}
