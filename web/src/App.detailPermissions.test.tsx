// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { disabledWatchdog } from "./watchdog/fixtures";
import { setToolbarSlot, setTopbarSlot } from "./shell/topbarSlot";

class FakeSource {
  readyState = 1;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  addEventListener() {}
  removeEventListener() {}
  close() {
    this.readyState = 2;
  }
}

beforeEach(() => {
  window.localStorage.clear();
  window.history.replaceState(null, "", "/monitors/1");
  vi.stubGlobal("EventSource", FakeSource);
  vi.stubGlobal("matchMedia", (media: string) => ({
    matches: false,
    media,
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent: () => false,
  }));
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  setTopbarSlot(null);
  setToolbarSlot(null);
  window.history.replaceState(null, "", "/");
});

describe("detail check permissions through the real app shell", () => {
  it.each([
    { role: "viewer", allowed: false },
    { role: "editor", allowed: true },
    { role: "admin", allowed: true },
  ])(
    "offers a check to $role only when permitted",
    async ({ role, allowed }) => {
      vi.stubGlobal(
        "fetch",
        vi.fn(async (input: RequestInfo | URL) => {
          const path = new URL(String(input), window.location.origin).pathname;
          const responses: Record<string, unknown> = {
            "/api/v1/watchdog": disabledWatchdog,
            "/api/v1/auth/me": {
              id: 1,
              email: "operator@example.com",
              role,
              created_at: "2026-09-01T00:00:00Z",
            },
            "/api/v1/monitors": {
              monitors: [
                {
                  id: 1,
                  name: "api",
                  type: "http",
                  target: "https://api.example.com",
                  interval_s: 20,
                  timeout_s: 5,
                  enabled: true,
                  status: "up",
                  created_at: "2026-09-01T00:00:00Z",
                },
              ],
            },
            "/api/v1/monitors/1": { id: 1, name: "Latest settings", type: "http", target: "https://api.example.com", interval_s: 20, timeout_s: 5, enabled: true, status: "up", created_at: "2026-09-01T00:00:00Z", repeat_after_s: 731 },
            "/api/v1/monitors/1/uptime": { windows: [] },
            "/api/v1/monitors/1/incidents": { incidents: [] },
            "/api/v1/monitors/1/heartbeats": { heartbeats: [] },
          };
          if (!(path in responses))
            throw new Error(`Unexpected request: ${path}`);
          return new Response(JSON.stringify(responses[path]), {
            status: 200,
            headers: { "Content-Type": "application/json", ETag: 'W/"1"' },
          });
        }),
      );
      render(<App />);
      await screen.findByText("operator@example.com");
      await waitFor(() =>
        expect(document.querySelector(".mon-detail")).not.toBeNull(),
      );
      await screen.findByText("No failed checks in the recent history.");
      expect(screen.queryByRole("alert")).toBeNull();
      expect(screen.queryByRole("button", { name: "Check now" }) !== null).toBe(
        allowed,
      );
      await screen.findByText("Nothing has gone wrong yet.");
      expect(screen.queryByRole("button", { name: "Edit monitor" }) !== null).toBe(allowed);
      if (allowed) {
        fireEvent.click(screen.getByRole("button", { name: "Edit monitor" }));
        expect((await screen.findByLabelText("Name") as HTMLInputElement).value).toBe("Latest settings");
        expect((screen.getByLabelText("Repeat alert base (seconds)") as HTMLInputElement).value).toBe("731");
        expect(screen.queryByRole("alert")).toBeNull();
      }
    },
  );
});
