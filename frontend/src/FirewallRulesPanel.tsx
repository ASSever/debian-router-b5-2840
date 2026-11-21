import { useEffect, useState, type FormEvent } from "react";

const endpoint = "/api/network/firewall/rules";
const maxEntries = 32;

export type FirewallTransaction = {
  transaction_id: string;
  expires_at: string;
};
type Forward = {
  protocol: "tcp" | "udp";
  wan_port: number;
  lan_address: string;
  lan_port: number;
};
type RuleSettings = { forwards: Forward[]; allow: string[]; block: string[] };
type Draft = {
  forwards: (Forward & { id: number })[];
  allow: string;
  block: string;
};
type Counters = Record<string, { packets: string; bytes: string }>;
type Status = { rules: RuleSettings; counters: Counters | null };

async function request<T>(settings?: RuleSettings): Promise<T> {
  const response = await fetch(endpoint, {
    cache: "no-store",
    signal: AbortSignal.timeout(20_000),
    ...(settings && {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(settings),
    }),
  });
  const data = await response.json();
  if (response.ok) return data;
  if (data.error === "invalid_firewall_rules") {
    throw new Error(
      "Use unique WAN ports per protocol, valid LAN host addresses and IPv4 address lists with up to 32 entries each.",
    );
  }
  if (data.error === "network_change_pending") {
    throw new Error("Finish the current network change first.");
  }
  throw new Error("Firewall rules are unavailable.");
}

function Counter({
  counters,
  name,
}: {
  counters: Counters | null;
  name: string;
}) {
  const value = counters?.[name];
  return (
    <span>
      {value ? `${value.packets} packets · ${value.bytes} bytes` : "—"}
    </span>
  );
}

