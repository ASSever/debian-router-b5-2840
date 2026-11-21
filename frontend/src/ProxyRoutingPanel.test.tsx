import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import ProxyRoutingPanel from "./ProxyRoutingPanel";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it("includes the subscription revision and reports a rolled back profile", async () => {
  const profile = { default: "direct", groups: [], rules: [] };
  const request = vi.fn<typeof fetch>(async (_, options) =>
    options?.method === "PUT"
      ? Response.json({ error: "proxy_apply_rolled_back" }, { status: 503 })
      : Response.json(profile),
  );
  vi.stubGlobal("fetch", request);
  render(<ProxyRoutingPanel subscriptionID="current-revision" />);
  fireEvent.click(screen.getByRole("button", { name: "Edit proxy profile" }));
  const editor = await screen.findByLabelText("Proxy profile (JSON)");
  expect(editor).toHaveValue(JSON.stringify(profile, null, 2));
  fireEvent.click(screen.getByRole("button", { name: "Apply proxy profile" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "The previous proxy profile was restored",
  );
  expect(request).toHaveBeenCalledWith(
    "/api/proxy/profile",
    expect.objectContaining({
      body: JSON.stringify({ ...profile, subscription_id: "current-revision" }),
    }),
  );
});
