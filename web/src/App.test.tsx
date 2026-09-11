// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { LAYOUT_STORAGE_KEY } from "./shell/preferences";

/**
 * The shell as a whole. These are the acceptance criteria of SUB-64 rather
 * than unit tests: that you land on the product and not a gallery, that the
 * layout survives a reload, and that the wall really drops the chrome.
 */

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
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({
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
      }),
    }),
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("the app shell", () => {
  it("lands on the dashboard, not on a component gallery", async () => {
    render(<App />);
    expect(await screen.findByText("api")).toBeTruthy();
    // The tab bar this replaced made the workbench the front door.
    expect(screen.queryByRole("tab")).toBeNull();
    expect(screen.getByRole("navigation", { name: "Primary" })).toBeTruthy();
  });

  it("remembers the chosen layout across a reload", async () => {
    const { unmount } = render(<App />);
    await screen.findByText("api");
    fireEvent.click(screen.getByRole("button", { name: "Compact" }));
    expect(window.localStorage.getItem(LAYOUT_STORAGE_KEY)).toBe("compact");
    unmount();

    // A fresh mount is what a reload looks like from here.
    render(<App />);
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Compact" }).getAttribute("aria-pressed")).toBe(
        "true",
      ),
    );
  });

  it("drops both sidebar and topbar on the status wall, and Esc brings them back", async () => {
    render(<App />);
    await screen.findByText("api");
    fireEvent.click(screen.getByRole("button", { name: "Status wall" }));

    await waitFor(() => expect(document.querySelector(".wall")).toBeTruthy());
    // That the chrome is gone is the entire reason this layout exists.
    expect(screen.queryByRole("navigation", { name: "Primary" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Status wall" })).toBeNull();

    fireEvent.keyDown(window, { key: "Escape" });
    await waitFor(() => expect(screen.getByRole("navigation", { name: "Primary" })).toBeTruthy());
  });

  it("collapses the sidebar with Ctrl+B and keeps it reachable", async () => {
    render(<App />);
    await screen.findByText("api");
    fireEvent.keyDown(window, { key: "b", ctrlKey: true });
    await waitFor(() =>
      expect(document.querySelector('.shell-sidebar[data-collapsed="true"]')).toBeTruthy(),
    );
    // Collapsed is a rail: the destination is still there to be clicked.
    expect(screen.getByText("Dashboard")).toBeTruthy();
  });
});
