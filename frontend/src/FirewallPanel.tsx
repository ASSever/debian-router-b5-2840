import { useEffect, useState } from "react";
import FirewallRulesPanel, {
  type FirewallTransaction,
} from "./FirewallRulesPanel";

const endpoint = "/api/network/firewall";
type Status = { enabled: boolean; wan?: string; pending?: FirewallTransaction };

async function request<T>(path = "", post = false, body?: object): Promise<T> {
  const response = await fetch(endpoint + path, {
    cache: "no-store",
    method: post ? "POST" : "GET",
    signal: AbortSignal.timeout(20_000),
    ...(body && {
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }),
  });
  const data = await response.json();
  if (!response.ok) {
    const details: Record<string, string> = {
      network_change_pending: "Finish the current network change first.",
      firewall_unavailable:
        "Check that LAN settings are confirmed and WAN has an IPv4 route.",
      static_wan_transaction_failed:
        "The operation failed. Check LAN and WAN before retrying.",
    };
    throw new Error(
      details[data.error] ?? "Could not update firewall settings.",
    );
  }
  return data;
}

export default function FirewallPanel() {
  const [status, setStatus] = useState<Status>();
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [seconds, setSeconds] = useState(0);
  const pending = status?.pending;

  useEffect(() => {
    let active = true;
    void request<Status>()
      .then((value) => {
        if (active) setStatus(value);
      })
      .catch(() => {
        if (active) setMessage("Firewall status is unavailable.");
      });
    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    if (!pending) return;
    let active = true;
    const interval = window.setInterval(() => {
      const remaining = Math.max(
        0,
        Math.ceil((Date.parse(pending.expires_at) - Date.now()) / 1000),
      );
      setSeconds(remaining);
      if (!remaining) {
        window.clearInterval(interval);
        void request<Status>()
          .then((value) => {
            if (active) setStatus(value);
          })
          .catch(() => {
            if (active)
              setMessage("Refresh firewall status to check rollback.");
          });
      }
    }, 1000);
    return () => {
      active = false;
      window.clearInterval(interval);
    };
  }, [pending]);

  function showPending(value: FirewallTransaction) {
    setSeconds(
      Math.max(
        0,
        Math.ceil((Date.parse(value.expires_at) - Date.now()) / 1000),
      ),
    );
    setStatus((previous) => ({ ...previous, enabled: true, pending: value }));
  }

  async function update(action: "apply" | "confirm" | "rollback" | "refresh") {
    setBusy(true);
    setMessage("");
    try {
      if (action === "apply") {
        showPending(await request<FirewallTransaction>("", true));
      } else {
        if (action !== "refresh" && pending) {
          await request("/" + action, true, {
            transaction_id: pending.transaction_id,
          });
        }
        setStatus(await request<Status>());
      }
    } catch (error) {
      setMessage(
        error instanceof Error
          ? error.message
          : "Could not update firewall settings.",
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="static-wan-form" aria-label="Firewall and NAT">
      <h3>Firewall and NAT</h3>
      {status?.wan && <p>WAN {status.wan}</p>}
      {pending ? (
        <>
          <p>
            Verify traffic and management access over Wi-Fi before confirming.
            Automatic rollback in {seconds}s.
          </p>
          <div className="static-wan-actions">
            <button
              type="button"
              className="primary-button"
              disabled={busy || !seconds}
              onClick={() => void update("confirm")}
            >
              Confirm firewall
            </button>
            <button
              type="button"
              className="danger-button"
              disabled={busy}
              onClick={() => void update("rollback")}
            >
              Restore previous firewall
            </button>
          </div>
        </>
      ) : (
        <div className="static-wan-actions">
          {status?.enabled ? (
            <p>Firewall and IPv4 NAT are configured.</p>
          ) : (
            <button
              type="button"
              className="primary-button"
              disabled={busy || !status?.wan}
              onClick={() => void update("apply")}
            >
              Apply firewall and NAT temporarily
            </button>
          )}
          <button
            type="button"
            className="refresh-button"
            disabled={busy}
            onClick={() => void update("refresh")}
          >
            Refresh firewall status
          </button>
        </div>
      )}
      {message && (
        <p role="status" className="static-wan-feedback">
          {message}
        </p>
      )}
      {status?.enabled && (
        <FirewallRulesPanel
          key={pending?.transaction_id ?? "confirmed"}
          readOnly={busy || Boolean(pending)}
          onPending={showPending}
        />
      )}
    </section>
  );
}
