import { useEffect, useState, type FormEvent } from "react";

const endpoint = "/api/network/lan/dns";

type DNSConfig = {
  primary: string;
  backup: string;
  router_only: boolean;
  records: { name: string; address: string }[];
};
type DNSStatus = { config: DNSConfig; managed: boolean };

async function readSettings(): Promise<DNSStatus> {
  const response = await fetch(endpoint, {
    cache: "no-store",
    signal: AbortSignal.timeout(8_000),
  });
  if (!response.ok) throw new Error("Could not load LAN DNS settings.");
  return response.json();
}

export default function LANDNSPanel() {
  const [managed, setManaged] = useState<boolean>();
  const [primary, setPrimary] = useState("");
  const [backup, setBackup] = useState("");
  const [routerOnly, setRouterOnly] = useState(false);
  const [records, setRecords] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");

  function fill(value: DNSStatus) {
    setManaged(value.managed);
    setPrimary(value.config.primary);
    setBackup(value.config.backup);
    setRouterOnly(value.config.router_only);
    setRecords(
      value.config.records
        .map(({ name, address }) => `${name} ${address}`)
        .join("\n"),
    );
  }

  useEffect(() => {
    let active = true;
    void readSettings()
      .then((value) => {
        if (active) fill(value);
      })
      .catch(() => {
        if (active) setMessage("Could not load LAN DNS settings.");
      });
    return () => {
      active = false;
    };
  }, []);

  async function update(action: "reload" | "save" | "clear") {
    setBusy(true);
    setMessage("");
    try {
      if (action === "reload") {
        fill(await readSettings());
        return;
      }
      let body: string | undefined;
      if (action === "save") {
        const entries = records
          .split("\n")
          .map((line) => line.trim())
          .filter(Boolean)
          .map((line) => {
            const fields = line.split(/\s+/);
            if (fields.length !== 2)
              throw new Error("Enter one DNS name and address per line.");
            return { name: fields[0], address: fields[1] };
          });
        body = JSON.stringify({
          primary: primary.trim(),
          backup: backup.trim(),
          router_only: routerOnly,
          records: entries,
        });
      }
      const response = await fetch(
        action === "clear" ? endpoint + "/clear-cache" : endpoint,
        {
          method: action === "clear" ? "POST" : "PUT",
          signal: AbortSignal.timeout(20_000),
          ...(body && {
            body,
            headers: { "Content-Type": "application/json" },
          }),
        },
      );
      if (!response.ok) {
        const { error } = await response.json();
        if (error === "invalid_lan_dns")
          throw new Error(
            "Check DNS names, addresses and upstreams. Use distinct upstreams and a primary before adding a backup.",
          );
        if (error === "network_change_pending")
          throw new Error("Finish the pending LAN or WAN change first.");
        throw new Error("Could not apply the DNS operation.");
      }
      if (action === "save") fill(await readSettings());
      setMessage(
        action === "clear" ? "DNS cache cleared." : "LAN DNS settings saved.",
      );
    } catch (error) {
      setMessage(
        error instanceof Error ? error.message : "Could not update LAN DNS.",
      );
    } finally {
      setBusy(false);
    }
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    void update("save");
  }
  const disabled = busy || !managed;
  return (
    <form className="static-wan-form" onSubmit={submit}>
      <h3>LAN DNS</h3>
      {managed === false && (
        <p className="static-wan-note">
          Apply and confirm a LAN profile first.
        </p>
      )}
      <div className="static-wan-fields">
        <label className="static-wan-field">
          Primary upstream IP
          <input
            value={primary}
            disabled={disabled}
            placeholder="System default"
            onChange={(event) => setPrimary(event.target.value)}
            autoComplete="off"
          />
        </label>
        <label className="static-wan-field">
          Backup upstream IP
          <input
            value={backup}
            disabled={disabled}
            onChange={(event) => setBackup(event.target.value)}
            autoComplete="off"
          />
        </label>
      </div>
      <label className="static-wan-field">
        Local records, one DNS name and IP address per line (up to 24)
        <textarea
          rows={4}
          value={records}
          disabled={disabled}
          placeholder="nas.home.arpa 192.168.10.20"
          onChange={(event) => setRecords(event.target.value)}
        />
      </label>
      <label>
        <input
          type="checkbox"
          checked={routerOnly}
          disabled={disabled}
          onChange={(event) => setRouterOnly(event.target.checked)}
        />
        Use router DNS only for routed TCP and UDP port 53
      </label>
      <div className="static-wan-actions">
        <button className="primary-button" type="submit" disabled={disabled}>
          Save DNS settings
        </button>
        <button
          className="refresh-button"
          type="button"
          disabled={disabled}
          onClick={() => void update("clear")}
        >
          Clear DNS cache
        </button>
        <button
          className="refresh-button"
          type="button"
          disabled={busy}
          onClick={() => void update("reload")}
        >
          Refresh DNS settings
        </button>
      </div>
      {message && (
        <p role="status" className="static-wan-feedback">
          {message}
        </p>
      )}
    </form>
  );
}
