import { useEffect, useState, type FormEvent } from "react";

import "./dashboard.css";

type Rate = {
  name: string;
  rx_bytes_per_second: number | null;
  tx_bytes_per_second: number | null;
};

type Space = { used_bytes: number; total_bytes: number };
type Link = {
  name: string;
  operational_state: string;
  speed_mbps: number | null;
  addresses: string[];
};

type Point = {
  sampled_at: string;
  traffic_available: boolean;
  interfaces: Rate[];
  clients: Rate[];
  lan_prefixes: string[];
  system: {
    cpu_percent: number | null;
    temperature_c: number | null;
    memory: Space | null;
    disk: Space | null;
    uptime_seconds: number | null;
    wan: Link | null;
    lan: Link | null;
    wan_gateway: string | null;
    wan_dns: string[] | null;
    firewall_enabled: boolean | null;
    services: { unit: string; state: string }[];
  };
};
type History = { stale: boolean; points: Point[] };
type Access = "loading" | "signed-out" | "change-password" | "signed-in";

export default function Dashboard() {
  const [open, setOpen] = useState(false);
  return (
    <section
      className="dashboard status-card"
      aria-labelledby="dashboard-title"
    >
      <div className="dashboard-heading">
        <h2 id="dashboard-title">Dashboard</h2>
        <button
          className="refresh-button"
          type="button"
          aria-expanded={open}
          onClick={() => setOpen(!open)}
        >
          {open ? "Close dashboard" : "Open dashboard"}
        </button>
      </div>
      {open && <DashboardContent />}
    </section>
  );
}

function DashboardContent() {
  const [access, setAccess] = useState<Access>("loading");
  const [history, setHistory] = useState<History | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [revision, setRevision] = useState(0);

  useEffect(() => {
    let active = true;
    let pending = false;
    const refresh = async () => {
      if (pending) return;
      pending = true;
      try {
        const response = await fetch("/api/dashboard", {
          signal: AbortSignal.timeout(8000),
        });
        if (!active) return;
        if (response.status === 401 || response.status === 403) {
          setAccess(response.status === 401 ? "signed-out" : "change-password");
          setHistory(null);
          setError("");
          return;
        }
        if (!response.ok) throw new Error("Dashboard unavailable");
        const data: History = await response.json();
        if (!active) return;
        setHistory(data);
        setAccess("signed-in");
        setError("");
      } catch {
        if (active) setError("Dashboard data is unavailable. Retrying…");
      } finally {
        pending = false;
      }
    };
    void refresh();
    const timer = window.setInterval(() => {
      if (!document.hidden) void refresh();
    }, 30_000);
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, [revision]);

  const authenticate = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = event.currentTarget;
    const input = new FormData(form);
    const changing = access === "change-password";
    const password = input.get("password");
    setBusy(true);
    setError("");
    try {
      const result = await fetch(
        changing ? "/api/auth/password" : "/api/auth/login",
        {
          method: changing ? "PUT" : "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(
            changing
              ? {
                  current_password: password,
                  new_password: input.get("replacement"),
                }
              : { password },
          ),
          signal: AbortSignal.timeout(8000),
        },
      );
      if (!result.ok) {
        setError(changing ? "Password change failed." : "Sign-in failed.");
        return;
      }
      form.reset();
      setRevision((value) => value + 1);
    } catch {
      setError("Authentication is unavailable.");
    } finally {
      setBusy(false);
    }
  };

  const signOut = async () => {
    setBusy(true);
    try {
      const result = await fetch("/api/auth/session", {
        method: "DELETE",
        signal: AbortSignal.timeout(8000),
      });
      if (!result.ok) throw new Error("Sign-out failed");
      setHistory(null);
      setAccess("signed-out");
      setRevision((value) => value + 1);
    } catch {
      setError("Sign-out failed.");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="dashboard-body">
      {error && <p role="alert">{error}</p>}
      {access === "loading" && !error && (
        <p role="status">Loading measurements…</p>
      )}
      {(access === "signed-out" || access === "change-password") && (
        <form
          className="dashboard-login"
          onSubmit={(event) => void authenticate(event)}
        >
          <label>
            Administrator password
            <input
              required
              name="password"
              type="password"
              autoComplete="current-password"
            />
          </label>
          {access === "change-password" && (
            <label>
              New password
              <input
                required
                name="replacement"
                type="password"
                autoComplete="new-password"
              />
            </label>
          )}
          <button className="refresh-button" type="submit" disabled={busy}>
            {access === "change-password" ? "Change password" : "Sign in"}
          </button>
        </form>
      )}
      {access === "signed-in" && (
        <>
          <div className="dashboard-actions">
            <Download
              label="Download failure report"
              filename="router-failure-report.json"
              endpoint="/api/diagnostics/report"
              errorMessage="The failure report could not be downloaded. Try again."
            />
            <Download
              label="Download backup"
              filename="router-backup.zip"
              endpoint="/api/backup"
              method="POST"
              errorMessage="The backup could not be downloaded. Try again after pending changes are resolved."
            >
              The backup can contain credentials. Store it securely.
            </Download>
            <button
              className="refresh-button"
              type="button"
              disabled={busy}
              onClick={() => void signOut()}
            >
              Sign out
            </button>
          </div>
          {history && (
            <Measurements history={history} disconnected={Boolean(error)} />
          )}
        </>
      )}
    </div>
  );
}

