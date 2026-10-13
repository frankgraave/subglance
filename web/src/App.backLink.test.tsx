// @vitest-environment jsdom
/*
 * The monitor page's back control returns to the list the page was opened
 * from, and says which (SUB-202): "Dashboard" from the dashboard, "All
 * monitors" from the inventory, and the inventory — where the breadcrumb
 * goes — when there is no list behind the page. The list it returns to comes
 * back as it was left.
 *
 * Its own file because every case mounts the whole app and walks real
 * history, and the files that already do that are at the size where one
 * more full mount makes the tests after it flaky.
 */
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { disabledWatchdog } from "./watchdog/fixtures";

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

const USER = {
  id: 1,
  email: "me@example.com",
  role: "admin",
  created_at: "2026-09-01T00:00:00Z",
};

const monitor = (id: number, name: string, status: string, tags: Record<string, string> = {}) => ({
  id,
  name,
  type: "http",
  target: `https://${name}.example.com`,
  interval_s: 20,
  timeout_s: 5,
  enabled: true,
  status,
  tags,
  created_at: "2026-09-01T00:00:00Z",
});
const MONITORS = [monitor(1, "api", "up"), monitor(2, "db", "down", { env: "prod" })];

beforeEach(() => {
  // jsdom has no modal dialogs; the command menu is one.
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute("open", ""); };
  HTMLDialogElement.prototype.close = function () { this.removeAttribute("open"); };
  vi.stubGlobal("scrollTo", vi.fn());
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
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/api/v1/watchdog") return Promise.resolve(new Response(JSON.stringify(disabledWatchdog)));
      const path = url.split("?")[0];
      const one = /^\/api\/v1\/monitors\/(\d+)(\/[a-z]+)?$/.exec(path);
      const body = url.includes("/auth/me")
        ? USER
        : one === null
          ? { monitors: MONITORS }
          : one[2] === "/uptime"
            ? { windows: [] }
            : one[2] === "/incidents"
              ? { incidents: [] }
              : one[2] === "/heartbeats"
                ? { heartbeats: [] }
                : MONITORS.find((m) => String(m.id) === one[1]);
      return Promise.resolve({ ok: true, status: 200, json: async () => body });
    }),
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

/** The back control, by the words it says. */
const back = (name: string) => screen.findByRole("button", { name });

/** Opens a monitor from whatever list is on screen, the way a person does. */
async function open(name: string) {
  fireEvent.click(await screen.findByRole("link", { name }));
  await screen.findByRole("article", { name });
}

/** The dashboard's status tab that is pressed. */
const pressedTab = () =>
  screen
    .getByRole("group", { name: "Filter by status" })
    .querySelector("[aria-pressed='true']")
    ?.textContent?.replace(/\d+/g, "")
    .trim();

describe("the back control on a monitor's page", () => {
  it("returns to the dashboard it was opened from, and says Dashboard", async () => {
    render(<App />);
    await open("api");
    expect(window.location.pathname).toBe("/monitors/1");
    fireEvent.click(await back("Dashboard"));
    await waitFor(() => expect(window.location.pathname).toBe("/"));
    await screen.findByRole("link", { name: "api" });
    // Returned to, not pushed: the dashboard's own entry, so the browser's
    // Forward is the monitor again rather than nothing.
    window.history.forward();
    await waitFor(() => expect(window.location.pathname).toBe("/monitors/1"));
    await back("Dashboard");
  });

  it("returns to the inventory it was opened from, and says All monitors", async () => {
    render(<App />);
    await screen.findByRole("link", { name: "api" });
    fireEvent.click(screen.getByRole("link", { name: "Monitors" }));
    await screen.findByRole("searchbox", { name: "Filter monitors" });
    await open("db");
    fireEvent.click(await back("All monitors"));
    await waitFor(() => expect(window.location.pathname).toBe("/monitors"));
    await screen.findByRole("searchbox", { name: "Filter monitors" });
  });

  it("goes to the inventory, where the breadcrumb goes, from a pasted link", async () => {
    window.history.replaceState(null, "", "/monitors/1");
    render(<App />);
    await screen.findByRole("article", { name: "api" });
    fireEvent.click(await back("All monitors"));
    await waitFor(() => expect(window.location.pathname).toBe("/monitors"));
    await screen.findByRole("searchbox", { name: "Filter monitors" });
  });

  it("goes to the inventory when the page was reached from a screen that is not a list", async () => {
    window.history.replaceState(null, "", "/incidents");
    render(<App />);
    await screen.findByRole("heading", { level: 1, name: "Incidents" });
    fireEvent.click(screen.getByRole("link", { name: "Dashboard" }));
    await open("api");
    // Opened from the dashboard, then left for the incidents screen and come
    // back to through Back: still the dashboard's page, still its way back.
    fireEvent.click(screen.getByRole("link", { name: "Incidents" }));
    await screen.findByRole("heading", { level: 1, name: "Incidents" });
    window.history.back();
    await back("Dashboard");
    fireEvent.click(await back("Dashboard"));
    await waitFor(() => expect(window.location.pathname).toBe("/"));
  });

  it("goes to the inventory when the command menu opened the page from Settings", async () => {
    window.history.replaceState(null, "", "/settings");
    render(<App />);
    await screen.findByText("me@example.com");
    // The shortcut is bound once the session has settled.
    await act(async () => {});
    fireEvent.keyDown(window, { key: "k", ctrlKey: true });
    const menu = await screen.findByRole("dialog", { name: "Command menu" });
    fireEvent.click(await within(menu).findByRole("option", { name: /Open db/ }));
    await screen.findByRole("article", { name: "db" });
    fireEvent.click(await back("All monitors"));
    await waitFor(() => expect(window.location.pathname).toBe("/monitors"));
  });

  it("keeps its way back across the skip link", async () => {
    render(<App />);
    await open("api");
    // Native fragment navigation: a new entry with no state of its own.
    window.history.pushState(null, "", "/monitors/1#shell-main");
    fireEvent.popState(window);
    fireEvent.click(await back("Dashboard"));
    await waitFor(() => expect(window.location.pathname).toBe("/"));
  });
});

