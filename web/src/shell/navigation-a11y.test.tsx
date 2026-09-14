// @vitest-environment jsdom
import { useRef, useState } from "react";
import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { AppShell } from "./AppShell";
import { documentTitle, useDocumentTitle } from "./documentTitle";
import { useRouteFocus } from "./useRouteFocus";

/**
 * SUB-100 part 3: a client-side navigation used to be silent.
 *
 * `App` scrolled to the top and changed nothing else — focus stayed on the
 * link that had just been replaced, the tab kept the hard-coded title from
 * `index.html` so every back-history entry read the same, and there was no
 * skip link even though the shell was written as if one existed.
 *
 * These are behaviour tests on the three pieces rather than on `App`, which
 * cannot mount without a session, a query client and a live connection.
 */

afterEach(() => {
  cleanup();
  document.title = "SubGlance";
});

describe("the tab says which screen you are on", () => {
  it("leads with the page, because a tab truncates from the right", () => {
    expect(documentTitle("Monitors")).toBe("Monitors — SubGlance");
  });

  it("falls back to the bare product name rather than a dangling dash", () => {
    expect(documentTitle(null)).toBe("SubGlance");
    expect(documentTitle("   ")).toBe("SubGlance");
  });

  it("retitles on navigation and lets go on unmount", () => {
    function Screen({ page }: { page: string }) {
      useDocumentTitle(page);
      return null;
    }
    const view = render(<Screen page="Monitors" />);
    expect(document.title).toBe("Monitors — SubGlance");
    view.rerender(<Screen page="Monitor" />);
    expect(document.title).toBe("Monitor — SubGlance");
    view.unmount();
    expect(document.title).toBe("SubGlance");
  });
});

describe("a route change moves focus into the new screen", () => {
  function Harness({ start }: { start: string }) {
    const [route, setRoute] = useState(start);
    const ref = useRef<HTMLElement | null>(null);
    useRouteFocus(route, ref);
    return (
      <>
        <button type="button" onClick={() => setRoute("/monitors/7")}>
          open
        </button>
        <main ref={ref} tabIndex={-1} data-testid="main">
          {route}
        </main>
      </>
    );
  }

  it("does not steal focus on first render", () => {
    render(<Harness start="/" />);
    expect(document.activeElement).toBe(document.body);
  });

  it("focuses the new screen when the route actually changes", () => {
    render(<Harness start="/" />);
    const opener = screen.getByRole("button", { name: "open" });
    opener.focus();
    act(() => opener.click());
    expect(document.activeElement).toBe(screen.getByTestId("main"));
  });

  it("leaves focus alone when a re-render reports the same route", () => {
    function Same() {
      const [, bump] = useState(0);
      const ref = useRef<HTMLElement | null>(null);
      useRouteFocus("/", ref);
      return (
        <>
          <button type="button" onClick={() => bump((n) => n + 1)}>
            bump
          </button>
          <main ref={ref} tabIndex={-1} data-testid="main" />
        </>
      );
    }
    render(<Same />);
    const button = screen.getByRole("button", { name: "bump" });
    button.focus();
    act(() => button.click());
    // A dashboard that re-renders on every heartbeat must not yank focus out
    // of whatever the user is doing.
    expect(document.activeElement).toBe(button);
  });
});

describe("the skip link", () => {
  const shell = (props: Partial<Parameters<typeof AppShell>[0]> = {}) =>
    render(
      <AppShell sidebarCollapsed={false} topbar={<div />} {...props}>
        <p>content</p>
      </AppShell>,
    );

  it("is the first focusable element and points at main", () => {
    const { container } = shell();
    const focusable = container.querySelectorAll("a[href], button, [tabindex]");
    const first = focusable[0] as HTMLAnchorElement;
    expect(first.getAttribute("href")).toBe("#shell-main");
    expect(container.querySelector("main")?.id).toBe("shell-main");
  });

  it("gives main a focus target that is not a tab stop of its own", () => {
    const { container } = shell();
    expect(container.querySelector("main")?.getAttribute("tabindex")).toBe("-1");
  });

  it("is withdrawn while the drawer is open, because there is nothing to skip to", () => {
    const { container } = shell({ narrow: true, navOpen: true, onNavClose: () => {} });
    expect(container.querySelector(".shell-skip")).toBeNull();
  });
});
