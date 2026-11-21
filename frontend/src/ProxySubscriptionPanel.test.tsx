import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import ProxySubscriptionPanel from "./ProxySubscriptionPanel";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it("renders a mixed report and clears the private subscription input", async () => {
  const fetchAPI = vi.fn<typeof fetch>(async (_, init) => {
    if (init?.method !== "POST") {
      return Response.json({ imported: 0, rejected: 0, rows: [] });
    }
    return Response.json({
      imported: 1,
      rejected: 1,
      rows: [
        {
          line: 1,
          protocol: "hysteria2",
          warning: "provider_options_unverified",
        },
        { line: 2, error: "unsupported_protocol" },
      ],
    });
  });
  vi.stubGlobal("fetch", fetchAPI);
  render(<ProxySubscriptionPanel />);
  const input = screen.getByLabelText("Subscription URL or contents");
  fireEvent.change(input, {
    target: { value: "https://provider.example/private-token" },
  });
  await vi.waitFor(() =>
    expect(
      screen.getByRole("button", { name: "Import subscription" }),
    ).toBeEnabled(),
  );
  fireEvent.click(screen.getByRole("button", { name: "Import subscription" }));
  expect(await screen.findByText("1 imported · 1 rejected")).toBeVisible();
  expect(screen.getByText(/Unsupported protocol/)).toBeVisible();
  expect(
    screen.getByText(/Connectivity unchecked.*Provider-specific/),
  ).toBeVisible();
  expect(input).toHaveValue("");
});