describe("the list it returns to", () => {
  it("is the dashboard as it was left: its status tab and its filter", async () => {
    render(<App />);
    await screen.findByRole("link", { name: "api" });
    fireEvent.click(screen.getByRole("button", { name: /^Down/ }));
    fireEvent.change(screen.getByRole("searchbox", { name: "Filter monitors by name or address" }), {
      target: { value: "d" },
    });
    expect(screen.queryByRole("link", { name: "api" })).toBeNull();
    await open("db");
    fireEvent.click(await back("Dashboard"));
    await screen.findByRole("link", { name: "db" });
    expect(pressedTab()).toBe("Down");
    expect(
      (screen.getByRole("searchbox", { name: "Filter monitors by name or address" }) as HTMLInputElement).value,
    ).toBe("d");
    expect(screen.queryByRole("link", { name: "api" })).toBeNull();
  });

  it("is the inventory as it was left: its filter and its sort", async () => {
    window.history.replaceState(null, "", "/monitors");
    render(<App />);
    const filter = await screen.findByRole("searchbox", { name: "Filter monitors" });
    fireEvent.click(screen.getByRole("button", { name: /^Sort: / }));
    fireEvent.click(
      within(screen.getByRole("dialog", { name: "Sort monitors" })).getByRole("radio", { name: "Status" }),
    );
    fireEvent.change(filter, { target: { value: "db" } });
    await open("db");
    fireEvent.click(await back("All monitors"));
    const again = await screen.findByRole("searchbox", { name: "Filter monitors" });
    expect((again as HTMLInputElement).value).toBe("db");
    expect(screen.queryByRole("link", { name: "api" })).toBeNull();
    expect(screen.getByRole("button", { name: "Sort: Status" })).toBeTruthy();
  });

  it("keeps a tag choice through a reload, while the list is still loading", async () => {
    // A reload keeps the entry, and so the view. The first render has no
    // monitors yet, which says nothing about which tags exist: pruning the
    // choice against that empty list would drop it before the data arrived.
    window.history.replaceState(
      { subglanceListView: { list: "monitors", tags: { env: "prod" } } },
      "",
      "/monitors",
    );
    render(<App />);
    await screen.findByRole("link", { name: "db" });
    expect(screen.queryByRole("link", { name: "api" })).toBeNull();
  });

  it("is the inventory as it was left after its add form was opened and closed", async () => {
    window.history.replaceState(null, "", "/monitors");
    render(<App />);
    const filter = await screen.findByRole("searchbox", { name: "Filter monitors" });
    fireEvent.change(filter, { target: { value: "db" } });
    // Closing the form pushes a fresh /monitors entry; the view has to move
    // onto it, or the way back from the next monitor finds it unfiltered.
    fireEvent.click(screen.getByRole("button", { name: "Add monitor" }));
    await waitFor(() => expect(window.location.pathname).toBe("/monitors/new"));
    fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(window.location.pathname).toBe("/monitors"));
    await open("db");
    fireEvent.click(await back("All monitors"));
    const again = await screen.findByRole("searchbox", { name: "Filter monitors" });
    expect((again as HTMLInputElement).value).toBe("db");
  });

  it("starts unfiltered when it is arrived at afresh, from the rail", async () => {
    render(<App />);
    await screen.findByRole("link", { name: "api" });
    fireEvent.click(screen.getByRole("button", { name: /^Down/ }));
    await open("db");
    fireEvent.click(screen.getByRole("link", { name: "Dashboard" }));
    await screen.findByRole("link", { name: "api" });
    expect(pressedTab()).toBe("All");
  });
});
