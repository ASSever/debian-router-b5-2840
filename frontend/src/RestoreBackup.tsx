import { useState, type FormEvent } from "react";

export default function RestoreBackup({
  onRestored,
}: {
  onRestored: (message: string) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const restore = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const archive = new FormData(event.currentTarget).get("archive");
    if (!(archive instanceof File) || archive.size === 0) return;
    if (archive.size > 32 * 1024 * 1024) {
      setError("The ZIP exceeds 32 MiB.");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const response = await fetch("/api/backup/restore", {
        method: "POST",
        headers: { "Content-Type": "application/zip" },
        body: archive,
        signal: AbortSignal.timeout(150_000),
      });
      if (response.status === 400) {
        setError("This backup failed ZIP, checksum, or SQLite validation.");
      } else if (response.status === 409) {
        setError("Another change is in progress. Wait for it to finish.");
      } else if (response.status === 401 || response.status === 403) {
        setError("Sign in as administrator before restoring a backup.");
      } else if (!response.ok) {
        setError(
          "The restore did not finish. Check the router before retrying.",
        );
      } else {
        const result: { network?: boolean } = await response.json();
        const networkMessage = result.network
          ? " Network changes take effect after a locally supervised reboot."
          : "";
        onRestored(
          response.status === 202
            ? "Settings were saved. The server will finish recovery when the API restarts."
            : "Backup restored. Sign in with the current administrator password." +
                networkMessage,
        );
      }
    } catch {
      setError(
        "The connection was interrupted. Check the router before retrying.",
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <details>
      <summary>Restore from backup</summary>
      <form
        className="dashboard-login"
        onSubmit={(event) => void restore(event)}
      >
        <p>
          Files are restored on disk. Running services load them at their next
          start.
        </p>
        <label>
          Backup ZIP
          <input
            type="file"
            name="archive"
            accept=".zip,application/zip"
            required
          />
        </label>
        <label>
          <input type="checkbox" required />
          Replace settings and sign out all sessions
        </label>
        <button className="refresh-button" type="submit" disabled={busy}>
          {busy ? "Restoring…" : "Restore backup"}
        </button>
        {error && <p role="alert">{error}</p>}
      </form>
    </details>
  );
}
