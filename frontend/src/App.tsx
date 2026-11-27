import { useCallback, useEffect, useState } from "react";

import StaticWANPanel from "./StaticWANPanel";
import Dashboard from "./Dashboard";
import PPPoEPanel from "./PPPoEPanel";
import WANDiagnosticPanel from "./WANDiagnosticPanel";

const CHECK_INTERVAL_MS = 5_000;
const REQUEST_TIMEOUT_MS = 4_000;
const NETWORK_REQUEST_TIMEOUT_MS = 4_000;

type NetworkInterface = {
  name: string;
  operational_state: string;
  carrier: boolean | null;
  speed_mbps: number | null;
};

type NetworkInterfaceResponse = { interfaces: NetworkInterface[] };

type NetworkDHCPStatus = {
  interface: string;
  link_state: string;
  lease_state: "leased" | "link_down" | "waiting";
  addresses: string[];
  gateway: string;
  dns: string[];
};

type HealthState =
  | { status: "checking"; checkedAt: null }
  | { status: "online"; checkedAt: Date }
  | { status: "offline"; checkedAt: Date };

function isHealthyResponse(value: unknown): value is { status: "ok" } {
  return (
    typeof value === "object" &&
    value !== null &&
    "status" in value &&
    value.status === "ok"
  );
}

function isNetworkInterface(value: unknown): value is NetworkInterface {
  if (typeof value !== "object" || value === null || !("name" in value)) {
    return false;
  }

  return (
    typeof value.name === "string" &&
    "operational_state" in value &&
    typeof value.operational_state === "string" &&
    "carrier" in value &&
    (typeof value.carrier === "boolean" || value.carrier === null) &&
    "speed_mbps" in value &&
    (value.speed_mbps === null ||
      (typeof value.speed_mbps === "number" &&
        Number.isInteger(value.speed_mbps) &&
        value.speed_mbps > 0))
  );
}

function isNetworkInterfaceResponse(
  value: unknown,
): value is NetworkInterfaceResponse {
  return (
    typeof value === "object" &&
    value !== null &&
    "interfaces" in value &&
    Array.isArray(value.interfaces) &&
    value.interfaces.every(isNetworkInterface)
  );
}

function isNetworkDHCPStatus(value: unknown): value is NetworkDHCPStatus {
  if (typeof value !== "object" || value === null) {
    return false;
  }
  return (
    "interface" in value &&
    typeof value.interface === "string" &&
    "link_state" in value &&
    typeof value.link_state === "string" &&
    "lease_state" in value &&
    (value.lease_state === "leased" ||
      value.lease_state === "link_down" ||
      value.lease_state === "waiting") &&
    "addresses" in value &&
    Array.isArray(value.addresses) &&
    value.addresses.every((address) => typeof address === "string") &&
    "gateway" in value &&
    typeof value.gateway === "string" &&
    "dns" in value &&
    Array.isArray(value.dns) &&
    value.dns.every((server) => typeof server === "string")
  );
}

function formatTime(value: Date): string {
  return new Intl.DateTimeFormat(undefined, {
    hour: "numeric",
    minute: "2-digit",
    second: "2-digit",
  }).format(value);
}

async function requestHealth(): Promise<
  Exclude<HealthState, { status: "checking" }>
> {
  const controller = new AbortController();
  const timeout = window.setTimeout(
    () => controller.abort(),
    REQUEST_TIMEOUT_MS,
  );

  try {
    const response = await fetch("/healthz", {
      method: "GET",
      cache: "no-store",
      headers: { Accept: "application/json" },
      signal: controller.signal,
    });

    if (!response.ok || !isHealthyResponse(await response.json())) {
      throw new Error("The API returned an unexpected health response.");
    }

    return { status: "online", checkedAt: new Date() };
  } catch {
    return { status: "offline", checkedAt: new Date() };
  } finally {
    window.clearTimeout(timeout);
  }
}

