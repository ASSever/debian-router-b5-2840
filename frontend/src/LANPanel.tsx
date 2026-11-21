import { useEffect, useState, type FormEvent } from "react";

const endpoint = "/api/network/lan";
const lanURL = "https://router.home.arpa:8443";

type Settings = { address: string; pool_start: string; pool_end: string };
type Transaction = { transaction_id: string; expires_at: string };
type Status = {
  config: Settings;
  addresses: string[];
  managed: boolean;
  wan_online: boolean;
  pending?: Transaction;
};
type Conflict = { interfaces: string[]; prefix: string; suggestion?: Settings };

class LANError extends Error {
  constructor(
    code: string,
    readonly conflict?: Conflict,
  ) {
    const messages: Record<string, string> = {
      invalid_lan_address: "Enter a usable IPv4 address and prefix.",
      invalid_lan_pool:
        "Enter an ordered pool within the LAN subnet, excluding the router, network and broadcast addresses.",
      network_change_pending:
        "Finish the current network change before applying LAN settings.",
      lan_reservation_conflict:
        "Edit or remove DHCP reservations that fall outside the new LAN or use its gateway address.",
      lan_dns_address_conflict:
        "The new LAN gateway matches a configured DNS upstream. Change the DNS upstream first.",
      lan_firewall_target_conflict:
        "Remove port forwards whose destinations would fall outside the new LAN or use its gateway address.",
      lan_routing_conflict:
        "Remove static routes whose destination overlaps the new LAN subnet.",
      lan_confirmation_requires_new_address:
        "Reconnect the LAN client and open the new address before confirming.",
      lan_confirmation_failed:
        "The router could not verify the new LAN. Automatic rollback remains armed until the deadline.",
    };
    super(
      conflict
        ? `The LAN overlaps ${conflict.prefix} on ${conflict.interfaces.join(", ")}.`
        : (messages[code] ?? "The LAN request could not be completed."),
    );
  }
}

async function request<T>(suffix = "", body?: object): Promise<T> {
  const response = await fetch(endpoint + suffix, {
    cache: "no-store",
    signal: AbortSignal.timeout(22_000),
    ...(body && {
      method: "POST",
      body: JSON.stringify(body),
      headers: { "Content-Type": "application/json" },
    }),
  });
  const data = await response.json();
  if (!response.ok) {
    throw new LANError(
      data.error,
      data.error === "lan_overlap" ? data : undefined,
    );
  }
  return data;
}

