import { useEffect, useState, type FormEvent } from "react";

type Reservation = { mac: string; address: string };
type List = { managed: boolean; entries: Reservation[] };

const endpoint = "/api/network/lan/reservations";
const empty = { mac: "", address: "" };

async function loadReservations(): Promise<List> {
  const response = await fetch(endpoint, {
    cache: "no-store",
    signal: AbortSignal.timeout(8_000),
  });
  if (!response.ok) throw new Error("Could not load DHCP reservations.");
  return response.json();
}

export default function DHCPReservationsPanel() {
  const [list, setList] = useState<List>();
  const [form, setForm] = useState<Reservation>(empty);
  const [editing, setEditing] = useState<string>();
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");

  useEffect(() => {
    let active = true;
    void loadReservations()
      .then((value) => {
        if (active) setList(value);
      })
      .catch(() => {
        if (active) setMessage("Could not load DHCP reservations.");
      });
    return () => {
      active = false;
    };
  }, []);

  async function refresh() {
    setBusy(true);
    setMessage("");
    try {
      setList(await loadReservations());
    } catch {
      setMessage("Could not load DHCP reservations.");
    } finally {
      setBusy(false);
    }
  }

  async function change(
    method: "POST" | "PATCH" | "DELETE",
    previous?: string,
  ) {
    setBusy(true);
    setMessage("");
    try {
      const response = await fetch(endpoint, {
        method,
        signal: AbortSignal.timeout(18_000),
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          previous_mac: previous,
          reservation:
            method === "DELETE"
              ? undefined
              : { mac: form.mac.trim(), address: form.address.trim() },
        }),
      });
      if (!response.ok) {
        const { error } = await response.json();
        const messages: Record<string, string> = {
          invalid_reservation:
            "Enter a unicast Ethernet MAC and a usable IPv4 address in the LAN subnet.",
          reservation_conflict:
            "Another reservation already uses this MAC or IPv4 address.",
          reservation_missing:
            "This reservation was removed. Refresh the list.",
          reservation_limit: "The reservation list is full.",
          network_change_pending: "Finish the pending LAN change first.",
        };
        throw new Error(
          messages[error] ?? "Could not update DHCP reservations.",
        );
      }
      setForm(empty);
      setEditing(undefined);
      setList(undefined);
      setList(await loadReservations());
      setMessage(
        method === "DELETE" ? "Reservation removed." : "Reservation saved.",
      );
    } catch (error) {
      setMessage(
        error instanceof Error
          ? error.message
          : "Could not update DHCP reservations.",
      );
    } finally {
      setBusy(false);
    }
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    void change(editing ? "PATCH" : "POST", editing);
  }

  const disabled = busy || !list?.managed;
  return (
    <section className="static-wan-form" aria-label="DHCP reservations">
      <h3>DHCP reservations</h3>
      <p className="static-wan-note">
        {list?.managed === false
          ? "Apply and confirm a LAN profile before managing reservations."
          : "Renew the client’s DHCP lease after changing its reservation."}
      </p>
      <button
        className="refresh-button"
        type="button"
        disabled={busy}
        onClick={() => void refresh()}
      >
        Refresh reservations
      </button>
      {list && list.entries.length === 0 && <p>No reservations.</p>}
      {list && list.entries.length > 0 && (
        <ul>
          {list.entries.map((entry) => (
            <li key={entry.mac}>
              {entry.mac} · {entry.address}{" "}
              <button
                type="button"
                disabled={disabled}
                aria-label={`Edit ${entry.mac}`}
                onClick={() => {
                  setEditing(entry.mac);
                  setForm(entry);
                }}
              >
                Edit
              </button>{" "}
              <button
                type="button"
                disabled={disabled}
                aria-label={`Delete ${entry.mac}`}
                onClick={() => void change("DELETE", entry.mac)}
              >
                Delete
              </button>
            </li>
          ))}
        </ul>
      )}
      <form onSubmit={submit}>
        <div className="static-wan-fields">
          <label className="static-wan-field">
            Client MAC address
            <input
              required
              autoComplete="off"
              disabled={disabled}
              value={form.mac}
              onChange={(event) =>
                setForm({ ...form, mac: event.target.value })
              }
            />
          </label>
          <label className="static-wan-field">
            Reserved IPv4 address
            <input
              required
              autoComplete="off"
              disabled={disabled}
              value={form.address}
              onChange={(event) =>
                setForm({ ...form, address: event.target.value })
              }
            />
          </label>
        </div>
        <div className="static-wan-actions">
          <button className="primary-button" type="submit" disabled={disabled}>
            {editing ? "Save reservation" : "Add reservation"}
          </button>
          {editing && (
            <button
              type="button"
              disabled={busy}
              onClick={() => {
                setForm(empty);
                setEditing(undefined);
              }}
            >
              Cancel edit
            </button>
          )}
        </div>
      </form>
      {message && (
        <p className="static-wan-feedback" role="status">
          {message}
        </p>
      )}
    </section>
  );
}
