import { useEffect, useState, type FormEvent } from "react";

const endpoint = "/api/proxy/subscription";
const reasons: Record<string, string> = {
  invalid_subscription:
    "Enter a list of server links or a Base64 subscription.",
  invalid_subscription_url: "Use an HTTPS subscription URL on a public host.",
  subscription_fetch_failed: "The subscription could not be downloaded.",
  subscription_too_large: "The subscription exceeds the size or entry limit.",
  subscription_import_in_progress: "Another subscription import is running.",
  subscription_storage_failed:
    "The router could not access the saved subscription.",
  unauthorized: "Sign in again to import a subscription.",
  invalid_server_uri: "Invalid server link.",
  invalid_server_port: "Invalid server port.",
  invalid_server_credentials: "Invalid or missing server credential.",
  invalid_server_options: "Invalid server options.",
  unsupported_server_options: "Unsupported server options.",
  unsupported_protocol: "Unsupported protocol.",
  line_too_long: "Server link is too long.",
  provider_options_unverified:
    "Provider-specific options require review before use.",
};

type Report = {
  imported: number;
  rejected: number;
  rows: { line: number; protocol?: string; error?: string; warning?: string }[];
};

const protocolNames: Record<string, string> = {
  vless: "VLESS",
  hysteria2: "Hysteria2",
};

export default function ProxySubscriptionPanel() {
  const [source, setSource] = useState("");
  const [report, setReport] = useState<Report | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const controller = new AbortController();
    void fetch(endpoint, { cache: "no-store", signal: controller.signal })
      .then(async (response) => {
        if (!response.ok) throw new Error();
        const saved = (await response.json()) as Report;
        if (!controller.signal.aborted) setReport(saved);
      })
      .catch(() => {
        if (!controller.signal.aborted)
          setError("Saved subscription is unavailable.");
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, []);

  async function importSubscription(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError("");
    setSource("");
    try {
      const response = await fetch(endpoint, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ source }),
        cache: "no-store",
      });
      const result = await response.json();
      if (response.ok || response.status === 422) {
        setReport(result as Report);
        if (response.status === 422) {
          setError(
            "No entries imported. Any previously saved subscription was kept.",
          );
        }
      } else {
        setError(
          reasons[result.error as string] ??
            "Unable to import the subscription.",
        );
      }
    } catch {
      setError("Unable to import the subscription.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section
      className="static-wan-form"
      aria-labelledby="proxy-subscription-title"
    >
      <h3 id="proxy-subscription-title">Proxy subscription</h3>
      <form className="static-wan-form" onSubmit={importSubscription}>
        <label className="static-wan-field">
          Subscription URL or contents
          <textarea
            value={source}
            onChange={(event) => setSource(event.target.value)}
            rows={3}
            maxLength={262144}
            autoComplete="off"
            spellCheck={false}
            autoCapitalize="none"
            disabled={busy}
            required
          />
        </label>
        <p className="static-wan-note">
          Paste a public HTTPS URL, a list of server links, or Base64 text. An
          import with valid entries replaces the saved subscription.
        </p>
        <div className="static-wan-actions">
          <button
            className="primary-button"
            type="submit"
            disabled={busy || loading || source.trim() === ""}
          >
            {busy ? "Importing…" : "Import subscription"}
          </button>
        </div>
      </form>
      {error && (
        <p
          role="alert"
          className="static-wan-feedback static-wan-feedback--error"
        >
          {error}
        </p>
      )}
      {report && report.rows.length > 0 && (
        <div>
          <p role="status">
            {report.imported} imported · {report.rejected} rejected
          </p>
          <ul
            aria-label="Subscription import results"
            className="interface-list"
          >
            {report.rows.map((row) => (
              <li key={row.line}>
                Line {row.line}
                {row.protocol ? ` · ${protocolNames[row.protocol]}` : ""}
                {" · "}
                {row.error
                  ? (reasons[row.error] ?? "Invalid entry.")
                  : "Connectivity unchecked."}
                {row.warning &&
                  ` ${reasons[row.warning] ?? "Review this entry before use."}`}
              </li>
            ))}
          </ul>
        </div>
      )}
    </section>
  );
}
