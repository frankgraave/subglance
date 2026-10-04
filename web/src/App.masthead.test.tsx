// @vitest-environment jsdom
/*
 * The masthead's contract (SUB-182): a control is in the bar only on a screen
 * where it does something. The global set — sidebar toggle, search, theme —
 * is on every route, the monitor detail page included; a
 * control for one screen is on that screen and nowhere else.
 *
 * Its own file because each assertion here walks the whole app across six
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
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { disabledWatchdog } from "./watchdog/fixtures";
import { setToolbarSlot } from "./shell/toolbarSlot";

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
  // Every test starts on the dashboard: the first one ends on a monitor's
  // detail page, and the route is the address bar's, which outlives a test.
  window.history.replaceState(null, "", "/");
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
      if (url === "/api/v1/watchdog") return Promise.resolve(new Response(JSON.stringify(disabledWatchdog)));
      const path = url.split("?")[0];
      const body = url.includes("/auth/me")
        ? USER
        : path === "/api/v1/monitors/1"
          ? MONITOR
          : path === "/api/v1/monitors/1/uptime"
            ? { windows: [] }
            : path === "/api/v1/monitors/1/incidents"
              ? { incidents: [] }
              : path === "/api/v1/monitors/1/heartbeats"
                ? { heartbeats: [] }
                : { monitors: [MONITOR] };
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
  setToolbarSlot(null);
});

/** The masthead's controls by accessible name, in order. */
const mastheadNames = () =>
  [
    ...document.querySelector(".shell-topbar")!.querySelectorAll("button, input, select"),
  ].map((el) =>
    el.tagName === "BUTTON"
      ? (el.getAttribute("aria-label") ?? el.textContent ?? "")
      : `${el.tagName.toLowerCase()}:${el.getAttribute("type") ?? ""}`,
  );

/** The page toolbar's controls by accessible name, in order. */
const toolbarNames = () =>
  [...document.querySelectorAll(".shell-toolbar button, .shell-toolbar input, .shell-toolbar select")].map(
    (el) => el.getAttribute("aria-label") ?? el.textContent ?? el.tagName,
  );

/*
 * What works on every screen. Written out rather than read off the dashboard,
 * so the set is a decision this file states and not whatever the first screen
 * happened to render.
 */
const GLOBAL = [
  "Collapse sidebar (Ctrl+B)",
  "Search",
  "Light",
  "Dark",
  "Auto",
];

const LAYOUTS = ["Rows", "Cards", "Compact", "Status wall"];

const ROUTES: { link: string | null; path: string; ready: () => Promise<unknown> }[] = [
  { link: "Dashboard", path: "/", ready: () => screen.findByText("api") },
  { link: "Incidents", path: "/incidents", ready: () => screen.findByRole("heading", { level: 1, name: "Incidents" }) },
  { link: "Monitors", path: "/monitors", ready: () => screen.findByRole("searchbox", { name: "Filter monitors" }) },
  { link: "Notifications", path: "/notifications", ready: () => screen.findByRole("searchbox", { name: /filter channels/i }) },
  { link: "Settings", path: "/settings", ready: () => screen.findByText("Not configured") },
];

async function visit(route: (typeof ROUTES)[number]) {
  fireEvent.click(screen.getByRole("link", { name: route.link! }));
  await waitFor(() => expect(window.location.pathname).toBe(route.path));
  await route.ready();
}

/*
 * The page's title is the masthead's (SUB-207). Read off the rendered app
 * rather than the table, so a screen that draws an `h1` of its own, or a
 * route that forgets to pass its title up, fails here.
 */
function titleState() {
  const masthead = document.querySelector(".shell-topbar")!;
  const h1s = [...document.querySelectorAll("h1")];
  const main = document.querySelector("main")!;
  return {
    count: h1s.length,
    inMasthead: h1s.every((h) => masthead.contains(h)),
    text: h1s[0]?.textContent ?? null,
    tab: document.title,
    lit: [...document.querySelectorAll(".shell-sidebar [aria-current='page']")].map(
      (a) => a.textContent?.trim() ?? "",
    ),
    mainName: main.getAttribute("aria-labelledby") === h1s[0]?.id,
  };
}

