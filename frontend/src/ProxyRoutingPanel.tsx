import { useState, type FormEvent } from "react";

const endpoint = "/api/proxy/profile";
const errors: Record<string, string> = {
  invalid_proxy_profile:
    "The profile contains invalid groups, rules, or destinations.",
  server_not_found: "A group references a server line that is unavailable.",
  subscription_changed: "Reload the subscription before applying the profile.",
  subscription_busy: "Another proxy operation is in progress.",
  proxy_config_rejected: "sing-box rejected the profile during validation.",
  proxy_apply_rolled_back:
    "Startup failed. The previous proxy profile was restored.",
  proxy_rollback_failed:
    "The previous profile could not be restarted. Recovery is pending.",
};

export default function ProxyRoutingPanel({
  subscriptionID,
}: {
  subscriptionID: string;
}) {
  const [editor, setEditor] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [applied, setApplied] = useState(false);

  async function openProfile() {
    setBusy(true);
    setError("");
    try {
      const result = await fetch(endpoint, { cache: "no-store" });
      if (!result.ok) throw new Error();
      const { default: target, groups, rules } = await result.json();
      setEditor(JSON.stringify({ default: target, groups, rules }, null, 2));
    } catch {
      setError("The current proxy profile is unavailable.");
    } finally {
      setBusy(false);
    }
  }

  async function apply(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError("");
    setApplied(false);
    let profile: unknown;
    try {
      profile = JSON.parse(editor ?? "");
      if (
        typeof profile !== "object" ||
        profile === null ||
        Array.isArray(profile)
      )
        throw new Error();
    } catch {
      setError("Enter a valid JSON profile.");
      return;
    }
    setBusy(true);
    try {
      const response = await fetch(endpoint, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ ...profile, subscription_id: subscriptionID }),
      });
      if (!response.ok) {
        const result = await response.json();
        setError(
          errors[result.error as string] ??
            "Unable to apply the proxy profile.",
        );
        return;
      }
      setApplied(true);
    } catch {
      setError("Unable to apply the proxy profile.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="static-wan-form" aria-labelledby="proxy-routing-title">
      <h3 id="proxy-routing-title">Proxy rules and groups</h3>
      {editor === null ? (
        <div className="static-wan-actions">
          <button
            className="refresh-button"
            type="button"
            disabled={busy}
            onClick={() => void openProfile()}
          >
            Edit proxy profile
          </button>
        </div>
      ) : (
        <form className="static-wan-form" onSubmit={apply}>
          <p className="static-wan-note">
            Rules are evaluated in order. Use <code>direct</code>,{" "}
            <code>block</code>, or <code>group:name</code> as a target. A group
            selects one member by its subscription line number.
          </p>
          <label className="static-wan-field">
            Proxy profile (JSON)
            <textarea
              rows={12}
              value={editor}
              onChange={(event) => {
                setEditor(event.target.value);
                setApplied(false);
              }}
              disabled={busy}
              required
              maxLength={8192}
              spellCheck={false}
            />
          </label>
          <div className="static-wan-actions">
            <button className="primary-button" type="submit" disabled={busy}>
              {busy ? "Applying…" : "Apply proxy profile"}
            </button>
          </div>
        </form>
      )}
      {error && (
        <p
          className="static-wan-feedback static-wan-feedback--error"
          role="alert"
        >
          {error}
        </p>
      )}
      {applied && (
        <p className="static-wan-feedback" role="status">
          Proxy profile applied.
        </p>
      )}
    </section>
  );
}
