// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { disabledWatchdog } from "./watchdog/fixtures";

/*
 * The incidents screen's mute button, through the real app shell.
 *
 * Muting repeat alerts is `POST /api/v1/incidents/{id}/ack`, which the server
 * guards with the write role. The monitor's own page already withheld the
 * button from a viewer; the incidents list did not, so a viewer was offered a
 * control that could only answer 403. Rendering the whole app rather than the
 * list alone is the point: the permission is read from the session in the
 * shell, and a list that honours `canWrite` proves nothing if the shell never
 * passes it.
 */

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
  window.history.replaceState(null, "", "/incidents");
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
  window.history.replaceState(null, "", "/");
});

describe("incident mute permissions through the real app shell", () => {
  it.each([
    { role: "viewer", allowed: false },
    { role: "editor", allowed: true },
    { role: "admin", allowed: true },
  ])(
    "offers to mute repeat alerts to $role only when permitted",
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
                  status: "down",
                  created_at: "2026-09-01T00:00:00Z",
                },
              ],
            },
            // One confirmed, unacknowledged, unresolved incident: exactly the
            // row that draws the mute button for a role allowed to press it.
            "/api/v1/incidents": {
              incidents: [
                {
                  id: 5,
                  monitor_id: 1,
                  started_at: "2026-09-10T08:00:00Z",
                  confirmed_at: "2026-09-10T08:01:00Z",
                  confirmed: true,
                  resolved: false,
                  acked: false,
                  duration_s: 720,
                  cause: "dns",
                },
              ],
            },
            "/api/v1/incidents/resolved": { incidents: [], has_more: false },
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
        expect(document.querySelector(".inc-row")).not.toBeNull(),
      );
      expect(screen.queryByRole("alert")).toBeNull();
      expect(
        screen.queryByRole("button", { name: /^Mute repeat alerts/ }) !== null,
      ).toBe(allowed);
    },
  );
});
