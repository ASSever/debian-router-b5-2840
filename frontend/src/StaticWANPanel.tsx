import { useEffect, useState, type FormEvent } from "react";
import WANLinePanel from "./WANLinePanel";
import LANPanel from "./LANPanel";

const REQUEST_TIMEOUT_MS = 25_000;

type SessionState = "checking" | "anonymous" | "authenticated";
type DHCPDefaults = {
  addresses: string[];
  gateway: string;
  dns: string[];
};
type StaticWANTransaction = {
  transaction_id: string;
  expires_at: string;
};

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

function isTransaction(value: unknown): value is StaticWANTransaction {
  return (
    isObject(value) &&
    "transaction_id" in value &&
    typeof value.transaction_id === "string" &&
    /^[a-f0-9]{32}$/.test(value.transaction_id) &&
    "expires_at" in value &&
    typeof value.expires_at === "string" &&
    Number.isFinite(Date.parse(value.expires_at))
  );
}

async function readJSON(response: Response): Promise<unknown> {
  try {
    return await response.json();
  } catch {
    return null;
  }
}

function errorMessage(value: unknown, fallback: string): string {
  if (!isObject(value) || typeof value.error !== "string") {
    return fallback;
  }
  const messages: Record<string, string> = {
    invalid_credentials: "The administrator password is incorrect.",
    invalid_new_password:
      "Use a different password that is at least 12 characters long.",
    invalid_static_address: "Enter a usable IPv4 address and prefix.",
    invalid_static_gateway:
      "The gateway must be a usable address in the WAN subnet.",
    invalid_static_dns: "Enter one to four unique IPv4 DNS servers.",
    static_wan_apply_failed:
      "WAN settings could not be applied. Check that enp1s0 has an active link and retry.",
    link_down: "The WAN port has no active link.",
    password_change_required: "Change the initial password before continuing.",
    unauthorized: "Sign in again to continue.",
  };
  return messages[value.error] ?? fallback;
}

async function postJSON(path: string, body: unknown): Promise<Response> {
  const controller = new AbortController();
  const timeout = window.setTimeout(
    () => controller.abort(),
    REQUEST_TIMEOUT_MS,
  );
  try {
    return await fetch(path, {
      method: "POST",
      cache: "no-store",
      credentials: "same-origin",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      body: JSON.stringify(body),
      signal: controller.signal,
    });
  } finally {
    window.clearTimeout(timeout);
  }
}

