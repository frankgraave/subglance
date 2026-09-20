// @vitest-environment jsdom
/*
 * The masthead's contract: the bar is identical on every screen (SUB-138).
 *
 * Its own file because each assertion here walks the whole app across four
 * routes, and `App.test.tsx` already mounts the shell fourteen times. Adding
 * three more full mounts to that file made the tests after them fail on a
 * monitor list that had not arrived yet — a property of the file's size, not
 * of the product, and the wrong thing to leave looking like a real failure.
 */
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { setToolbarSlot, setTopbarSlot } from "./shell/topbarSlot";

/** jsdom has neither matchMedia nor EventSource. */
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

/** A complete `User`, as `/api/v1/auth/me` really answers. */
const USER = {
  id: 1,
  email: "me@example.com",
  role: "admin",
  created_at: "2026-09-01T00:00:00Z",
};

const MONITOR = {
  id: 1,
  name: "api",
  type: "http",
  target: "https://api.example.com",
  interval_s: 20,
  timeout_s: 5,
  enabled: true,
  status: "up",
  created_at: "2026-09-01T00:00:00Z",
};

beforeEach(() => {
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
  /*
   * Answered per endpoint, not one body for every request.
   *
   * A single `{ monitors: [...] }` for everything also satisfies
   * `/api/v1/auth/me`, which casts it to a `User` — so the shell rendered a
   * signed-in dashboard for a response with no id, email or role, and these
   * tests would have passed against an app that asked the wrong endpoint or
   * skipped the session entirely.
   */
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      const body = url.includes("/auth/me") ? USER : { monitors: [MONITOR] };
      return Promise.resolve({
        ok: true,
        status: 200,
        json: async () => body,
      });
    }),
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  /*
   * The shell's portal slots are module state (SUB-138), so an unmounted
   * topbar leaves its detached node published. The next test then portals its
   * search into a node nobody can query, and every assertion that walks the
   * masthead sees a bar with one control missing — which is exactly the kind
   * of failure that looks like a product bug and is not.
   *
   * React clears them on unmount via the ref callback, but `cleanup()` runs
   * the unmount after this file's own listeners have already been torn down
   * in some orderings, so this is belt and braces.
   */
  setTopbarSlot(null);
  setToolbarSlot(null);
});

describe("the masthead", () => {
  it("keeps the masthead identical on every screen", async () => {
    /*
     * The rule the masthead exists for (SUB-138): what is in it is true
     * everywhere, so it never changes as you navigate. Earlier it did — the
     * layout switcher was dashboard-only and the add button was global while
     * meaning something local — and the bar taught people to re-read it on
     * every screen.
     *
     * Checked by walking the routes and comparing the accessible names of the
     * bar's controls, which is stronger than asserting any single button: a
     * control added to one screen's masthead fails this without anyone having
     * to remember to write a test for it.
     */
    render(<App />);
    await screen.findByText("api");

    /*
     * The *shape* of the bar, not its wording.
     *
     * A search field is in the same place on every screen and says what it
     * searches there — "Search monitors…" on the dashboard, "Filter
     * channels…" on Notifications — and requiring identical placeholder text
     * would forbid the field from being honest about its own screen. What
     * must not change is which controls exist and in what order, so buttons
     * are compared by name and the field by being a search box at all.
     */
    const mastheadShape = () =>
      [
        ...document
          .querySelector(".shell-topbar")!
          .querySelectorAll("button, input"),
      ].map((el) =>
        el.tagName === "INPUT"
          ? `input:${el.getAttribute("type")}`
          : `button:${el.getAttribute("aria-label") ?? el.textContent ?? ""}`,
      );

    const onDashboard = mastheadShape();
    // It is not empty, or this test would pass against a missing bar.
    expect(onDashboard.length).toBeGreaterThan(4);
    expect(onDashboard).toContain("input:search");
    expect(onDashboard).toContain("button:Status wall");

    for (const destination of ["Incidents", "Monitors", "Notifications", "Settings"]) {
      fireEvent.click(screen.getByRole("link", { name: destination }));
      await waitFor(() =>
        expect(
          screen
            .getByRole("link", { name: destination })
            .getAttribute("aria-current"),
        ).toBe("page"),
      );
      expect(mastheadShape()).toEqual(onDashboard);
    }
  });


  it("never puts a monitor-shaped action in the masthead", async () => {
    /*
     * The add button used to live here, and pressing it on Notifications
     * opened a drawer for adding a *monitor* on a screen about channels. A
     * global bar cannot hold a local action honestly; adding a monitor is now
     * the monitors page's own button.
     */
    render(<App />);
    await screen.findByText(USER.email);
    fireEvent.click(screen.getByRole("link", { name: "Notifications" }));
    await waitFor(() =>
      expect(
        screen
          .getByRole("link", { name: "Notifications" })
          .getAttribute("aria-current"),
      ).toBe("page"),
    );

    const masthead = document.querySelector(".shell-topbar")!;
    expect(
      [...masthead.querySelectorAll("button")].filter((button) =>
        /monitor/i.test(button.getAttribute("aria-label") ?? ""),
      ),
    ).toEqual([]);
  });



  it("keeps every pressed-state control in the masthead honest on every screen", async () => {
    /*
     * The general form of the rule above, and the reason the [+] left the
     * bar: a control in chrome that is on every screen must mean the same
     * thing on every screen. The layout segments and the theme segments
     * legitimately carry `aria-pressed` — they report which option is on —
     * so what is asserted is that the set does not change as you navigate.
     *
     * A control that is honest on the dashboard and meaningless elsewhere
     * shows up here as a difference, without anybody having to remember that
     * this test exists.
     */
    render(<App />);
    await screen.findByText(USER.email);

    const pressedNames = () =>
      [
        ...document
          .querySelector(".shell-topbar")!
          .querySelectorAll("[aria-pressed]"),
      ].map((el) => el.getAttribute("aria-label") ?? el.textContent ?? "");

    const onDashboard = pressedNames();
    expect(onDashboard).toContain("Component workbench");
    expect(onDashboard).not.toContain("Add a monitor");

    for (const destination of ["Incidents", "Monitors", "Notifications", "Settings"]) {
      fireEvent.click(screen.getByRole("link", { name: destination }));
      await waitFor(() =>
        expect(
          screen
            .getByRole("link", { name: destination })
            .getAttribute("aria-current"),
        ).toBe("page"),
      );
      expect(pressedNames()).toEqual(onDashboard);
    }
  });

});
