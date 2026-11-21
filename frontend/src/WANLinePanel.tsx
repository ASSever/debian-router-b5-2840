import { useEffect, useState, type FormEvent } from "react";

const endpoint = "/api/network/wan/line";

type Settings = {
  vlan_id: number;
  priority: number;
  mtu: number;
  mac: string;
};

type Status = {
  config: Settings;
  interface: string;
  lease_state: string;
  addresses: string[];
  gateway: string;
};

type Transaction = {
  transaction_id: string;
  expires_at: string;
};

async function request<T>(suffix = "", body?: object): Promise<T> {
  const response = await fetch(endpoint + suffix, {
    method: body ? "POST" : "GET",
    cache: "no-store",
    signal: AbortSignal.timeout(22_000),
    ...(body && {
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }),
  });
  const data = await response.json();
  if (!response.ok) {
    const messages: Record<string, string> = {
      invalid_vlan_id: "Enter a VLAN ID from 1 to 4094 or leave it blank.",
      invalid_vlan_priority: "Enter a priority from 0 to 7 with a VLAN ID.",
      invalid_wan_mtu: "Enter an MTU from 1280 to 1500 or leave it blank.",
      invalid_wan_mac: "Enter a valid unicast Ethernet MAC address.",
      static_wan_transaction_failed:
        "The change could not be completed. Check the DHCP lease and try again before the rollback deadline.",
      wan_line_apply_failed:
        "Could not apply the line settings. Check that IPv4 DHCP is active on the WAN port and no other WAN change is pending.",
    };
    throw new Error(messages[data?.error] ?? "WAN line request failed.");
  }
  return data as T;
}

export default function WANLinePanel() {
  const [status, setStatus] = useState<Status | null>(null);
  const [settings, setSettings] = useState<Settings>({
    vlan_id: 0,
    priority: 0,
    mtu: 0,
    mac: "",
  });
  const [pending, setPending] = useState<Transaction | null>(null);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [seconds, setSeconds] = useState(0);
  const [reload, setReload] = useState(0);

  useEffect(() => {
    let active = true;
    const timer = window.setTimeout(
      () => {
        void request<Status>()
          .then((value) => {
            if (!active) return;
            setStatus(value);
            setSettings(value.config);
          })
          .catch(() => {
            if (active) setMessage("WAN line status is unavailable.");
          });
      },
      reload === 0 ? 0 : 2_000,
    );
    return () => {
      active = false;
      window.clearTimeout(timer);
    };
  }, [reload]);

  useEffect(() => {
    if (!pending) return;
    let active = true;
    const expires = Date.parse(pending.expires_at);
    const interval = window.setInterval(() => {
      const remaining = Math.max(0, Math.ceil((expires - Date.now()) / 1_000));
      setSeconds(remaining);
      if (remaining === 0) {
        setPending(null);
        setStatus(null);
        setReload((value) => value + 1);
        setMessage("The rollback deadline has passed. Checking WAN status.");
      }
    }, 1_000);
    const poll = () => {
      void request<Status>()
        .then((value) => {
          if (active) setStatus(value);
        })
        .catch(() => undefined);
    };
    poll();
    const refresh = window.setInterval(poll, 3_000);
    return () => {
      active = false;
      window.clearInterval(interval);
      window.clearInterval(refresh);
    };
  }, [pending]);

  async function update(action: "apply" | "confirm" | "rollback") {
    setBusy(true);
    setMessage("");
    try {
      if (action === "apply") {
        const result = await request<Transaction>("", {
          ...settings,
          mac: settings.mac.trim(),
        });
        setSeconds(
          Math.max(
            0,
            Math.ceil((Date.parse(result.expires_at) - Date.now()) / 1_000),
          ),
        );
        setStatus(null);
        setPending(result);
        return;
      }
      if (!pending) return;
      await request("/" + action, { transaction_id: pending.transaction_id });
      setPending(null);
      const value = await request<Status>();
      setStatus(value);
      setSettings(value.config);
      setMessage(
        action === "confirm"
          ? "WAN line settings saved."
          : "Previous WAN line settings restored.",
      );
    } catch (error) {
      setMessage(
        error instanceof Error ? error.message : "WAN line request failed.",
      );
    } finally {
      setBusy(false);
    }
  }

  function apply(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    void update("apply");
  }

  const locked = busy || pending !== null || status === null;
  return (
    <form className="static-wan-form" onSubmit={apply}>
      <h3>DHCP WAN line settings</h3>
      <p className="static-wan-note">
        Leave VLAN ID, MTU and MAC blank for the defaults. Changes roll back
        after 120 seconds unless confirmed with an active DHCP lease.
      </p>
      {status && (
        <p className="static-wan-note" aria-live="polite">
          {status.interface} · {status.lease_state.replaceAll("_", " ")}
          {status.addresses.length > 0 && " · " + status.addresses.join(", ")}
          {status.gateway && " · Gateway " + status.gateway}
        </p>
      )}
      <div className="static-wan-fields">
        <label className="static-wan-field">
          VLAN ID
          <input
            type="number"
            min="1"
            max="4094"
            placeholder="Untagged"
            value={settings.vlan_id || ""}
            onChange={(event) => {
              const vlan_id = Number(event.target.value);
              setSettings({
                ...settings,
                vlan_id,
                priority: vlan_id ? settings.priority : 0,
              });
            }}
            disabled={locked}
          />
        </label>
        <label className="static-wan-field">
          VLAN egress priority (PCP)
          <input
            type="number"
            min="0"
            max="7"
            value={settings.priority}
            onChange={(event) =>
              setSettings({ ...settings, priority: Number(event.target.value) })
            }
            disabled={locked || settings.vlan_id === 0}
          />
        </label>
        <label className="static-wan-field">
          MTU
          <input
            type="number"
            min="1280"
            max="1500"
            placeholder="Default"
            value={settings.mtu || ""}
            onChange={(event) =>
              setSettings({ ...settings, mtu: Number(event.target.value) })
            }
            disabled={locked}
          />
        </label>
        <label className="static-wan-field">
          MAC clone
          <input
            type="text"
            autoComplete="off"
            maxLength={17}
            placeholder="Original address"
            value={settings.mac}
            onChange={(event) =>
              setSettings({ ...settings, mac: event.target.value })
            }
            disabled={locked}
          />
        </label>
      </div>
      <div className="static-wan-actions">
        {pending ? (
          <>
            <button
              className="primary-button"
              type="button"
              disabled={
                busy || status?.lease_state !== "leased" || !status.gateway
              }
              onClick={() => void update("confirm")}
            >
              Save line settings ({seconds}s)
            </button>
            <button
              className="danger-button"
              type="button"
              disabled={busy}
              onClick={() => void update("rollback")}
            >
              Restore line settings
            </button>
          </>
        ) : (
          <button className="primary-button" type="submit" disabled={locked}>
            Apply line settings temporarily
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