export default function FirewallRulesPanel({
  pending,
  readOnly,
  onPending,
}: {
  pending?: string;
  readOnly: boolean;
  onPending(value: FirewallTransaction): void;
}) {
  const [draft, setDraft] = useState<Draft>();
  const [counters, setCounters] = useState<Counters | null>(null);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let active = true;
    void request<Status>()
      .then(({ rules, counters }) => {
        if (!active) return;
        setDraft({
          forwards: rules.forwards.map((forward, id) => ({ ...forward, id })),
          allow: rules.allow.join("\n"),
          block: rules.block.join("\n"),
        });
        setCounters(counters);
      })
      .catch(() => {
        if (active) {
          setDraft(undefined);
          setCounters(null);
          setMessage("Could not load firewall rules.");
        }
      });
    return () => {
      active = false;
    };
  }, [pending]);

  function editForward(id: number, fields: Partial<Forward>) {
    if (draft)
      setDraft({
        ...draft,
        forwards: draft.forwards.map((forward) =>
          forward.id === id ? { ...forward, ...fields } : forward,
        ),
      });
  }

  async function refreshCounters() {
    setBusy(true);
    setMessage("");
    try {
      setCounters((await request<Status>()).counters);
    } catch {
      setCounters(null);
      setMessage("Could not refresh rule counters.");
    } finally {
      setBusy(false);
    }
  }

  async function apply(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!draft) return;
    setBusy(true);
    setMessage("");
    try {
      const split = (list: string) => list.trim().split(/\s+/).filter(Boolean);
      onPending(
        await request<FirewallTransaction>({
          forwards: draft.forwards.map(
            ({ protocol, wan_port, lan_address, lan_port }) => ({
              protocol,
              wan_port,
              lan_address: lan_address.trim(),
              lan_port,
            }),
          ),
          allow: split(draft.allow),
          block: split(draft.block),
        }),
      );
    } catch (error) {
      setMessage(
        error instanceof Error ? error.message : "Could not apply rules.",
      );
    } finally {
      setBusy(false);
    }
  }

  const operationDisabled = busy || readOnly;
  return (
    <section aria-label="Port forwarding and source lists">
      <h4>Port forwarding</h4>
      {draft && (
        <form onSubmit={(event) => void apply(event)}>
          {draft.forwards.map((forward, index) => (
            <fieldset disabled={operationDisabled} key={forward.id}>
              <legend>Port forward {index + 1}</legend>
              <div className="static-wan-fields">
                <label className="static-wan-field">
                  Protocol
                  <select
                    value={forward.protocol}
                    onChange={(event) =>
                      editForward(forward.id, {
                        protocol: event.target.value === "udp" ? "udp" : "tcp",
                      })
                    }
                  >
                    <option value="tcp">TCP</option>
                    <option value="udp">UDP</option>
                  </select>
                </label>
                <label className="static-wan-field">
                  WAN port
                  <input
                    type="number"
                    required
                    min={1}
                    max={65535}
                    value={forward.wan_port || ""}
                    onChange={(event) =>
                      editForward(forward.id, {
                        wan_port: Number(event.target.value),
                      })
                    }
                  />
                </label>
                <label className="static-wan-field">
                  LAN IPv4 address
                  <input
                    required
                    inputMode="decimal"
                    value={forward.lan_address}
                    onChange={(event) =>
                      editForward(forward.id, {
                        lan_address: event.target.value,
                      })
                    }
                  />
                </label>
                <label className="static-wan-field">
                  LAN port
                  <input
                    type="number"
                    required
                    min={1}
                    max={65535}
                    value={forward.lan_port || ""}
                    onChange={(event) =>
                      editForward(forward.id, {
                        lan_port: Number(event.target.value),
                      })
                    }
                  />
                </label>
              </div>
              <div className="static-wan-actions">
                <Counter
                  counters={counters}
                  name={`forward_${forward.protocol}_${forward.wan_port}`}
                />
                <button
                  type="button"
                  className="danger-button"
                  onClick={() =>
                    setDraft({
                      ...draft,
                      forwards: draft.forwards.filter(
                        (row) => row.id !== forward.id,
                      ),
                    })
                  }
                >
                  Remove
                </button>
              </div>
            </fieldset>
          ))}
          <button
            type="button"
            className="refresh-button"
            disabled={operationDisabled || draft.forwards.length >= maxEntries}
            onClick={() =>
              setDraft({
                ...draft,
                forwards: [
                  ...draft.forwards,
                  {
                    id:
                      Math.max(-1, ...draft.forwards.map((row) => row.id)) + 1,
                    protocol: "tcp",
                    wan_port: 0,
                    lan_address: "",
                    lan_port: 0,
                  },
                ],
              })
            }
          >
            Add port forward
          </button>
          <h4>WAN source address lists</h4>
          <p>
            Enter one IPv4 address or CIDR per line. Blocked sources take
            priority. Leaving the allow list empty permits all sources outside
            the block list.
          </p>
          <div className="static-wan-fields">
            <label className="static-wan-field">
              Allow
              <textarea
                rows={3}
                disabled={operationDisabled}
                value={draft.allow}
                onChange={(event) =>
                  setDraft({ ...draft, allow: event.target.value })
                }
              />
            </label>
            <label className="static-wan-field">
              Block
              <textarea
                rows={3}
                disabled={operationDisabled}
                value={draft.block}
                onChange={(event) =>
                  setDraft({ ...draft, block: event.target.value })
                }
              />
            </label>
          </div>
          <ul>
            <li>
              Matched allow list ·{" "}
              <Counter counters={counters} name="source_allowed" />
            </li>
            <li>
              Matched block list ·{" "}
              <Counter counters={counters} name="source_blocked" />
            </li>
            <li>
              Outside allow list ·{" "}
              <Counter counters={counters} name="source_not_allowed" />
            </li>
          </ul>
          <p>Counters reset when the firewall table is reloaded.</p>
          <div className="static-wan-actions">
            <button
              type="submit"
              className="primary-button"
              disabled={operationDisabled}
            >
              Apply firewall rules temporarily
            </button>
            <button
              type="button"
              className="refresh-button"
              disabled={busy}
              onClick={() => void refreshCounters()}
            >
              Refresh counters
            </button>
          </div>
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