describe("the masthead's title", () => {
  it("is the page's only h1 on every route, the same word as the tab and the rail", async () => {
    render(<App />);
    await screen.findByText("api");
    for (const route of ROUTES) {
      if (route.path !== "/") await visit(route);
      const word = route.link!;
      expect(titleState(), route.path).toEqual({
        count: 1,
        inMasthead: true,
        text: word,
        tab: `${word} \u2014 SubGlance`,
        lit: [word],
        mainName: true,
      });
    }
  });

  it("names a monitor's page after the monitor, under a link to Monitors", async () => {
    render(<App />);
    fireEvent.click(await screen.findByRole("link", { name: "api" }));
    await waitFor(() => expect(window.location.pathname).toBe("/monitors/1"));
    await screen.findByRole("article", { name: "api" });
    expect(titleState()).toEqual({
      count: 1,
      inMasthead: true,
      text: "api",
      tab: "api \u2014 SubGlance",
      lit: ["Monitors"],
      mainName: true,
    });

    // The breadcrumb's parent is a real link to the section, and following
    // it is a navigation like the rail's.
    const masthead = document.querySelector<HTMLElement>(".shell-topbar")!;
    const up = within(masthead).getByRole("link", { name: "Monitors" });
    expect(up.getAttribute("href")).toBe("/monitors");
    fireEvent.click(up);
    await waitFor(() => expect(window.location.pathname).toBe("/monitors"));
    expect(titleState().text).toBe("Monitors");
    expect(within(masthead).queryByRole("link")).toBeNull();
  });

  it("says Monitor while the id names no monitor", async () => {
    window.history.replaceState(null, "", "/monitors/99");
    render(<App />);
    await screen.findByText("That monitor does not exist, or has been deleted.");
    expect(titleState()).toMatchObject({ count: 1, inMasthead: true, text: "Monitor", tab: "Monitor \u2014 SubGlance" });
  });
});