export default function StaticWANPanel() {
  const [session, setSession] = useState<SessionState>("checking");
  const [mustChangePassword, setMustChangePassword] = useState(false);
  const [loginPassword, setLoginPassword] = useState("");
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [address, setAddress] = useState("");
  const [gateway, setGateway] = useState("");
  const [dns, setDNS] = useState("");
  const [formDirty, setFormDirty] = useState(false);
  const [pending, setPending] = useState<StaticWANTransaction | null>(null);
  const [secondsRemaining, setSecondsRemaining] = useState(0);
  const [isBusy, setIsBusy] = useState(false);
  const [feedback, setFeedback] = useState("");
  const [feedbackIsError, setFeedbackIsError] = useState(false);

  useEffect(() => {
    let active = true;
    void fetch("/api/auth/session", {
      method: "GET",
      cache: "no-store",
      credentials: "same-origin",
      headers: { Accept: "application/json" },
    })
      .then(async (response) => {
        const value: unknown = await readJSON(response);
        if (!active) return;
        if (
          response.ok &&
          isObject(value) &&
          value.authenticated === true &&
          typeof value.must_change_password === "boolean"
        ) {
          setSession("authenticated");
          setMustChangePassword(value.must_change_password);
        } else {
          setSession("anonymous");
        }
      })
      .catch(() => {
        if (active) setSession("anonymous");
      });
    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    if (session !== "authenticated" || mustChangePassword) return;
    let active = true;
    void fetch("/api/network/dhcp?interface=enp1s0", {
      method: "GET",
      cache: "no-store",
      headers: { Accept: "application/json" },
    })
      .then(async (response) => {
        const value: unknown = await readJSON(response);
        if (
          !active ||
          !response.ok ||
          !isObject(value) ||
          value.interface !== "enp1s0" ||
          !Array.isArray(value.addresses) ||
          !value.addresses.every((item) => typeof item === "string") ||
          typeof value.gateway !== "string" ||
          !Array.isArray(value.dns) ||
          !value.dns.every((item) => typeof item === "string")
        ) {
          return;
        }
        const defaults: DHCPDefaults = {
          addresses: value.addresses,
          gateway: value.gateway,
          dns: value.dns,
        };
        if (formDirty || defaults.addresses.length === 0) return;
        setAddress(defaults.addresses[0]);
        setGateway(defaults.gateway);
        setDNS(defaults.dns.join(", "));
      })
      .catch(() => undefined);
    return () => {
      active = false;
    };
  }, [formDirty, mustChangePassword, session]);

  useEffect(() => {
    if (pending === null) return;
    const updateCountdown = () => {
      const remaining = Math.max(
        0,
        Math.ceil((Date.parse(pending.expires_at) - Date.now()) / 1_000),
      );
      setSecondsRemaining(remaining);
      if (remaining === 0) {
        setPending(null);
        setFeedback(
          "The confirmation window ended. The previous WAN settings should now be restored.",
        );
        setFeedbackIsError(false);
      }
    };
    updateCountdown();
    const interval = window.setInterval(updateCountdown, 1_000);
    return () => window.clearInterval(interval);
  }, [pending]);

  async function handleLogin(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setIsBusy(true);
    setFeedback("");
    try {
      const response = await postJSON("/api/auth/login", {
        password: loginPassword,
      });
      const value: unknown = await readJSON(response);
      if (!response.ok || !isObject(value)) {
        throw new Error(errorMessage(value, "Unable to sign in."));
      }
      setSession("authenticated");
      setMustChangePassword(value.must_change_password === true);
      setLoginPassword("");
      setFeedback("");
      setFeedbackIsError(false);
    } catch (error) {
      setFeedback(
        error instanceof Error ? error.message : "Unable to sign in.",
      );
      setFeedbackIsError(true);
    } finally {
      setIsBusy(false);
    }
  }

  async function handlePasswordChange(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setIsBusy(true);
    setFeedback("");
    try {
      const response = await fetch("/api/auth/password", {
        method: "PUT",
        cache: "no-store",
        credentials: "same-origin",
        headers: {
          Accept: "application/json",
          "Content-Type": "application/json",
        },
        body: JSON.stringify({
          current_password: currentPassword,
          new_password: newPassword,
        }),
      });
      const value: unknown =
        response.status === 204 ? null : await readJSON(response);
      if (!response.ok) {
        throw new Error(
          errorMessage(value, "Unable to change the administrator password."),
        );
      }
      setSession("anonymous");
      setMustChangePassword(false);
      setCurrentPassword("");
      setNewPassword("");
      setFeedback("Password updated. Sign in again with the new password.");
      setFeedbackIsError(false);
    } catch (error) {
      setFeedback(
        error instanceof Error
          ? error.message
          : "Unable to change the administrator password.",
      );
      setFeedbackIsError(true);
    } finally {
      setIsBusy(false);
    }
  }

  async function handleApply(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setIsBusy(true);
    setFeedback("");
    try {
      const response = await postJSON("/api/network/wan/static", {
        address: address.trim(),
        gateway: gateway.trim(),
        dns: dns
          .split(",")
          .map((server) => server.trim())
          .filter(Boolean),
      });
      const value: unknown = await readJSON(response);
      if (!response.ok) {
        throw new Error(
          errorMessage(value, "Static WAN settings could not be applied."),
        );
      }
      if (!isTransaction(value)) {
        throw new Error("The router returned an invalid rollback transaction.");
      }
      setPending(value);
      setFeedback(
        "Settings are active temporarily. Check the WAN connection, then save them or restore the previous configuration.",
      );
      setFeedbackIsError(false);
    } catch (error) {
      setFeedback(
        error instanceof Error
          ? error.message
          : "Static WAN settings could not be applied.",
      );
      setFeedbackIsError(true);
    } finally {
      setIsBusy(false);
    }
  }

  async function finishTransaction(confirm: boolean) {
    if (pending === null) return;
    setIsBusy(true);
    setFeedback("");
    try {
      const path = confirm
        ? "/api/network/wan/static/confirm"
        : "/api/network/wan/static/rollback";
      const response = await postJSON(path, {
        transaction_id: pending.transaction_id,
      });
      const value: unknown =
        response.status === 204 ? null : await readJSON(response);
      if (!response.ok) {
        throw new Error(
          errorMessage(
            value,
            confirm
              ? "The WAN settings could not be confirmed. Automatic rollback remains armed."
              : "The previous WAN settings could not be restored yet.",
          ),
        );
      }
      setPending(null);
      setFeedback(
        confirm
          ? "Static WAN settings are confirmed on enp1s0."
          : "The previous WAN configuration was restored.",
      );
      setFeedbackIsError(false);
    } catch (error) {
      setFeedback(
        error instanceof Error
          ? error.message
          : "The WAN transaction could not be completed.",
      );
      setFeedbackIsError(true);
    } finally {
      setIsBusy(false);
    }
  }

  async function handleLogout() {
    setIsBusy(true);
    try {
      const response = await fetch("/api/auth/session", {
        method: "DELETE",
        cache: "no-store",
        credentials: "same-origin",
      });
      if (!response.ok) throw new Error("Unable to sign out.");
      setSession("anonymous");
      setMustChangePassword(false);
      setPending(null);
      setFeedback("Signed out.");
      setFeedbackIsError(false);
    } catch {
      setFeedback("Unable to sign out.");
      setFeedbackIsError(true);
    } finally {
      setIsBusy(false);
    }
  }

  const countdown = `${Math.floor(secondsRemaining / 60)}:${String(secondsRemaining % 60).padStart(2, "0")}`;

  return (
    <section className="static-wan-card" aria-labelledby="static-wan-title">
      <div className="static-wan-heading">
        <div>
          <p className="eyebrow">WAN CONFIGURATION</p>
          <h2 id="static-wan-title">Static IPv4 settings</h2>
          <p className="port-description">
            Configure the WAN port with an address, gateway, and DNS servers.
          </p>
        </div>
        {session === "authenticated" && !mustChangePassword && (
          <button
            className="refresh-button"
            type="button"
            onClick={() => void handleLogout()}
            disabled={isBusy || pending !== null}
          >
            Sign out
          </button>
        )}
      </div>

      {session === "checking" && (
        <p className="port-feedback" aria-live="polite">
          Checking administrator session…
        </p>
      )}

      {session === "anonymous" && (
        <form className="static-wan-form" onSubmit={handleLogin}>
          <p className="static-wan-note">
            Sign in as the router administrator to change WAN settings.
          </p>
          <label className="static-wan-field">
            Administrator password
            <input
              type="password"
              autoComplete="current-password"
              value={loginPassword}
              onChange={(event) => setLoginPassword(event.target.value)}
              required
            />
          </label>
          <div className="static-wan-actions">
            <button className="primary-button" type="submit" disabled={isBusy}>
              {isBusy ? "Signing in…" : "Sign in"}
            </button>
          </div>
        </form>
      )}

      {session === "authenticated" && mustChangePassword && (
        <form className="static-wan-form" onSubmit={handlePasswordChange}>
          <p className="static-wan-note">
            Replace the initial administrator password before changing network
            settings.
          </p>
          <label className="static-wan-field">
            Current administrator password
            <input
              type="password"
              autoComplete="current-password"
              value={currentPassword}
              onChange={(event) => setCurrentPassword(event.target.value)}
              required
            />
          </label>
          <label className="static-wan-field">
            New password
            <input
              type="password"
              autoComplete="new-password"
              minLength={12}
              value={newPassword}
              onChange={(event) => setNewPassword(event.target.value)}
              required
            />
          </label>
          <div className="static-wan-actions">
            <button className="primary-button" type="submit" disabled={isBusy}>
              {isBusy ? "Updating password…" : "Update password"}
            </button>
          </div>
        </form>
      )}

      {session === "authenticated" && !mustChangePassword && (
        <>
          <form className="static-wan-form" onSubmit={handleApply}>
            <div className="static-wan-fields">
              <label className="static-wan-field">
                IPv4 address / prefix
                <input
                  type="text"
                  inputMode="text"
                  autoComplete="off"
                  placeholder="192.168.1.104/24"
                  value={address}
                  onChange={(event) => {
                    setFormDirty(true);
                    setAddress(event.target.value);
                  }}
                  disabled={isBusy || pending !== null}
                  required
                />
              </label>
              <label className="static-wan-field">
                Gateway
                <input
                  type="text"
                  inputMode="decimal"
                  autoComplete="off"
                  placeholder="192.168.1.1"
                  value={gateway}
                  onChange={(event) => {
                    setFormDirty(true);
                    setGateway(event.target.value);
                  }}
                  disabled={isBusy || pending !== null}
                  required
                />
              </label>
              <label className="static-wan-field static-wan-field--wide">
                DNS servers, separated by commas
                <input
                  type="text"
                  inputMode="decimal"
                  autoComplete="off"
                  placeholder="192.168.1.1, 1.1.1.1"
                  value={dns}
                  onChange={(event) => {
                    setFormDirty(true);
                    setDNS(event.target.value);
                  }}
                  disabled={isBusy || pending !== null}
                  required
                />
              </label>
            </div>

            <p className="static-wan-note">
              Settings affect <strong>enp1s0</strong> only. Applying starts a
              120-second rollback window. Confirm only after checking WAN
              access; otherwise restore now or let the timer restore the
              previous configuration.
            </p>

            {pending === null ? (
              <div className="static-wan-actions">
                <button
                  className="primary-button"
                  type="submit"
                  disabled={isBusy}
                >
                  {isBusy ? "Applying…" : "Apply temporarily"}
                </button>
              </div>
            ) : (
              <div className="static-wan-pending" aria-live="polite">
                <p>
                  Automatic rollback in <strong>{countdown}</strong>
                </p>
                <div className="static-wan-actions">
                  <button
                    className="primary-button"
                    type="button"
                    onClick={() => void finishTransaction(true)}
                    disabled={isBusy}
                  >
                    Confirm settings
                  </button>
                  <button
                    className="danger-button"
                    type="button"
                    onClick={() => void finishTransaction(false)}
                    disabled={isBusy}
                  >
                    Restore previous settings
                  </button>
                </div>
              </div>
            )}
          </form>
          <WANLinePanel />
          <LANPanel />
        </>
      )}

      {feedback !== "" && (
        <p
          className={`static-wan-feedback${feedbackIsError ? " static-wan-feedback--error" : ""}`}
          role={feedbackIsError ? "alert" : "status"}
          aria-live="polite"
        >
          {feedback}
        </p>
      )}
    </section>
  );
}
