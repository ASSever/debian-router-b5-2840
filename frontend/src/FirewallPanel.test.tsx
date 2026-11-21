import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import FirewallPanel from "./FirewallPanel";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it("discards provisional inputs before the restored rules finish loading", async () => {
  const endpoint = "/api/network/firewall";
  const pending = {
    transaction_id: "0123456789abcdef0123456789abcdef",
    expires_at: new Date(Date.now() + 120_000).toISOString(),
  };
  let rolledBack = false;
  let resolveRestored!: (response: Response) => void;
  const restored = new Promise<Response>((resolve) => {
    resolveRestored = resolve;
  });
  const rules = (allow: string[]) =>
    Response.json({
      rules: { forwards: [], allow, block: [] },
      counters: null,
    });
  const request = vi.fn<typeof globalThis.fetch>(async (path) => {
    if (path === endpoint + "/rollback") {
      rolledBack = true;
      return Response.json({ status: "rolled_back" });
    }
    if (path === endpoint + "/rules") {
      return rolledBack ? restored : rules(["192.0.2.0/24"]);
    }
    if (path === "/api/network/routing") {
      return Response.json({
        config: { routes: [], ipv6: null },
        ipv6_active: false,
      });
    }
    return Response.json({
      enabled: true,
      pending: rolledBack ? undefined : pending,
    });
  });
  vi.stubGlobal("fetch", request);
  render(<FirewallPanel />);

  const apply = { name: "Apply firewall rules temporarily" };
  expect(await screen.findByRole("button", apply)).toBeDisabled();
  fireEvent.click(
    screen.getByRole("button", { name: "Restore previous firewall" }),
  );
  await waitFor(() =>
    expect(
      request.mock.calls.filter(([path]) => path === endpoint + "/rules"),
    ).toHaveLength(2),
  );
  expect(screen.queryByRole("button", apply)).toBeNull();

  resolveRestored(rules(["198.51.100.29/32"]));
  expect(await screen.findByRole("button", apply)).toBeEnabled();
  expect(screen.getByRole("textbox", { name: "Allow" })).toHaveValue(
    "198.51.100.29/32",
  );
});
