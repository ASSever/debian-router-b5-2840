import { useState } from "react";

type Report = {
  interface: string;
  address?: string;
  gateway?: string;
  resolver?: string;
  result: string;
  checks: { name: string; status: string }[];
};

const results: Record<string, string> = {
  online: "WAN checks passed",
  link_down: "WAN link is down",
  link_unknown: "WAN link state is unavailable",
  no_address: "WAN has no usable IPv4 address",
  gateway_failed: "WAN gateway is missing or unreachable",
  gateway_unverified: "WAN gateway could not be verified",
  dns_failed: "WAN DNS resolution failed",
  external_failed: "External connectivity checks failed",
};

const labels: Record<string, string> = {
  link: "Link",
  address: "IPv4 address",
  gateway: "Gateway",
  dns: "DNS",
  external: "External TCP connectivity",
};

const outcomes: Record<string, string> = {
  ok: "Passed",
  failed: "Failed",
  unverified: "Unverified",
};

export default function WANDiagnosticPanel() {
  const [report, setReport] = useState<Report>();
  const [message, setMessage] = useState("");
  const [running, setRunning] = useState(false);

  async function run() {
    setReport(undefined);
    setMessage("");
    setRunning(true);
    try {
      const response = await fetch("/api/network/wan/diagnostic", {
        method: "POST",
        signal: AbortSignal.timeout(15_000),
      });
      if (!response.ok) {
        const message =
          response.status === 401 || response.status === 403
            ? "Sign in with an active administrator account to run WAN diagnostics."
            : response.status === 409
              ? "A WAN check is already running. Try again when it finishes."
              : "WAN diagnostics are unavailable.";
        throw new Error(message);
      }
      setReport(await response.json());
    } catch (error) {
      setMessage(
        error instanceof Error
          ? error.message
          : "WAN diagnostics are unavailable.",
      );
    } finally {
      setRunning(false);
    }
  }

  return (
    <section aria-label="WAN diagnostics">
      <h3>WAN diagnostics</h3>
      <p>Checks the configured IPv4 Ethernet WAN.</p>
      <button
        className="refresh-button"
        type="button"
        disabled={running}
        onClick={() => void run()}
      >
        {running ? "Checking WAN…" : "Run WAN diagnostics"}
      </button>
      <div aria-live="polite">
        {message && <p>{message}</p>}
        {report && (
          <>
            <p>
              <strong>{results[report.result] ?? "WAN checks finished"}</strong>
            </p>
            <p>
              Interface {report.interface}
              {report.address && ` · ${report.address}`}
            </p>
            <ul>
              {report.checks.map(({ name, status }) => (
                <li key={name}>
                  {labels[name]}
                  {name === "gateway" &&
                    report.gateway &&
                    ` (${report.gateway})`}
                  {name === "dns" && report.resolver && ` (${report.resolver})`}{" "}
                  · {outcomes[status]}
                </li>
              ))}
            </ul>
          </>
        )}
      </div>
    </section>
  );
}