export default function App() {
  const [health, setHealth] = useState<HealthState>({
    status: "checking",
    checkedAt: null,
  });
  const [isChecking, setIsChecking] = useState(true);

  const checkHealth = useCallback(async () => {
    setIsChecking(true);

    try {
      setHealth(await requestHealth());
    } finally {
      setIsChecking(false);
    }
  }, []);

  useEffect(() => {
    let active = true;
    const updateHealth = () => {
      void requestHealth().then((nextHealth) => {
        if (active) {
          setHealth(nextHealth);
          setIsChecking(false);
        }
      });
    };

    updateHealth();
    const interval = window.setInterval(updateHealth, CHECK_INTERVAL_MS);

    return () => {
      active = false;
      window.clearInterval(interval);
    };
  }, []);

  const statusLabel = {
    checking: "Checking connection",
    online: "API online",
    offline: "Connection lost",
  }[health.status];

  const statusMessage = {
    checking: "Connecting to the router API…",
    online: "The router API is responding normally.",
    offline: "The router API is not responding. We’ll retry automatically.",
  }[health.status];

  return (
    <div className="page-shell">
      <header className="topbar">
        <a className="brand" href="/" aria-label="Debian Router home">
          <span className="brand-mark" aria-hidden="true">
            <span />
            <span />
            <span />
          </span>
          <span className="brand-name">Debian Router</span>
        </a>
        <span className="local-label">LOCAL ADMINISTRATION</span>
      </header>

      <main className="main-content">
        <div className="page-heading">
          <p className="eyebrow">OVERVIEW</p>
          <h1>Router status</h1>
          <p className="page-description">
            Check whether the router API is available on this device.
          </p>
        </div>

        <section className="status-card" aria-labelledby="api-status-title">
          <div className="card-heading">
            <div>
              <p className="eyebrow">SYSTEM SERVICE</p>
              <h2 id="api-status-title">Router API</h2>
            </div>
            <div
              className={`status-badge status-badge--${health.status}`}
              role="status"
              aria-live="polite"
            >
              <span className="status-dot" aria-hidden="true" />
              <span>{statusLabel}</span>
            </div>
          </div>

          <div className={`status-message status-message--${health.status}`}>
            <span className="message-indicator" aria-hidden="true" />
            <p>{statusMessage}</p>
          </div>

          <div className="card-footer">
            <p className="last-checked">
              {health.checkedAt ? (
                <>
                  Last checked <time>{formatTime(health.checkedAt)}</time>
                </>
              ) : (
                "Waiting for the first response…"
              )}
            </p>
            <button
              className="refresh-button"
              type="button"
              onClick={() => void checkHealth()}
              disabled={isChecking}
            >
              <span aria-hidden="true">↻</span>
              {isChecking ? "Checking…" : "Check now"}
            </button>
          </div>
        </section>

        <Dashboard />
        <NetworkPortWizard />

        <StaticWANPanel />
        <PPPoEPanel />

        <p className="polling-note">
          Status refreshes automatically every {CHECK_INTERVAL_MS / 1_000}{" "}
          seconds.
        </p>
      </main>

      <footer className="page-footer">
        <span>Debian Router</span>
        <span>Local device status</span>
      </footer>
    </div>
  );
}

