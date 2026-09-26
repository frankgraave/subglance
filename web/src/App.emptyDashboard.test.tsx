// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { disabledWatchdog } from "./watchdog/fixtures";

/**
 * SUB-27: the empty dashboard is the onboarding (DESIGN.md §7.6), and its one
 * next step used to be directions — "press Add monitor at the top of the
 * page" — to a button that has lived on the Monitors card since SUB-138. On
 * a fresh install that sentence pointed at nothing.
 *
 * The add form is stubbed for the same reason as in App.create.test.tsx: what
 * is under test is where the button leads, not the form behind it.
 */

vi.mock("./monitors/AddMonitor", () => ({
  AddMonitor: ({ onCreated }: { onCreated?: (id: string) => void }) => (
    <button type="button" onClick={() => onCreated?.("2")}>
      pretend the monitor saved
    </button>
  ),
}));

class FakeSource {
  readyState = 1;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: (() => void) | null = null;
  addEventListener() {}
  removeEventListener() {}
  close() {
    this.readyState = 2;
  }
}

/** The signed-in reader's role, flipped per test. */
let role = "admin";

beforeEach(() => {
  role = "admin";
  window.localStorage.clear();
  window.history.replaceState(null, "", "/");
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: false,
    media: query,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    onchange: null,
    dispatchEvent: () => false,
  }));
  vi.stubGlobal("EventSource", FakeSource);
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/api/v1/watchdog") return Promise.resolve(new Response(JSON.stringify(disabledWatchdog)));
      const body = url.includes("/auth/me")
        ? {
            id: 1,
            email: "me@example.com",
            role,
            created_at: "2026-09-01T00:00:00Z",
          }
        : { monitors: [] };
      return Promise.resolve({ ok: true, status: 200, json: async () => body });
    }),
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("the empty dashboard", () => {
  it("opens the add form from its one next step", async () => {
    render(<App />);
    await screen.findByText("Nothing is being watched yet");

    fireEvent.click(
      screen.getByRole("button", { name: "Add the first monitor" }),
    );

    expect(
      await screen.findByRole("button", { name: /pretend the monitor saved/ }),
    ).toBeTruthy();
    // The form is a route, so the step lands on a real address: Back closes
    // it and a reload reopens it, the same as from the Monitors card.
    expect(window.location.pathname).toBe("/monitors/new");
  });

  it("offers a viewer no button, and says who adds monitors", async () => {
    role = "viewer";
    render(<App />);
    await screen.findByText("Nothing is being watched yet");

    expect(
      screen.queryByRole("button", { name: "Add the first monitor" }),
    ).toBeNull();
    expect(
      screen.getByText(/An editor or an admin adds monitors on the/),
    ).toBeTruthy();
  });
});
