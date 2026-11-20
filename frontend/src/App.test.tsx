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
    fetchMock
      .mockResolvedValueOnce(healthResponse(true))
      .mockResolvedValueOnce(healthResponse(false))
      .mockResolvedValueOnce(healthResponse(true));

    render(<App />);

    expect(await screen.findByRole("status")).toHaveTextContent("API online");
    expect(screen.getByRole("button", { name: "Check now" })).toBeEnabled();
    expect(fetchMock).toHaveBeenLastCalledWith(
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
});