function NetworkPortWizard() {
  const [interfaces, setInterfaces] = useState<NetworkInterface[] | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [loadError, setLoadError] = useState(false);
  const [wanInterface, setWanInterface] = useState("");
  const [lanInterface, setLanInterface] = useState("");
  const [selectionAccepted, setSelectionAccepted] = useState(false);
  const [dhcpStatus, setDHCPStatus] = useState<NetworkDHCPStatus | null>(null);
  const [dhcpLoading, setDHCPLoading] = useState(false);
  const [dhcpError, setDHCPError] = useState(false);

  const refreshInterfaces = useCallback(async () => {
    const controller = new AbortController();
    const timeout = window.setTimeout(
      () => controller.abort(),
      NETWORK_REQUEST_TIMEOUT_MS,
    );

    try {
      const response = await fetch("/api/network/interfaces", {
        method: "GET",
        cache: "no-store",
        headers: { Accept: "application/json" },
        signal: controller.signal,
      });
      const value: unknown = await response.json();
      if (!response.ok || !isNetworkInterfaceResponse(value)) {
        throw new Error("The API returned an unexpected interface response.");
      }

      setInterfaces(value.interfaces);
      setLoadError(false);
      setWanInterface((current) =>
        value.interfaces.some(
          (networkInterface) => networkInterface.name === current,
        )
          ? current
          : "",
      );
      setLanInterface((current) =>
        value.interfaces.some(
          (networkInterface) => networkInterface.name === current,
        )
          ? current
          : "",
      );
      setSelectionAccepted(false);
      setDHCPStatus(null);
      setDHCPError(false);
    } catch {
      setLoadError(true);
      setInterfaces(null);
      setWanInterface("");
      setLanInterface("");
      setSelectionAccepted(false);
      setDHCPStatus(null);
      setDHCPError(false);
    } finally {
      window.clearTimeout(timeout);
      setIsLoading(false);
    }
  }, []);

  const refreshDHCPStatus = useCallback(async (interfaceName: string) => {
    setDHCPLoading(true);
    const controller = new AbortController();
    const timeout = window.setTimeout(
      () => controller.abort(),
      NETWORK_REQUEST_TIMEOUT_MS,
    );
    try {
      const response = await fetch(
        "/api/network/dhcp?interface=" + encodeURIComponent(interfaceName),
        {
          method: "GET",
          cache: "no-store",
          headers: { Accept: "application/json" },
          signal: controller.signal,
        },
      );
      const value: unknown = await response.json();
      if (
        !response.ok ||
        !isNetworkDHCPStatus(value) ||
        value.interface !== interfaceName
      ) {
        throw new Error("The API returned an unexpected DHCP response.");
      }
      setDHCPStatus(value);
      setDHCPError(false);
    } catch {
      setDHCPStatus(null);
      setDHCPError(true);
    } finally {
      window.clearTimeout(timeout);
      setDHCPLoading(false);
    }
  }, []);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void refreshInterfaces();
    }, 0);
    return () => window.clearTimeout(timer);
  }, [refreshInterfaces]);

  useEffect(() => {
    if (!selectionAccepted || wanInterface === "") {
      return;
    }
    const initialCheck = window.setTimeout(
      () => void refreshDHCPStatus(wanInterface),
      0,
    );
    const timer = window.setInterval(
      () => void refreshDHCPStatus(wanInterface),
      CHECK_INTERVAL_MS,
    );
    return () => {
      window.clearTimeout(initialCheck);
      window.clearInterval(timer);
    };
  }, [refreshDHCPStatus, selectionAccepted, wanInterface]);

  const selectedInterfacesAreDifferent =
    wanInterface !== "" &&
    lanInterface !== "" &&
    wanInterface !== lanInterface &&
    interfaces?.some(
      (networkInterface) => networkInterface.name === wanInterface,
    ) === true &&
    interfaces?.some(
      (networkInterface) => networkInterface.name === lanInterface,
    ) === true;

  const sameInterfaceSelected =
    wanInterface !== "" && lanInterface !== "" && wanInterface === lanInterface;

  function updateWanInterface(value: string) {
    setWanInterface(value);
    setSelectionAccepted(false);
    setDHCPStatus(null);
    setDHCPError(false);
  }

  function updateLanInterface(value: string) {
    setLanInterface(value);
    setSelectionAccepted(false);
  }

  return (
    <section className="port-card" aria-labelledby="port-wizard-title">
      <div className="port-card-heading">
        <div>
          <p className="eyebrow">NETWORK SETUP</p>
          <h2 id="port-wizard-title">Choose WAN and LAN ports</h2>
          <p className="port-description">
            Review live link information and select two different interfaces.
          </p>
        </div>
        <button
          className="refresh-button"
          type="button"
          onClick={() => {
            setIsLoading(true);
            setLoadError(false);
            void refreshInterfaces();
          }}
          disabled={isLoading}
        >
          <span aria-hidden="true">↻</span>
          {isLoading ? "Reading ports…" : "Refresh ports"}
        </button>
      </div>

      {isLoading && interfaces === null && (
        <p className="port-feedback" aria-live="polite">
          Reading network interfaces…
        </p>
      )}
      {loadError && (
        <p className="port-feedback port-feedback--error" role="alert">
          Interface status is unavailable. Refresh to try again.
        </p>
      )}
      {!isLoading && !loadError && interfaces?.length === 0 && (
        <p className="port-feedback" aria-live="polite">
          No network interfaces are available.
        </p>
      )}

      {interfaces !== null && interfaces.length > 0 && (
        <>
          <ul
            className="interface-list"
            aria-label="Available network interfaces"
          >
            {interfaces.map((networkInterface) => (
              <li className="interface-row" key={networkInterface.name}>
                <div className="interface-name">
                  <strong>{networkInterface.name}</strong>
                  <span>{networkInterface.operational_state}</span>
                </div>
                <div className="interface-link">
                  <span
                    className={`link-badge link-badge--${networkInterface.carrier === true ? "up" : networkInterface.carrier === false ? "down" : "unknown"}`}
                  >
                    {formatLinkState(networkInterface)}
                  </span>
                  <span className="interface-speed">
                    {formatLinkSpeed(networkInterface)}
                  </span>
                </div>
              </li>
            ))}
          </ul>

          <div className="port-selection">
            <label className="port-field" htmlFor="wan-interface">
              <span>WAN interface</span>
              <select
                id="wan-interface"
                value={wanInterface}
                onChange={(event) => updateWanInterface(event.target.value)}
              >
                <option value="">Select an interface</option>
                {interfaces.map((networkInterface) => (
                  <option
                    key={networkInterface.name}
                    value={networkInterface.name}
                  >
                    {networkInterface.name}
                  </option>
                ))}
              </select>
            </label>
            <label className="port-field" htmlFor="lan-interface">
              <span>LAN interface</span>
              <select
                id="lan-interface"
                value={lanInterface}
                onChange={(event) => updateLanInterface(event.target.value)}
              >
                <option value="">Select an interface</option>
                {interfaces.map((networkInterface) => (
                  <option
                    key={networkInterface.name}
                    value={networkInterface.name}
                  >
                    {networkInterface.name}
                  </option>
                ))}
              </select>
            </label>
          </div>

          {sameInterfaceSelected && (
            <p className="port-feedback port-feedback--error" role="alert">
              WAN and LAN must use different interfaces.
            </p>
          )}
          <div className="port-action-row">
            <p className="port-help">
              This confirms the selection in the page only. It does not change
              network settings.
            </p>
            <button
              className="refresh-button"
              type="button"
              disabled={!selectedInterfacesAreDifferent}
              onClick={() => setSelectionAccepted(true)}
            >
              Confirm selection
            </button>
          </div>
          {selectionAccepted && selectedInterfacesAreDifferent && (
            <>
              <p className="port-feedback port-feedback--success" role="status">
                Port selection accepted. Network settings have not been applied.
              </p>
              <section aria-labelledby="wan-dhcp-title">
                <h3 id="wan-dhcp-title">WAN DHCP status</h3>
                {dhcpLoading && dhcpStatus === null && (
                  <p className="port-feedback" role="status">
                    Reading the current WAN DHCP lease…
                  </p>
                )}
                {dhcpError && (
                  <p
                    className="port-feedback port-feedback--error"
                    role="alert"
                  >
                    WAN DHCP status is unavailable. Refresh to try again.
                  </p>
                )}
                {dhcpStatus !== null && (
                  <div aria-live="polite">
                    <p>
                      {dhcpStatus.lease_state === "leased"
                        ? "DHCP lease acquired"
                        : dhcpStatus.lease_state === "link_down"
                          ? "WAN link is down"
                          : "Waiting for a DHCP lease"}
                    </p>
                    <p>Address: {dhcpStatus.addresses.join(", ") || "—"}</p>
                    <p>Gateway: {dhcpStatus.gateway || "—"}</p>
                    <p>DNS: {dhcpStatus.dns.join(", ") || "—"}</p>
                  </div>
                )}
                <button
                  className="refresh-button"
                  type="button"
                  disabled={dhcpLoading}
                  onClick={() => void refreshDHCPStatus(wanInterface)}
                >
                  {dhcpLoading ? "Refreshing…" : "Refresh DHCP status"}
                </button>
                <p className="port-help">
                  Status is read from systemd-networkd. This page does not apply
                  or change network settings.
                </p>
              </section>
              <WANDiagnosticPanel />
            </>
          )}
        </>
      )}
    </section>
  );
}

function formatLinkState(networkInterface: NetworkInterface): string {
  if (networkInterface.carrier === true) {
    return "Link detected";
  }
  if (networkInterface.carrier === false) {
    return "No link";
  }
  return "Link unknown";
}

function formatLinkSpeed(networkInterface: NetworkInterface): string {
  if (networkInterface.speed_mbps !== null) {
    return `${networkInterface.speed_mbps} Mbps`;
  }
  return networkInterface.carrier === false
    ? "No negotiated speed"
    : "Speed unknown";
}