function Download({
  label,
  filename,
  endpoint,
  method = "GET",
  errorMessage,
  children,
}: {
  label: string;
  filename: string;
  endpoint: string;
  method?: "GET" | "POST";
  errorMessage: string;
  children?: string;
}) {
  const [downloading, setDownloading] = useState(false);
  const [error, setError] = useState("");

  const download = async () => {
    setDownloading(true);
    setError("");
    try {
      const response = await fetch(endpoint, {
        method,
        cache: "no-store",
        signal: AbortSignal.timeout(method === "POST" ? 140_000 : 25_000),
      });
      if (response.status === 401 || response.status === 403) {
        setError("Sign in as administrator to download this file.");
        return;
      }
      if (!response.ok) throw new Error("Download unavailable");
      const url = URL.createObjectURL(await response.blob());
      const link = document.createElement("a");
      link.href = url;
      link.download = filename;
      link.click();
      window.setTimeout(URL.revokeObjectURL, 1000, url);
    } catch {
      setError(errorMessage);
    } finally {
      setDownloading(false);
    }
  };

  return (
    <div>
      <button
        className="refresh-button"
        type="button"
        disabled={downloading}
        onClick={() => void download()}
      >
        {downloading ? "Preparing download…" : label}
      </button>
      {children && <p>{children}</p>}
      {error && <p role="alert">{error}</p>}
    </div>
  );
}

function formatValue(value: number | null, unit: string) {
  return value === null
    ? "Unavailable"
    : `${value.toLocaleString(undefined, { maximumFractionDigits: 1 })} ${unit}`;
}

function formatSpace(space: Space | null) {
  return space
    ? `${formatValue(space.used_bytes / 2 ** 30, "GiB")} / ${formatValue(space.total_bytes / 2 ** 30, "GiB")}`
    : "Unavailable";
}

function formatLink(link: Link | null) {
  return link
    ? [
        link.name,
        link.operational_state,
        ...link.addresses,
        link.speed_mbps === null ? null : `${link.speed_mbps} Mb/s`,
      ]
        .filter(Boolean)
        .join(" · ")
    : "Unavailable";
}

function Measurements({
  history,
  disconnected,
}: {
  history: History;
  disconnected: boolean;
}) {
  const latest = history.points.at(-1);
  if (!latest)
    return <p role="status">Waiting for the first saved measurement.</p>;
  const system = latest.system;
  return (
    <>
      <p className="dashboard-timestamp">
        {history.stale || disconnected
          ? "Stale measurement"
          : "Last measurement"}{" "}
        <time dateTime={latest.sampled_at}>
          {new Date(latest.sampled_at).toLocaleString()}
        </time>
      </p>
      <dl className="dashboard-resources">
        <div>
          <dt>WAN link</dt>
          <dd>{formatLink(system.wan)}</dd>
        </div>
        <div>
          <dt>LAN link</dt>
          <dd>{formatLink(system.lan)}</dd>
        </div>
        <div>
          <dt>WAN gateway</dt>
          <dd>
            {system.wan_gateway === null
              ? "Unavailable"
              : system.wan_gateway || "None"}
          </dd>
        </div>
        <div>
          <dt>WAN DNS</dt>
          <dd>
            {system.wan_dns === null
              ? "Unavailable"
              : system.wan_dns.join(", ") || "None"}
          </dd>
        </div>
        <div>
          <dt>Router firewall</dt>
          <dd>
            {system.firewall_enabled === null
              ? "Unavailable"
              : system.firewall_enabled
                ? "Enabled"
                : "Disabled"}
          </dd>
        </div>
        <div>
          <dt>LAN IPs with counters</dt>
          <dd>
            {latest.traffic_available ? latest.clients.length : "Unavailable"}
          </dd>
        </div>
        <div>
          <dt>CPU</dt>
          <dd>{formatValue(system.cpu_percent, "%")}</dd>
        </div>
        <div>
          <dt>CPU temperature</dt>
          <dd>{formatValue(system.temperature_c, "°C")}</dd>
        </div>
        <div>
          <dt>RAM used</dt>
          <dd>{formatSpace(system.memory)}</dd>
        </div>
        <div>
          <dt>Storage used</dt>
          <dd>{formatSpace(system.disk)}</dd>
        </div>
        <div>
          <dt>System uptime</dt>
          <dd>
            {formatValue(
              system.uptime_seconds === null
                ? null
                : Math.floor(system.uptime_seconds / 60),
              "min",
            )}
          </dd>
        </div>
      </dl>
      <h3>Services</h3>
      <ul className="dashboard-services">
        {system.services.map((service) => (
          <li key={service.unit}>
            <span>
              {service.unit
                .replace(/^debian-router-/, "")
                .replace(/\.service$/, "")}
            </span>
            <strong data-state={service.state}>
              {service.state.replaceAll("_", " ")}
            </strong>
          </li>
        ))}
      </ul>
      {!latest.traffic_available && (
        <p role="status">Current traffic counters are unavailable.</p>
      )}
      <TrafficChart
        title="Interface traffic"
        points={history.points}
        kind="interfaces"
        labels={["RX", "TX"]}
      />
      <TrafficChart
        title="LAN client traffic"
        points={history.points}
        kind="clients"
        labels={["Download", "Upload"]}
      />
    </>
  );
}

