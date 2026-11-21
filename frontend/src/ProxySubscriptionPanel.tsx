import { useEffect, useState, type FormEvent } from "react";
import ProxyRoutingPanel from "./ProxyRoutingPanel";

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
  subscription_changed:
    "The saved subscription changed. Reload before checking.",
  subscription_busy: "Another proxy operation is in progress.",
  server_not_found: "The selected entry is no longer available.",
  server_dns_failed: "The router could not resolve the server address.",
  nonpublic_destination: "The server must resolve to a public address.",
  unsupported_options: "The selected entry requires unsupported options.",
  worker_unavailable: "The sing-box check could not start.",
  config_rejected: "sing-box rejected the test configuration.",
  worker_start_failed: "The temporary proxy did not become ready.",
  proxy_request_failed: "The HTTPS request through this server failed.",
  unexpected_http_status: "The test destination returned an unexpected status.",
  timed_out: "The check timed out.",
  canceled: "The check was canceled.",
};

type Report = {
  id?: string;
  imported: number;
  rejected: number;
  rows: { line: number; protocol?: string; error?: string; warning?: string }[];
};

type CheckResult = {
  status: "passed" | "failed";
  reason?: string;
  elapsed_ms: number;
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
  const [checking, setChecking] = useState<number | null>(null);
  const [checks, setChecks] = useState<Record<number, string>>({});

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
        setChecks({});
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

  async function checkServer(line: number) {
    setChecking(line);
    setChecks((previous) => ({ ...previous, [line]: "Checking…" }));
    try {
      const response = await fetch(`${endpoint}/check`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ line, subscription_id: report?.id ?? "" }),
      });
      const result = await response.json();
      const message = response.ok
        ? describeCheck(result as CheckResult)
        : (reasons[result.error as string] ?? "Unable to check this server.");
      setChecks((previous) => ({ ...previous, [line]: message }));
    } catch {
      setChecks((previous) => ({
        ...previous,
        [line]: "Unable to check this server.",
      }));
    } finally {
      setChecking(null);
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
            disabled={
              busy || loading || checking !== null || source.trim() === ""
            }
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
                {row.error ? (
                  (reasons[row.error] ?? "Invalid entry.")
                ) : (
                  <span aria-live="polite">
                    {checks[row.line] ?? "Connectivity unchecked."}
                  </span>
                )}
                {row.warning &&
                  ` ${reasons[row.warning] ?? "Review this entry before use."}`}
                {!row.error && (
                  <>
                    {" "}
                    <button
                      className="refresh-button"
                      type="button"
                      aria-label={`Check server on line ${row.line}`}
                      disabled={busy || checking !== null}
                      onClick={() => void checkServer(row.line)}
                    >
                      Check server
                    </button>
                  </>
                )}
              </li>
            ))}
          </ul>
        </div>
      )}
      <ProxyRoutingPanel subscriptionID={report?.id ?? ""} />
    </section>
  );
}

function describeCheck(result: CheckResult): string {
  if (result.status === "passed") {
    return `HTTPS check passed in ${result.elapsed_ms} ms.`;
  }
  return reasons[result.reason ?? ""] ?? "The HTTPS check failed.";
}
