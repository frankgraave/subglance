// @vitest-environment jsdom
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { COMPACT_MAX_WIDTH } from "./layout/useMediaQuery";
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
      expect(
        screen
          .getByRole("button", { name: "Compact" })
          .getAttribute("aria-pressed"),
      ).toBe("true"),
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
    await waitFor(() =>
      expect(screen.getByRole("navigation", { name: "Primary" })).toBeTruthy(),
    );
  });

  it("collapses the sidebar with Ctrl+B and keeps it reachable", async () => {
    render(<App />);
    await screen.findByText("api");
    fireEvent.keyDown(window, { key: "b", ctrlKey: true });
    await waitFor(() =>
      expect(
        document.querySelector('.shell-sidebar[data-collapsed="true"]'),
      ).toBeTruthy(),
    );
    // Collapsed is a rail: the destination is still there to be clicked.
    expect(screen.getByText("Dashboard")).toBeTruthy();
  });

  it("reaches the add-monitor form from the topbar, and leaves it with Esc", async () => {
    render(<App />);
    await screen.findByText("api");

    // SUB-24: the primary action must be reachable from the screen you land
    // on. A form behind a settings page fails the sixty seconds before it is
    // even opened.
    fireEvent.click(screen.getByRole("button", { name: "Add a monitor" }));
    await waitFor(() =>
      expect(screen.getByLabelText(/what should be watched/i)).toBeTruthy(),
    );

    fireEvent.keyDown(window, { key: "Escape" });
    await waitFor(() =>
      expect(screen.queryByLabelText(/what should be watched/i)).toBeNull(),
    );
  });

  it("never shows one content mode while a control claims the other", async () => {
    render(<App />);
    await screen.findByText("api");

    const workbench = screen.getByRole("button", {
      name: "Component workbench",
    });
    const add = screen.getByRole("button", { name: "Add a monitor" });

    fireEvent.click(workbench);
    await waitFor(() =>
      expect(workbench.getAttribute("aria-pressed")).toBe("true"),
    );

    // The render branch prefers the workbench, so without the toggles clearing
    // each other this left the workbench on screen with the add button lit —
    // and Esc then closed a form nobody could see.
    fireEvent.click(add);
    await waitFor(() =>
      expect(screen.getByLabelText(/what should be watched/i)).toBeTruthy(),
    );
    expect(workbench.getAttribute("aria-pressed")).toBe("false");

    // And the other way round: opening the workbench unlights the add button.
    fireEvent.click(workbench);
    await waitFor(() =>
      expect(screen.queryByLabelText(/what should be watched/i)).toBeNull(),
    );
    expect(add.getAttribute("aria-pressed")).toBe("false");
  });
});

/**
 * The phone drawer, wired into the real shell (SUB-66).
 *
 * `NavDrawer.test.tsx` covers the drawer as a component. What is untested
 * there is the wiring: that one topbar button means "collapse the rail" on a
 * laptop and "open the drawer" on a phone, and that Esc prefers the drawer
 * over everything else it could have meant. Both are decided in `App.tsx`, so
 * both have to be asserted from `App.tsx`.
 */
