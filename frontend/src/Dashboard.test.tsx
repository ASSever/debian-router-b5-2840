import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";

import Dashboard from "./Dashboard";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

it("offers a report download after administrator access and retries an error", async () => {
  const fetcher = vi
    .fn()
    .mockResolvedValueOnce(new Response(JSON.stringify({ points: [] })))
    .mockResolvedValueOnce(new Response(null, { status: 503 }))
    .mockResolvedValueOnce(new Response('{"events":[]}'));
  vi.stubGlobal("fetch", fetcher);
  vi.stubGlobal(
    "URL",
    Object.assign(class extends URL {}, {
      createObjectURL: () => "blob:report",
      revokeObjectURL: vi.fn(),
    }),
  );
  const download = vi.fn();
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
    this: HTMLAnchorElement,
  ) {
    download(this.download, this.href);
  });

  render(<Dashboard />);
  expect(screen.queryByText("Download failure report")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Open dashboard" }));
  const button = await screen.findByRole("button", {
    name: "Download failure report",
  });
  fireEvent.click(button);
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "The failure report could not be downloaded.",
  );
  expect(download).not.toHaveBeenCalled();
  fireEvent.click(button);
  await screen.findByRole("button", { name: "Download failure report" });
  expect(fetcher.mock.calls[2][0]).toBe("/api/diagnostics/report");
  expect(download).toHaveBeenCalledWith(
    "router-failure-report.json",
    "blob:report",
  );
});

it("renders stored rates, blanks missing values and identifies stale measurements", async () => {
  const samples = [0, null, 2048].map((rx, index) => ({
    sampled_at: new Date(
      Date.UTC(2026, 10, 27, 9, 0, index * 30),
    ).toISOString(),
    traffic_available: true,
    interfaces: [
      { name: "enp2s0", rx_bytes_per_second: rx, tx_bytes_per_second: null },
    ],
    clients: [],
    lan_prefixes: [],
    system: {
      cpu_percent: 0,
      temperature_c: null,
      memory: null,
      disk: null,
      uptime_seconds: 90,
      wan: null,
      lan: null,
      wan_gateway: null,
      wan_dns: null,
      firewall_enabled: false,
      services: [{ unit: "debian-router-agent.service", state: "active" }],
    },
  }));
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(JSON.stringify({ stale: true, points: samples })),
    ),
  );
  render(<Dashboard />);
  fireEvent.click(screen.getByRole("button", { name: "Open dashboard" }));
  const plot = await screen.findByRole("img", {
    name: "Interface traffic saved samples",
  });
  expect(plot.querySelectorAll(".dashboard-bar")).toHaveLength(2);
  expect(
    within(plot).getByTitle(/RX 0 B\/s.*TX Unavailable/),
  ).toBeInTheDocument();
  expect(
    within(plot).getByTitle(/RX Unavailable.*TX Unavailable/),
  ).toBeEmptyDOMElement();
  expect(screen.getByText(/Stale measurement/)).toBeInTheDocument();
  expect(screen.getByText("active")).toBeInTheDocument();
  expect(screen.getByText("0 %")).toBeInTheDocument();
});
