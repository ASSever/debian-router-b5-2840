import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import LANPanel from "./LANPanel";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function response(status: number, body: unknown) {
  return Promise.resolve({
    ok: status < 400,
    json: async () => body,
  } as Response);
}

it("shows the overlapping interfaces and fills the suggested subnet and pool", async () => {
  const config = {
    address: "192.168.1.1/24",
    pool_start: "192.168.1.100",
    pool_end: "192.168.1.200",
  };
  const suggested = {
    address: "192.168.10.1/24",
    pool_start: "192.168.10.100",
    pool_end: "192.168.10.200",
  };
  const fetch = vi
    .fn<typeof globalThis.fetch>()
    .mockImplementation((_url, init) =>
      init?.method === "POST"
        ? response(409, {
            error: "lan_overlap",
            interfaces: ["enp1s0", "wlp3s0"],
            prefix: "192.168.1.0/24",
            suggestion: suggested,
          })
        : response(200, {
            config,
            addresses: [],
            managed: false,
            wan_online: true,
          }),
    );
  vi.stubGlobal("fetch", fetch);
  render(<LANPanel />);
  expect(await screen.findByText(/Active addresses/)).toBeInTheDocument();
  fireEvent.click(
    screen.getByRole("button", { name: "Apply LAN temporarily" }),
  );
  expect(await screen.findByText(/enp1s0, wlp3s0/)).toBeInTheDocument();
  expect(fetch.mock.calls[1][1]?.body).toBe(JSON.stringify(config));
  fireEvent.click(screen.getByRole("button", { name: "Use 192.168.10.1/24" }));
  expect(screen.getByLabelText("LAN IPv4 address / prefix")).toHaveValue(
    suggested.address,
  );
  expect(screen.getByLabelText("DHCP pool start")).toHaveValue(
    suggested.pool_start,
  );
  expect(screen.getByLabelText("DHCP pool end")).toHaveValue(
    suggested.pool_end,
  );
});