function TrafficChart({
  title,
  points,
  kind,
  labels,
}: {
  title: string;
  points: Point[];
  kind: "interfaces" | "clients";
  labels: [string, string];
}) {
  const [selected, setSelected] = useState("");
  const names = [
    ...new Set(
      points.flatMap((point) => point[kind].map((value) => value.name)),
    ),
  ].sort();
  const name = names.includes(selected)
    ? selected
    : (names.find((item) => item !== "lo") ?? names[0]);
  const data = points.map((point) => {
    const value = point[kind].find((row) => row.name === name);
    return {
      time: point.sampled_at,
      rx: value?.rx_bytes_per_second ?? null,
      tx: value?.tx_bytes_per_second ?? null,
    };
  });
  const max = Math.max(1, ...data.flatMap(({ rx, tx }) => [rx ?? 0, tx ?? 0]));
  const hasData = data.some(({ rx, tx }) => rx !== null || tx !== null);
  const first = Date.parse(data[0].time);
  const last = Date.parse(data[data.length - 1].time);
  const span = Math.max(last - first, 1);
  const tickWidth = Math.min(10, (30000 / span) * 80);
  const latest = data[data.length - 1];

  return (
    <figure className="dashboard-chart">
      <figcaption>
        <h3>{title}</h3>
        {name && (
          <select
            aria-label={title}
            value={name}
            onChange={(event) => setSelected(event.target.value)}
          >
            {names.map((item) => (
              <option key={item}>{item}</option>
            ))}
          </select>
        )}
      </figcaption>
      {hasData ? (
        <>
          <p className="dashboard-legend">
            <span>
              {labels[0]} {formatValue(latest.rx, "B/s")}
            </span>
            <span>
              {labels[1]} {formatValue(latest.tx, "B/s")}
            </span>
          </p>
          <p className="dashboard-scale">Scale 0–{formatValue(max, "B/s")}</p>
          <div
            className="dashboard-plot"
            aria-label={`${title} saved samples`}
            role="img"
          >
            {data.map(({ time, rx, tx }, index) => (
              <div
                key={`${time}-${index}`}
                className="dashboard-slot"
                style={{
                  left: `${((Date.parse(time) - first) / span) * (100 - tickWidth)}%`,
                  width: `${tickWidth}%`,
                }}
                title={`${new Date(time).toLocaleTimeString()} · ${labels[0]} ${formatValue(rx, "B/s")} · ${labels[1]} ${formatValue(tx, "B/s")}`}
              >
                {[rx, tx].map(
                  (value, direction) =>
                    value !== null && (
                      <span
                        key={direction}
                        className={`dashboard-bar dashboard-bar-${direction}`}
                        style={{
                          height: `${Math.max(2, (value / max) * 100)}%`,
                        }}
                      />
                    ),
                )}
              </div>
            ))}
          </div>
          <p className="dashboard-axis">
            <time>{new Date(first).toLocaleTimeString()}</time>
            <time>{new Date(last).toLocaleTimeString()}</time>
          </p>
        </>
      ) : (
        <p>No rate measurements for this series yet.</p>
      )}
    </figure>
  );
}
