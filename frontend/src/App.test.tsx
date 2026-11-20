import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import App from "./App";

function healthResponse(healthy: boolean): Response {
  return {
    ok: healthy,
    status: healthy ? 200 : 503,
    json: async () => ({ status: healthy ? "ok" : "unavailable" }),
  } as Response;
}

function interfaceResponse(): Response {
  return {
    ok: true,
    status: 200,
    json: async () => ({
      interfaces: [
        {
          name: "enp1s0",
          operational_state: "up",
          carrier: true,
          speed_mbps: 1000,
        },
        {
          name: "enp2s0",
          operational_state: "down",
          carrier: false,
          speed_mbps: null,
        },
        {
          name: "wlp3s0",
          operational_state: "up",
          carrier: true,
          speed_mbps: null,
        },
      ],
    }),
  } as Response;
}

describe("router API health indicator", () => {
  const fetchMock = vi.fn<typeof fetch>();

  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    fetchMock.mockReset();
  });

  it("updates from reachable to unreachable and back", async () => {
    const healthResults = [true, false, true];
    fetchMock.mockImplementation((input) => {
      if (input === "/api/network/interfaces") {
        return Promise.resolve(interfaceResponse());
      }
      return Promise.resolve(healthResponse(healthResults.shift() ?? true));
    });

    render(<App />);

    expect(await screen.findByRole("status")).toHaveTextContent("API online");
    expect(screen.getByRole("button", { name: "Check now" })).toBeEnabled();
    expect(fetchMock).toHaveBeenCalledWith(
      "/healthz",
      expect.objectContaining({ method: "GET", cache: "no-store" }),
    );

    fireEvent.click(screen.getByRole("button", { name: "Check now" }));
    expect(await screen.findByRole("status")).toHaveTextContent(
      "Connection lost",
    );
    expect(screen.getByText(/not responding/i)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Check now" }));
    expect(await screen.findByRole("status")).toHaveTextContent("API online");
  });

  it("shows live port details and accepts only distinct WAN and LAN interfaces", async () => {
    fetchMock.mockImplementation((input) =>
      Promise.resolve(
        input === "/api/network/interfaces"
          ? interfaceResponse()
          : healthResponse(true),
      ),
    );

    render(<App />);

    expect(await screen.findByText("1000 Mbps")).toBeInTheDocument();
    expect(screen.getByText("No link")).toBeInTheDocument();
    expect(screen.getByText("No negotiated speed")).toBeInTheDocument();
    expect(screen.getByText("Speed unknown")).toBeInTheDocument();

    const wan = screen.getByLabelText("WAN interface");
    const lan = screen.getByLabelText("LAN interface");
    const confirm = screen.getByRole("button", { name: "Confirm selection" });
    fireEvent.change(wan, { target: { value: "enp1s0" } });
    fireEvent.change(lan, { target: { value: "enp1s0" } });
    expect(screen.getByRole("alert")).toHaveTextContent(
      "WAN and LAN must use different interfaces.",
    );
    expect(confirm).toBeDisabled();

    fireEvent.change(lan, { target: { value: "enp2s0" } });
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(confirm).toBeEnabled();
    fireEvent.click(confirm);
    expect(
      screen.getByText(
        "Port selection accepted. Network settings have not been applied.",
      ),
    ).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/network/interfaces",
      expect.objectContaining({ method: "GET", cache: "no-store" }),
    );
  });
});