describe("the masthead", () => {
  it("holds the global set, and only the global set, on every route", async () => {
    /*
     * The rule (SUB-182): a control is in this bar only where it does
     * something. Checked as an exact list per route rather than as "the same
     * as the dashboard", so a control that leaks into the bar on one screen
     * fails here, and so does one that goes missing from one screen — which is
     * how search disappeared from the monitor detail page.
     */
    render(<App />);
    await screen.findByText("api");

    for (const route of ROUTES) {
      if (route.path !== "/") await visit(route);
      expect(mastheadNames(), route.path).toEqual(GLOBAL);
    }

    // The detail page is reached from the list, not from the sidebar, and it
    // is the screen that used to lose search.
    fireEvent.click(screen.getByRole("link", { name: "Dashboard" }));
    fireEvent.click(await screen.findByRole("link", { name: "api" }));
    await waitFor(() => expect(window.location.pathname).toBe("/monitors/1"));
    expect(mastheadNames(), "/monitors/1").toEqual(GLOBAL);
  });

  it("puts the layout switcher in the dashboard's View panel and on no other screen", async () => {
    /*
     * The four layouts are four ways of drawing the dashboard. In the masthead
     * the switcher sat on Monitors, Incidents, Notifications and Settings,
     * where pressing it changed nothing on screen. It is in the View panel at
     * the head of the dashboard's list now (SUB-183).
     */
    render(<App />);
    await screen.findByText("api");
    expect(toolbarNames(), "the dashboard draws nothing in the page toolbar").toEqual([]);
    fireEvent.click(screen.getByRole("button", { name: /^View: / }));
    const panel = screen.getByRole("dialog", { name: "View" });
    for (const layout of LAYOUTS) expect(within(panel).getByRole("button", { name: layout })).toBeTruthy();
    fireEvent.keyDown(panel, { key: "Escape" });

    for (const route of ROUTES.slice(1)) {
      await visit(route);
      const here = [...mastheadNames(), ...toolbarNames()];
      for (const layout of LAYOUTS) expect(here, `${layout} on ${route.path}`).not.toContain(layout);
      expect(screen.queryByRole("button", { name: /^View: / }), route.path).toBeNull();
    }
  });

  it("offers one search entry per bar, never two side by side", async () => {
    /*
     * The masthead's search is the command menu, on every screen. A screen
     * that filters its list does it from its own toolbar, so the masthead
     * never carries a second, page-bound field beside the global one.
     */
    render(<App />);
    await screen.findByText("api");
    for (const route of ROUTES) {
      if (route.path !== "/") await visit(route);
      const masthead = document.querySelector(".shell-topbar")!;
      expect(masthead.querySelectorAll("input").length, route.path).toBe(0);
      expect(within(masthead as HTMLElement).getAllByRole("button", { name: "Search" })).toHaveLength(1);
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



  it("draws every select in the page toolbar as one framed field, on every screen", async () => {
    /*
     * The dashboard framed its tag filters with a glyph while Monitors and
     * Incidents drew a bare key beside a bordered select, so one bar held two
     * patterns depending on the screen. Checked on the rendered bar of every
     * route, because a select portalled into the toolbar is invisible to a
     * scan of its source file.
     */
    render(<App />);
    await screen.findByText("api");
    let seen = 0;
    for (const route of ROUTES) {
      if (route.path !== "/") await visit(route);
      for (const select of document.querySelectorAll<HTMLSelectElement>(".shell-toolbar select")) {
        seen++;
        const frame = select.closest("label");
        const where = `${route.path}: ${frame?.textContent ?? select.outerHTML}`;
        expect(frame?.classList.contains("tb-field"), where).toBe(true);
        expect(select.classList.contains("tb-select"), where).toBe(true);
        expect(frame?.querySelector(":scope > svg[aria-hidden='true']"), where).not.toBeNull();
        expect(frame?.querySelector(".tb-label")?.textContent?.trim(), where).toBeTruthy();
      }
    }
    // Incidents (show, history) at least, so the walk cannot pass on a
    // toolbar that rendered no selects. Monitors left the toolbar (SUB-207).
    expect(seen).toBeGreaterThanOrEqual(2);
  });

  it("keeps every pressed-state control in the masthead the same on every screen", async () => {
    /*
     * A control in chrome that is on every screen must mean the same thing on
     * every screen. The theme segments legitimately carry `aria-pressed` —
     * they report what is on — so what is asserted is that the set does not
     * change as you navigate.
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
    expect(onDashboard).toEqual(["Light", "Dark", "Auto"]);
    expect(onDashboard).not.toContain("Add a monitor");

    for (const route of ROUTES.slice(1)) {
      await visit(route);
      expect(pressedNames()).toEqual(onDashboard);
    }
  });

  it("drives the same preferences from the settings page as from the bars", async () => {
    /*
     * The Display section on /settings is a second view of the preferences
     * App owns, not a second copy of them: a choice made there has to show
     * in the masthead and the dashboard's View panel at once and be what the
     * next visit reads back.
     */
    render(<App />);
    await screen.findByText(USER.email);
    fireEvent.click(screen.getByRole("link", { name: "Settings" }));
    const card = await screen.findByRole("region", { name: "Display" });
    const masthead = document.querySelector<HTMLElement>(".shell-topbar")!;
    const pressed = (root: HTMLElement, group: string, option: string) =>
      within(within(root).getByRole("group", { name: group }))
        .getByRole("button", { name: option })
        .getAttribute("aria-pressed");

    fireEvent.click(within(within(card).getByRole("group", { name: "Colour theme" })).getByRole("button", { name: "Light" }));
    expect(pressed(masthead, "Colour theme", "Light")).toBe("true");

    fireEvent.click(within(within(card).getByRole("group", { name: "Dashboard layout" })).getByRole("button", { name: "Compact" }));
    expect(window.localStorage.getItem("subglance:layout")).toBe("compact");

    fireEvent.click(within(within(card).getByRole("group", { name: "Cards per row" })).getByRole("button", { name: "Three per row" }));
    expect(pressed(card, "Cards per row", "Three per row")).toBe("true");
    expect(window.localStorage.getItem("subglance:card-columns")).toBe("3");

    fireEvent.click(screen.getByRole("link", { name: "Dashboard" }));
    await screen.findByText("api");
    fireEvent.click(screen.getByRole("button", { name: "View: Compact" }));
    const view = screen.getByRole("dialog", { name: "View" });
    expect(pressed(view, "Dashboard layout", "Compact")).toBe("true");
  });
});
