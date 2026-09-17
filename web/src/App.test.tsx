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
import { setToolbarSlot, setTopbarSlot } from "./shell/topbarSlot";

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

describe("the app shell", () => {
  it("lands on the dashboard, not on a component gallery", async () => {
    render(<App />);
    // The session resolves first: the shell only renders the dashboard once
    // `/api/v1/auth/me` has returned a real user, and the account footer is
    // where that user becomes visible.
    expect(await screen.findByText(USER.email)).toBeTruthy();
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

  it("moves focus into the wall when a route change swaps the shell away", async () => {
    // Leaving a monitor while the wall is the chosen layout unmounts the
    // shell's <main> and mounts the wall's own one. Without a focus target on
    // the wall the route change is silent for a keyboard user: focus stays on
    // a control in a document that no longer exists.
    //
    // The preference is seeded rather than clicked, which is the honest way to
    // reach this state since SUB-131: the layout switcher is the dashboard's
    // control, and the wall shows no monitor links, so there is no sequence of
    // clicks that puts a monitor on screen with `wall` already chosen. A
    // reload with the stored preference is exactly that sequence, and storage
    // is where the preference genuinely lives.
    window.localStorage.setItem(LAYOUT_STORAGE_KEY, "wall");
    window.history.replaceState(null, "", "/monitors/1");
    render(<App />);
    await waitFor(() =>
      expect(document.querySelector(".mon-detail")).toBeTruthy(),
    );

    fireEvent.keyDown(window, { key: "Escape" });
    await waitFor(() => expect(document.querySelector(".wall")).toBeTruthy());
    await waitFor(() =>
      expect(document.activeElement).toBe(document.querySelector(".wall")),
    );
  });

  /*
   * SUB-131. The layout switcher is the dashboard's control, and it used to
   * appear on every screen — including Incidents and Monitors, where the four
   * options rearranged nothing, and the monitor detail view, where the wall
   * would have replaced a single monitor with a chrome-less grid of all of
   * them.
   *
   * Asserted per route rather than once, because "it is absent somewhere" is
   * the assertion that passes when someone removes it everywhere.
   */
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

  it("reaches the add-monitor form in two clicks, and leaves it with Esc", async () => {
    /*
     * SUB-24 still holds: the primary action must be reachable from the
     * screen you land on, because a form behind a settings page fails the
     * sixty seconds before it is even opened.
     *
     * What changed in SUB-138 is the route to it, not the requirement. The
     * masthead's [+] was global chrome opening a monitor-shaped drawer, which
     * on Notifications meant pressing it added a *monitor* to a screen about
     * channels. Adding a monitor now lives on Monitors, one sidebar click
     * away, where the button sits in the card header of the list it adds to.
     */
    render(<App />);
    await screen.findByText("api");

    fireEvent.click(screen.getByRole("link", { name: "Monitors" }));
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: /add monitor/i }),
      ).toBeTruthy(),
    );

    fireEvent.click(screen.getByRole("button", { name: /add monitor/i }));
    await waitFor(() =>
      expect(screen.getByLabelText(/what should be watched/i)).toBeTruthy(),
    );

    fireEvent.keyDown(window, { key: "Escape" });
    await waitFor(() =>
      expect(screen.queryByLabelText(/what should be watched/i)).toBeNull(),
    );
  });

  /*
   * Every control in the bar reports the state the screen is actually in.
   *
   * The defect this began as: the add form was a *screen*, the render branch
   * preferred the workbench, and pressing Add while the workbench was open lit
   * the add button over a workbench that stayed on screen — a control claiming
   * a state the page did not have.
   *
   * The add button has since left the masthead entirely (SUB-138), so the
   * original pairing cannot be staged any more. The rule underneath is what
   * was always worth asserting and it still applies to every control that
   * remains: a button reads as pressed if and only if the thing it opens is
   * on screen. The workbench is the one toggle left in the bar, and it is
   * checked against its own content rather than against its own attribute —
   * the claim under test cannot also be the evidence for it.
   */
  it("never lets a control claim a state the screen does not have", async () => {
    render(<App />);
    await screen.findByText("api");

    const workbench = screen.getByRole("button", {
      name: "Component workbench",
    });
    /*
     * The workbench's own content, not the button's attribute: the claim
     * under test cannot also be the evidence for it. Its fixture gallery
     * carries a heading no other screen has.
     */
    const gallery = () =>
      screen.queryByText(/fixtures, not live data/i);

    expect(workbench.getAttribute("aria-pressed")).toBe("false");
    expect(gallery()).toBeNull();

    fireEvent.click(workbench);
    await waitFor(() => expect(gallery()).toBeTruthy());
    expect(workbench.getAttribute("aria-pressed")).toBe("true");

    fireEvent.click(workbench);
    await waitFor(() => expect(gallery()).toBeNull());
    expect(workbench.getAttribute("aria-pressed")).toBe("false");
  });

  /*
   * SUB-132: the same button did two different things. On the inventory it
   * opened a drawer; on the dashboard it replaced the whole screen with the
   * form, taking the monitor list with it. Asserted as "the list is still
   * there", because a missing form was never the failure — a disappearing
   * dashboard was.
   *
   * Reached from the Monitors page since SUB-138, which is where adding a
   * monitor now lives. The guarantee is unchanged: the form is an overlay and
   * the list it was opened from survives underneath it.
   */
  it("opens the add form as a drawer over the list, not instead of it", async () => {
    render(<App />);
    await screen.findByText("api");

    fireEvent.click(screen.getByRole("link", { name: "Monitors" }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /add monitor/i })).toBeTruthy(),
    );
    fireEvent.click(screen.getByRole("button", { name: /add monitor/i }));
    await waitFor(() =>
      expect(screen.getByLabelText(/what should be watched/i)).toBeTruthy(),
    );

    // A dialog, and the list it was opened from is still behind it.
    const dialog = screen.getByRole("dialog", { name: /add monitor/i });
    expect(dialog).toBeTruthy();
    expect(screen.getByText("api")).toBeTruthy();

    /*
     * Two surfaces, never three (PR #42, tightened in SUB-138).
     *
     * This used to assert `.card .panel .add-form` — drawer, card, panel,
     * form — which is the third frame Frank pointed at in the drawer
     * screenshot. The card is gone; the drawer's own header is what it was
     * reaching for. The panel stays, so the form is not bare on the drawer
     * background.
     */
    expect(dialog.querySelector(".card")).toBeNull();
    expect(dialog.querySelector(".panel .add-form")).toBeTruthy();
  });

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

    /*
     * Two things Esc could mean at once. The navigation drawer is the newer
     * and more modal of the two, so it goes first; the form must survive that
     * press.
     *
     * The form is reached through Monitors since SUB-138. That route change
     * is what exposed a real gap: `/monitors/new` is a route rather than a
     * flag, and it was missing from the Esc queue entirely — so Esc fell past
     * an open modal form straight through to "leave this screen".
     */
    // On a phone the rail is a drawer, so getting to Monitors means opening
    // it first — and it closes itself on navigation, which is what leaves
    // this test free to reopen it below.
    fireEvent.click(screen.getByRole("button", { name: "Open navigation" }));
    fireEvent.click(await screen.findByRole("link", { name: "Monitors" }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /add monitor/i })).toBeTruthy(),
    );
    fireEvent.click(screen.getByRole("button", { name: /add monitor/i }));
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
