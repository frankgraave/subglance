// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";

/**
 * SUB-99, part 1, as the user meets it: adding a monitor is the first thing
 * anyone does with SubGlance, and until this the list simply did not change.
 * The form closed onto a dashboard that denied the new monitor existed, until
 * the page was reloaded by hand.
 *
 * A file of its own because the add form is stubbed here, and `App.test.tsx`
 * drives the real one. The stub is deliberate: App's only responsibility on
 * this path is what it does with `onCreated`, and rendering the real form
 * would drag its validation and its preview endpoint into an assertion about
 * cache invalidation.
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

const USER = {
  id: 1,
  email: "me@example.com",
  role: "admin",
  created_at: "2026-09-01T00:00:00Z",
};

/** Names chosen so no assertion can pass by matching another fixture. */
const ALPHA = {
  id: 1,
  name: "alpha",
  type: "http",
  target: "https://alpha.example.com",
  interval_s: 20,
  timeout_s: 5,
  enabled: true,
  status: "up",
  created_at: "2026-09-01T00:00:00Z",
};

const BRAVO = {
  ...ALPHA,
  id: 2,
  name: "bravo",
  target: "https://bravo.example.com",
};

/** Flipped by the test; the stub server only knows bravo once it is set. */
let created = false;

beforeEach(() => {
  created = false;
  window.localStorage.clear();
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
      const body = url.includes("/auth/me")
        ? USER
        : { monitors: created ? [ALPHA, BRAVO] : [ALPHA] };
      return Promise.resolve({ ok: true, status: 200, json: async () => body });
    }),
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("creating a monitor", () => {
  it("shows it in the list without a reload", async () => {
    render(<App />);
    await screen.findByText("alpha");
    expect(screen.queryByText("bravo")).toBeNull();

    // Adding a monitor lives on Monitors since SUB-138, where the button sits
    // in the header of the card it adds to.
    fireEvent.click(screen.getByRole("link", { name: "Monitors" }));
    fireEvent.click(
      await screen.findByRole("button", { name: /add monitor/i }),
    );
    const save = await screen.findByRole("button", {
      name: /pretend the monitor saved/,
    });
    created = true;
    fireEvent.click(save);

    expect(await screen.findByText("bravo")).toBeTruthy();
  });

  it("returns to the list rather than staying on the form", async () => {
    render(<App />);
    await screen.findByText("alpha");
    fireEvent.click(screen.getByRole("link", { name: "Monitors" }));
    fireEvent.click(
      await screen.findByRole("button", { name: /add monitor/i }),
    );
    const save = await screen.findByRole("button", {
      name: /pretend the monitor saved/,
    });
    created = true;
    fireEvent.click(save);

    await screen.findByText("bravo");
    expect(
      screen.queryByRole("button", { name: /pretend the monitor saved/ }),
    ).toBeNull();
  });
});