export default function LANPanel() {
  const [status, setStatus] = useState<Status | null>(null);
  const [form, setForm] = useState<Settings>({
    address: "",
    pool_start: "",
    pool_end: "",
  });
  const [busy, setBusy] = useState(false);
  const [pending, setPending] = useState<Transaction | null>(null);
  const [seconds, setSeconds] = useState(0);
  const [message, setMessage] = useState("");
  const [suggestion, setSuggestion] = useState<Settings>();
  const [recheck, setRecheck] = useState(0);

  useEffect(() => {
    let active = true;
    const timer = window.setTimeout(
      () => {
        void request<Status>()
          .then((value) => {
            if (!active) return;
            setStatus(value);
            setForm(value.config);
            if (value.pending) {
              setPending(value.pending);
              setSeconds(
                Math.max(
                  0,
                  Math.ceil(
                    (Date.parse(value.pending.expires_at) - Date.now()) / 1000,
                  ),
                ),
              );
            }
          })
          .catch(() => {
            if (active) setMessage("LAN status is unavailable.");
          });
      },
      recheck > 0 ? 1500 : 0,
    );
    return () => {
      active = false;
      window.clearTimeout(timer);
    };
  }, [recheck]);

  useEffect(() => {
    if (!pending) return;
    const interval = window.setInterval(() => {
      const remaining = Math.max(
        0,
        Math.ceil((Date.parse(pending.expires_at) - Date.now()) / 1000),
      );
      setSeconds(remaining);
      if (remaining === 0) {
        setPending(null);
        setStatus(null);
        setMessage(
          "Confirmation time expired. Reconnect to the previous LAN to check the restored settings.",
        );
      }
    }, 1000);
    return () => window.clearInterval(interval);
  }, [pending]);

  async function update(action: "apply" | "confirm" | "rollback") {
    setBusy(true);
    setMessage("");
    setSuggestion(undefined);
    try {
      if (action === "apply") {
        const values = {
          address: form.address.trim(),
          pool_start: form.pool_start.trim(),
          pool_end: form.pool_end.trim(),
        };
        const result = await request<Transaction>("", values);
        setForm(values);
        setSeconds(
          Math.max(
            0,
            Math.ceil((Date.parse(result.expires_at) - Date.now()) / 1000),
          ),
        );
        setPending(result);
      } else if (pending) {
        await request("/" + action, { transaction_id: pending.transaction_id });
        setPending(null);
        setStatus(null);
        setRecheck((old) => old + 1);
        setMessage(
          action === "confirm"
            ? "LAN settings saved."
            : "Previous LAN settings restored.",
        );
      }
    } catch (error) {
      if (error instanceof LANError) setSuggestion(error.conflict?.suggestion);
      setMessage(
        error instanceof Error
          ? error.message
          : "The LAN request could not be completed.",
      );
      if (action === "apply" && !(error instanceof LANError))
        setRecheck((old) => old + 1);
    } finally {
      setBusy(false);
    }
  }

  function apply(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    void update("apply");
  }

  return (
    <form className="static-wan-form" onSubmit={apply}>
      <h3>Network / LAN</h3>
      {status && !pending && (
        <p className="static-wan-note">
          Active addresses: {status.addresses.join(", ") || "none"}.
          {!status.wan_online &&
            " WAN has no current IPv4 address. Check overlap again after it connects."}
        </p>
      )}
      <div className="static-wan-fields">
        {(
          [
            ["address", "LAN IPv4 address / prefix"],
            ["pool_start", "DHCP pool start"],
            ["pool_end", "DHCP pool end"],
          ] as const
        ).map(([field, label]) => (
          <label className="static-wan-field" key={field}>
            {label}
            <input
              required
              autoComplete="off"
              value={form[field]}
              disabled={busy || !!pending || !status}
              onChange={(event) =>
                setForm({ ...form, [field]: event.target.value })
              }
            />
          </label>
        ))}
      </div>
      {pending ? (
        <p className="static-wan-note">
          Renew the LAN client's DHCP lease, then open{" "}
          <a href={lanURL}>{lanURL}</a> at {form.address}. Confirm from the new
          LAN before automatic rollback in {seconds}s.
        </p>
      ) : (
        <p className="static-wan-note">
          Applying changes the LAN gateway and DHCP pool. Open{" "}
          <a href={lanURL}>{lanURL}</a> from the LAN to confirm within five
          minutes.
        </p>
      )}
      <div className="static-wan-actions">
        {pending ? (
          <>
            <button
              className="primary-button"
              type="button"
              disabled={busy || window.location.port !== "8443"}
              onClick={() => void update("confirm")}
            >
              Confirm LAN settings
            </button>
            <button
              className="danger-button"
              type="button"
              disabled={busy}
              onClick={() => void update("rollback")}
            >
              Restore previous LAN
            </button>
          </>
        ) : (
          <button
            className="primary-button"
            type="submit"
            disabled={busy || !status}
          >
            Apply LAN temporarily
          </button>
        )}
        {suggestion && (
          <button
            className="refresh-button"
            type="button"
            onClick={() => {
              setForm(suggestion);
              setSuggestion(undefined);
              setMessage("");
            }}
          >
            Use {suggestion.address}
          </button>
        )}
        {!status && !pending && !busy && (
          <button
            className="refresh-button"
            type="button"
            onClick={() => setRecheck((old) => old + 1)}
          >
            Refresh LAN status
          </button>
        )}
      </div>
      {message && (
        <p className="static-wan-feedback" role="status">
          {message}
        </p>
      )}
    </form>
  );
}