describe("the app shell on a phone", () => {
  /**
   * A `matchMedia` that actually answers the query it is given.
   *
   * A stub that reports `matches: true` for everything also reports it for
   * `(max-width: 9999px)`, so these tests would pass on a shell that used the
   * wrong breakpoint. It evaluates `(max-width: N)` against a width we control
   * and notifies `change` listeners when that width moves, which is what lets
   * a test cross the breakpoint the way rotating a phone does.
   */
  function stubViewport(initialWidth: number) {
    let width = initialWidth;
    const listeners = new Set<{
      query: string;
      notify: (event: MediaQueryListEvent) => void;
    }>();
    // Only width queries get an answer from the viewport. Anything else the
    // app asks — `prefers-color-scheme`, say — is not what this stub is about,
    // and answering `true` to all of it is how a stub stops proving anything.
    const evaluate = (query: string) => {
      const max = /\(max-width:\s*(\d+)px\)/.exec(query);
      if (max === null) return false;
      return width <= Number(max[1]);
    };

    vi.stubGlobal("matchMedia", (query: string) => {
      const list = {
        get matches() {
          return evaluate(query);
        },
        media: query,
        addEventListener(
          _type: string,
          notify: (event: MediaQueryListEvent) => void,
        ) {
          listeners.add({ query, notify });
        },
        removeEventListener(
          _type: string,
          notify: (event: MediaQueryListEvent) => void,
        ) {
          for (const entry of listeners) {
            if (entry.notify === notify) listeners.delete(entry);
          }
        },
        addListener() {},
        removeListener() {},
        onchange: null,
        dispatchEvent: () => false,
      };
      return list;
    });

    return {
      /** Resize, then tell everyone who asked to be told. */
      setWidth(next: number) {
        width = next;
        for (const { query, notify } of [...listeners]) {
          notify({
            matches: evaluate(query),
            media: query,
          } as MediaQueryListEvent);
        }
      },
    };
  }

  /** A phone, one pixel below the only breakpoint the dashboard has. */
  function stubNarrowViewport() {
    return stubViewport(COMPACT_MAX_WIDTH - 1);
  }

  it("opens a drawer from the topbar button instead of collapsing a rail", async () => {
    stubNarrowViewport();
    render(<App />);
    await screen.findByText("api");

    // The rail is gone, so the button cannot mean "collapse" — and it says so.
    expect(document.querySelector(".shell-sidebar")).toBeNull();
    const button = screen.getByRole("button", { name: "Open navigation" });

    fireEvent.click(button);
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "Navigation" })).toBeTruthy(),
    );
    // The drawer holds the navigation the rail would have held.
    expect(screen.getByRole("navigation", { name: "Primary" })).toBeTruthy();
    // And focus came with it: the shell behind the drawer is inert, so a
    // keyboard user left standing on the old button has nowhere to go. Scoped
    // to the dialog because the topbar button is also called "Close
    // navigation" while the drawer is open.
    const drawer = screen.getByRole("dialog", { name: "Navigation" });
    expect(document.activeElement).toBe(
      within(drawer).getByRole("button", { name: "Close navigation" }),
    );
  });

  it("closes the drawer once the viewport is wide enough for the rail", async () => {
    const viewport = stubNarrowViewport();
    render(<App />);
    await screen.findByText("api");

    fireEvent.click(screen.getByRole("button", { name: "Open navigation" }));
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "Navigation" })).toBeTruthy(),
    );

    // Rotating to landscape crosses the breakpoint. The rail is back, so the
    // drawer — and the scrim over a page that no longer needs covering — has
    // to go.
    act(() => viewport.setWidth(COMPACT_MAX_WIDTH + 1));
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "Navigation" })).toBeNull(),
    );
    expect(document.querySelector(".shell-sidebar")).toBeTruthy();

    // And it stays gone on the way back: unmounting the drawer is not the same
    // as closing it, and a drawer that reopens itself on rotation is the bug
    // that hides behind that difference.
    act(() => viewport.setWidth(COMPACT_MAX_WIDTH - 1));
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Open navigation" }),
      ).toBeTruthy(),
    );
    expect(screen.queryByRole("dialog", { name: "Navigation" })).toBeNull();
  });

  it("closes the drawer on Esc before Esc means anything else", async () => {
    stubNarrowViewport();
    render(<App />);
    await screen.findByText("api");

    // Two things Esc could mean at once. The drawer is the newer and more
    // modal of the two, so it goes first; the form must survive that press.
    fireEvent.click(screen.getByRole("button", { name: "Add a monitor" }));
    await waitFor(() =>
      expect(screen.getByLabelText(/what should be watched/i)).toBeTruthy(),
    );
    fireEvent.click(screen.getByRole("button", { name: "Open navigation" }));
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "Navigation" })).toBeTruthy(),
    );

    fireEvent.keyDown(window, { key: "Escape" });
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "Navigation" })).toBeNull(),
    );
    expect(screen.getByLabelText(/what should be watched/i)).toBeTruthy();

    // And only then does Esc get to mean "leave the add form".
    fireEvent.keyDown(window, { key: "Escape" });
    await waitFor(() =>
      expect(screen.queryByLabelText(/what should be watched/i)).toBeNull(),
    );
  });

  it("gives focus back to the button that opened it", async () => {
    stubNarrowViewport();
    render(<App />);
    await screen.findByText("api");

    const button = screen.getByRole("button", { name: "Open navigation" });
    button.focus();
    fireEvent.click(button);
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "Navigation" })).toBeTruthy(),
    );

    fireEvent.keyDown(window, { key: "Escape" });
    // The same button, found again by the name it now carries: a keyboard user
    // who opened the drawer must not be dumped back on `body`.
    await waitFor(() =>
      expect(document.activeElement).toBe(
        screen.getByRole("button", { name: "Open navigation" }),
      ),
    );
  });

  it("still answers Ctrl+B, and on a phone that opens the drawer", async () => {
    stubNarrowViewport();
    render(<App />);
    await screen.findByText("api");

    // The shortcut is not desktop-only; it toggles whatever "the navigation"
    // currently is. The laptop half of this is asserted above.
    fireEvent.keyDown(window, { key: "b", ctrlKey: true });
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "Navigation" })).toBeTruthy(),
    );

    fireEvent.keyDown(window, { key: "b", ctrlKey: true });
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "Navigation" })).toBeNull(),
    );

    // Cmd+B is the same shortcut on a Mac, and it is a separate modifier on
    // the event: asserting only ctrlKey leaves half the users uncovered.
    fireEvent.keyDown(window, { key: "b", metaKey: true });
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "Navigation" })).toBeTruthy(),
    );

    fireEvent.keyDown(window, { key: "b", metaKey: true });
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "Navigation" })).toBeNull(),
    );
  });
});
