import { useCallback, useEffect, useState } from "react";

const CHECK_INTERVAL_MS = 5_000;
const REQUEST_TIMEOUT_MS = 4_000;

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
