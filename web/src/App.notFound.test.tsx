// @vitest-environment jsdom
/*
 * SUB-177: an address that names no screen says so, and the sign-in screen
 * names itself in the tab.
 *
 * Both used to read "Dashboard". An unknown path drew the dashboard under the
 * wrong address, so a stale link looked as though it worked; the sign-in form
 * sat under a tab that named a screen the visitor could not see yet.
 */
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { disabledWatchdog } from "./watchdog/fixtures";

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

const USER = { id: 1, email: "me@example.com", role: "admin", created_at: "2026-09-01T00:00:00Z" };
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

let signedIn = true;
let setupRequired = false;

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

beforeEach(() => {
  signedIn = true;
  setupRequired = false;
  window.history.replaceState(null, "", "/");
  window.localStorage.clear();
  document.title = "SubGlance";
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
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
    vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input).split("?")[0];
      if (path === "/api/v1/auth/me") {
        return signedIn ? json(USER) : json({ error: "authentication required" }, 401);
      }
      if (path === "/api/v1/setup") return json({ setup_required: setupRequired });
      if (path === "/api/v1/watchdog") return json(disabledWatchdog);
      return json({ monitors: [MONITOR] });
    }),
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("an address that names no screen", () => {
  it("says so under its own title, keeps the address, and lights no section", async () => {
    window.history.replaceState(null, "", "/this-does-not-exist");
    render(<App />);

    const heading = await screen.findByRole("heading", { level: 1, name: "Page not found" });
    expect(document.querySelector(".shell-topbar")!.contains(heading)).toBe(true);
    expect(document.querySelectorAll("h1")).toHaveLength(1);
    expect(document.title).toBe("Page not found \u2014 SubGlance");
    // The typed address is quoted, and stays in the bar.
    expect(screen.getByText("/this-does-not-exist")).toBeTruthy();
    expect(window.location.pathname).toBe("/this-does-not-exist");
    // The dashboard is not drawn behind it.
    expect(screen.queryByText("api")).toBeNull();
    expect(document.querySelectorAll(".shell-sidebar [aria-current='page']")).toHaveLength(0);
  });

  it("links to the dashboard, and following the link draws it", async () => {
    window.history.replaceState(null, "", "/monitors/1/extra");
    render(<App />);

    const link = await screen.findByRole("link", { name: "Go to the dashboard" });
    expect(link.getAttribute("href")).toBe("/");
    fireEvent.click(link);

    await waitFor(() => expect(window.location.pathname).toBe("/"));
    expect(await screen.findByText("api")).toBeTruthy();
    expect(document.title).toBe("Dashboard \u2014 SubGlance");
  });
});

describe("the tab while signed out", () => {
  it("names the sign-in screen, not the dashboard behind it", async () => {
    signedIn = false;
    render(<App />);

    await screen.findByRole("heading", { level: 1, name: "Sign in" });
    await waitFor(() => expect(document.title).toBe("Sign in \u2014 SubGlance"));
  });

  it("names the setup screen on a first run", async () => {
    signedIn = false;
    setupRequired = true;
    render(<App />);

    await screen.findByRole("heading", { level: 1, name: "Set up this instance" });
    await waitFor(() => expect(document.title).toBe("Set up this instance \u2014 SubGlance"));
  });
});
