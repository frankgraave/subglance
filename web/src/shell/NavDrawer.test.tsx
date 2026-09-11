// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AppShell } from "./AppShell";
import { NavDrawer } from "./NavDrawer";

afterEach(() => {
  cleanup();
  document.body.style.overflow = "";
});

/**
 * jsdom has no media queries at all, so the drawer's "close when the viewport
 * grows" listener needs a stub. It returns a real listener registry rather
 * than a no-op, because the behaviour under test *is* the listener firing.
 */
function stubMatchMedia() {
  const listeners = new Set<(event: MediaQueryListEvent) => void>();
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: true,
    media: query,
    addEventListener: (_: string, fn: (event: MediaQueryListEvent) => void) => void listeners.add(fn),
    removeEventListener: (_: string, fn: (event: MediaQueryListEvent) => void) =>
      void listeners.delete(fn),
  }));
  return {
    /** Simulate the viewport crossing the breakpoint. */
    widen: () => {
      for (const fn of listeners) fn({ matches: false } as MediaQueryListEvent);
    },
  };
}

describe("NavDrawer", () => {
  it("renders nothing at all while closed", () => {
    render(<NavDrawer open={false} onClose={() => {}} />);
    // Not "rendered off-screen": a drawer parked with translateX keeps its
    // links focusable and readable to a screen reader, which is the usual way
    // this pattern is got wrong.
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.queryByRole("navigation")).toBeNull();
  });

  /*
   * The regression CodeRabbit found on PR #3, and it is not theoretical.
   *
   * Opening the drawer marks the shell `inert` in the same commit. The HTML
   * focus fixup rule moves focus off a focused element the instant it becomes
   * inert, so `document.activeElement` read inside the drawer's own effect is
   * already `body` — the opener is gone before we could look at it. Only an
   * element captured by the caller, before the state change, survives.
   *
   * jsdom does not implement the fixup rule, so the test forces the same
   * starting condition by hand: focus has already left the opener by the time
   * the drawer mounts. That is exactly the state a real browser hands us.
   */
  it("returns focus to the opener the caller captured, not to whatever is focused on mount", () => {
    stubMatchMedia();
    const opener = document.createElement("button");
    document.body.append(opener);
    opener.focus();
    expect(document.activeElement).toBe(opener);

    const returnFocusRef = { current: opener as HTMLElement | null };
    // What the browser does when the shell around the opener goes inert.
    opener.blur();
    expect(document.activeElement).toBe(document.body);

    const { unmount } = render(<NavDrawer open onClose={() => {}} returnFocusRef={returnFocusRef} />);
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "Close navigation" }));

    unmount();
    expect(document.activeElement).toBe(opener);
    opener.remove();
  });

  it("is a modal dialog holding the real navigation", () => {
    render(<NavDrawer open onClose={() => {}} />);
    const dialog = screen.getByRole("dialog");
    expect(dialog.getAttribute("aria-modal")).toBe("true");
    expect(dialog.getAttribute("aria-label")).toBe("Navigation");
    // The same sidebar the laptop gets, expanded: you opened it deliberately.
    expect(screen.getByText("Dashboard")).toBeTruthy();
  });

  it("moves focus to the way out, and gives it back on close", () => {
    const opener = document.createElement("button");
    document.body.append(opener);
    opener.focus();

    const { rerender } = render(<NavDrawer open onClose={() => {}} />);
    // The close button, not the first link: a screen reader should hear a way
    // out before it hears five destinations.
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "Close navigation" }));

    rerender(<NavDrawer open={false} onClose={() => {}} />);
    expect(document.activeElement).toBe(opener);
    opener.remove();
  });

  it("closes on the close button and on the scrim", () => {
    const onClose = vi.fn();
    const { container } = render(<NavDrawer open onClose={onClose} />);
    fireEvent.click(screen.getByRole("button", { name: "Close navigation" }));
    fireEvent.click(container.querySelector(".shell-scrim")!);
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it("hides the scrim from assistive technology", () => {
    const { container } = render(<NavDrawer open onClose={() => {}} />);
    // It duplicates the close button, so announcing it would offer a second,
    // unlabelled way to do the same thing.
    expect(container.querySelector(".shell-scrim")!.getAttribute("aria-hidden")).toBe("true");
  });

  it("holds the page still underneath, and releases it on close", () => {
    const { rerender } = render(<NavDrawer open onClose={() => {}} />);
    expect(document.body.style.overflow).toBe("hidden");
    rerender(<NavDrawer open={false} onClose={() => {}} />);
    expect(document.body.style.overflow).toBe("");
  });

  it("closes itself when the viewport grows past the breakpoint", () => {
    const media = stubMatchMedia();
    const onClose = vi.fn();
    render(<NavDrawer open onClose={onClose} />);
    // Rotating a phone to landscape crosses 640px; the scrim must not stay
    // over a layout that now has room for the rail.
    media.widen();
    expect(onClose).toHaveBeenCalledTimes(1);
    vi.unstubAllGlobals();
  });
});

describe("AppShell on a phone", () => {
  const shell = (over: Partial<Parameters<typeof AppShell>[0]> = {}) => (
    <AppShell sidebarCollapsed={false} topbar={<div>bar</div>} {...over}>
      <p>dashboard</p>
    </AppShell>
  );

  it("spends no width on a rail", () => {
    render(shell({ narrow: true }));
    // At 375px a permanent 56px of icons is 15% of the screen, spent on four
    // destinations that do not exist yet.
    expect(screen.queryByRole("navigation")).toBeNull();
  });

  it("still renders the rail on a laptop", () => {
    render(shell({ narrow: false }));
    expect(screen.getByRole("navigation")).toBeTruthy();
  });

  it("makes the page inert behind the open drawer", () => {
    const { container } = render(shell({ narrow: true, navOpen: true }));
    const page = container.querySelector(".shell")!;
    // Content under an overlay that still answers Tab is a trap for anyone
    // not using a mouse.
    expect(page.hasAttribute("inert")).toBe(true);
    expect(screen.getByRole("dialog")).toBeTruthy();
  });

  it("leaves the page interactive when the drawer is shut", () => {
    const { container } = render(shell({ narrow: true, navOpen: false }));
    expect(container.querySelector(".shell")!.hasAttribute("inert")).toBe(false);
  });

  it("never makes a laptop inert, whatever the drawer flag says", () => {
    const { container } = render(shell({ narrow: false, navOpen: true }));
    expect(container.querySelector(".shell")!.hasAttribute("inert")).toBe(false);
  });
});
