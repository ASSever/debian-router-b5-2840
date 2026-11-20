import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import StaticWANPanel from "./StaticWANPanel";

function response(status: number, value?: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => value,
  } as Response;
}

function dhcpResponse(): Response {
  return response(200, {
    interface: "enp1s0",
    link_state: "carrier",
    lease_state: "leased",
    addresses: ["192.168.1.104/24"],
    gateway: "192.168.1.1",
    dns: ["192.168.1.1", "1.1.1.1"],
  });
}

describe("Static WAN controls", () => {
  const fetchMock = vi.fn<typeof fetch>();

  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    fetchMock.mockReset();
  });

  it("signs in, pre-fills the current WAN lease, applies settings, and restores them", async () => {
    const expiry = new Date(Date.now() + 120_000).toISOString();
    fetchMock.mockImplementation((input, init) => {
      if (input === "/api/auth/session" && init?.method === "GET") {
        return Promise.resolve(response(401, { error: "unauthorized" }));
      }
      if (input === "/api/network/dhcp?interface=enp1s0") {
        return Promise.resolve(dhcpResponse());
      }
      if (input === "/api/auth/login") {
        return Promise.resolve(response(200, { must_change_password: false }));
      }
      if (input === "/api/network/wan/static") {
        return Promise.resolve(
          response(202, {
            transaction_id: "0123456789abcdef0123456789abcdef",
            expires_at: expiry,
          }),
        );
      }
      if (input === "/api/network/wan/static/rollback") {
        return Promise.resolve(response(200, { status: "rolled_back" }));
      }
      return Promise.resolve(response(404, { error: "not_found" }));
    });

    render(<StaticWANPanel />);
    fireEvent.change(await screen.findByLabelText("Administrator password"), {
      target: { value: "test-admin-password" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }));

    const address = await screen.findByLabelText("IPv4 address / prefix");
    await waitFor(() => {
      expect(address).toHaveValue("192.168.1.104/24");
      expect(screen.getByLabelText("Gateway")).toHaveValue("192.168.1.1");
      expect(
        screen.getByLabelText("DNS servers, separated by commas"),
      ).toHaveValue("192.168.1.1, 1.1.1.1");
    });

    fireEvent.click(screen.getByRole("button", { name: "Apply temporarily" }));
    expect(
      await screen.findByText(/Automatic rollback in/),
    ).toBeInTheDocument();
    const applyCall = fetchMock.mock.calls.find(
      ([input]) => input === "/api/network/wan/static",
    );
    expect(applyCall?.[1]?.body).toBe(
      JSON.stringify({
        address: "192.168.1.104/24",
        gateway: "192.168.1.1",
        dns: ["192.168.1.1", "1.1.1.1"],
      }),
    );

    fireEvent.click(
      screen.getByRole("button", { name: "Restore previous settings" }),
    );
    expect(
      await screen.findByText("The previous WAN configuration was restored."),
    ).toBeInTheDocument();
  });

  it("explains an invalid gateway and does not start a rollback transaction", async () => {
    fetchMock.mockImplementation((input, init) => {
      if (input === "/api/auth/session" && init?.method === "GET") {
        return Promise.resolve(
          response(200, { authenticated: true, must_change_password: false }),
        );
      }
      if (input === "/api/network/dhcp?interface=enp1s0") {
        return Promise.resolve(dhcpResponse());
      }
      if (input === "/api/network/wan/static") {
        return Promise.resolve(
          response(400, { error: "invalid_static_gateway" }),
        );
      }
      return Promise.resolve(response(404, { error: "not_found" }));
    });

    render(<StaticWANPanel />);
    fireEvent.change(await screen.findByLabelText("Gateway"), {
      target: { value: "192.168.2.1" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Apply temporarily" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "The gateway must be a usable address in the WAN subnet.",
    );
    expect(
      screen.queryByRole("button", { name: "Confirm settings" }),
    ).not.toBeInTheDocument();
  });

  it("requires a new password after first login", async () => {
    fetchMock.mockImplementation((input, init) => {
      if (input === "/api/auth/session" && init?.method === "GET") {
        return Promise.resolve(response(401, { error: "unauthorized" }));
      }
      if (input === "/api/auth/login") {
        return Promise.resolve(response(200, { must_change_password: true }));
      }
      if (input === "/api/auth/password") {
        return Promise.resolve(response(204));
      }
      if (input === "/api/network/dhcp?interface=enp1s0") {
        return Promise.resolve(dhcpResponse());
      }
      return Promise.resolve(response(404, { error: "not_found" }));
    });

    render(<StaticWANPanel />);
    fireEvent.change(await screen.findByLabelText("Administrator password"), {
      target: { value: "initial-admin-password" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
    fireEvent.change(
      await screen.findByLabelText("Current administrator password"),
      { target: { value: "initial-admin-password" } },
    );
    fireEvent.change(screen.getByLabelText("New password"), {
      target: { value: "replacement-password" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Update password" }));

    expect(
      await screen.findByText(
        "Password updated. Sign in again with the new password.",
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Sign in" })).toBeInTheDocument();
  });
});
