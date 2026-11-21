import { useEffect, useState, type FormEvent } from "react";
import type { FirewallTransaction } from "./FirewallRulesPanel";

const endpoint = "/api/network/routing";
const emptyIPv6 = { wan_address: "", wan_gateway: "", lan_prefix: "" };

type Route = { destination: string; gateway: string };
type Config = { routes: Route[]; ipv6: typeof emptyIPv6 | null };
type Status = { config: Config; ipv6_active: boolean };
type Row = Route & { id: number };

async function request<T>(config?: Config): Promise<T> {
  const response = await fetch(endpoint, {
    cache: "no-store",
    signal: AbortSignal.timeout(20_000),
    ...(config && {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(config),
    }),
  });
  const data = await response.json();
  if (!response.ok) {
    const errors: Record<string, string> = {
      invalid_routing:
        "Check the destination prefixes and gateways. Use a separate /64 for LAN IPv6.",
      routing_overlap: "The route or IPv6 prefix overlaps a connected network.",
      routing_gateway_unreachable:
        "Each IPv4 next hop must be directly reachable on the active WAN.",
      routing_requires_wan:
        "Static IPv6 requires the active physical WAN port.",
      routing_resources_in_use:
        "The reserved IPv6 routing resources are already in use.",
      network_change_pending: "Finish the current network change first.",
    };
    throw new Error(errors[data.error] ?? "Routing settings are unavailable.");
  }
  return data;
}

export default function RoutingPanel({
  readOnly,
  onPending,
}: {
  readOnly: boolean;
  onPending(value: FirewallTransaction): void;
}) {
  const [routes, setRoutes] = useState<Row[]>();
  const [ipv6, setIPv6] = useState(emptyIPv6);
  const [enabled, setEnabled] = useState(false);
  const [active, setActive] = useState(false);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");

  useEffect(() => {
    let current = true;
    void request<Status>()
      .then(({ config, ipv6_active }) => {
        if (!current) return;
        setRoutes(config.routes.map((route, id) => ({ ...route, id })));
        setIPv6(config.ipv6 ?? emptyIPv6);
        setEnabled(config.ipv6 !== null);
        setActive(ipv6_active);
      })
      .catch(() => {
        if (current) setMessage("Could not load routing settings.");
      });
    return () => {
      current = false;
    };
  }, []);

  function editRoute(row: Row, field: keyof Route, value: string) {
    setRoutes((current) =>
      current?.map((route) =>
        route.id === row.id ? { ...route, [field]: value } : route,
      ),
    );
  }

  async function apply(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!routes) return;
    setBusy(true);
    setMessage("");
    try {
      onPending(
        await request<FirewallTransaction>({
          routes: routes.map(({ destination, gateway }) => ({
            destination: destination.trim(),
            gateway: gateway.trim(),
          })),
          ipv6: enabled
            ? {
                wan_address: ipv6.wan_address.trim(),
                wan_gateway: ipv6.wan_gateway.trim(),
                lan_prefix: ipv6.lan_prefix.trim(),
              }
            : null,
        }),
      );
    } catch (error) {
      setMessage(
        error instanceof Error
          ? error.message
          : "Could not apply routing settings.",
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <section aria-label="Static routes and IPv6">
      <h4>Static routes and IPv6</h4>
      {!routes && !message && <p role="status">Loading routing settings…</p>}
      {routes && (
        <form onSubmit={(event) => void apply(event)}>
          <fieldset disabled={readOnly || busy}>
            <legend>LAN routes via WAN</legend>
            <p>Enter a destination prefix and a next hop on the WAN link.</p>
            {routes.map((route, index) => (
              <fieldset key={route.id}>
                <legend>Static route {index + 1}</legend>
                <div className="static-wan-fields">
                  <label className="static-wan-field">
                    Destination prefix
                    <input
                      required
                      value={route.destination}
                      onChange={(event) =>
                        editRoute(route, "destination", event.target.value)
                      }
                    />
                  </label>
                  <label className="static-wan-field">
                    Next hop
                    <input
                      required
                      value={route.gateway}
                      onChange={(event) =>
                        editRoute(route, "gateway", event.target.value)
                      }
                    />
                  </label>
                </div>
                <button
                  type="button"
                  className="danger-button"
                  onClick={() =>
                    setRoutes(routes.filter((item) => item.id !== route.id))
                  }
                >
                  Remove route
                </button>
              </fieldset>
            ))}
            <button
              type="button"
              className="refresh-button"
              disabled={routes.length >= 24}
              onClick={() =>
                setRoutes([
                  ...routes,
                  {
                    id: Math.max(-1, ...routes.map((route) => route.id)) + 1,
                    destination: "",
                    gateway: "",
                  },
                ])
              }
            >
              Add static route
            </button>
          </fieldset>
          <fieldset disabled={readOnly || busy}>
            <legend>Routed IPv6</legend>
            <label>
              <input
                type="checkbox"
                checked={enabled}
                onChange={(event) => setEnabled(event.target.checked)}
              />
              Enable static IPv6
            </label>
            {enabled && (
              <div className="static-wan-fields">
                <label className="static-wan-field">
                  WAN IPv6 address / prefix
                  <input
                    required
                    value={ipv6.wan_address}
                    onChange={(event) =>
                      setIPv6({ ...ipv6, wan_address: event.target.value })
                    }
                  />
                </label>
                <label className="static-wan-field">
                  WAN IPv6 next hop
                  <input
                    required
                    value={ipv6.wan_gateway}
                    onChange={(event) =>
                      setIPv6({ ...ipv6, wan_gateway: event.target.value })
                    }
                  />
                </label>
                <label className="static-wan-field">
                  Routed LAN IPv6 prefix /64
                  <input
                    required
                    value={ipv6.lan_prefix}
                    onChange={(event) =>
                      setIPv6({ ...ipv6, lan_prefix: event.target.value })
                    }
                  />
                </label>
              </div>
            )}
            <p>
              Provide a LAN prefix routed to this B5 by the upstream router. LAN
              clients use SLAAC.
            </p>
          </fieldset>
          {active && <p>IPv6 forwarding is active.</p>}
          <button
            type="submit"
            className="primary-button"
            disabled={readOnly || busy}
          >
            Apply routing temporarily
          </button>
        </form>
      )}
      {message && (
        <p className="static-wan-feedback" role="status">
          {message}
        </p>
      )}
    </section>
  );
}
