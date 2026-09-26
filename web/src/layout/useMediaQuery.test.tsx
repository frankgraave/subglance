// @vitest-environment jsdom
import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  SIDEBAR_VETO_MAX_WIDTH,
  useCompactViewport,
  useMediaQuery,
  useSidebarSqueeze,
} from "./useMediaQuery";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

/**
 * A controllable `matchMedia`. jsdom does not implement one at all, so every
 * test that cares about the breakpoint has to bring its own.
 */
function stubMatchMedia(matches: boolean) {
  const listeners = new Set<() => void>();
  let current = matches;
  vi.stubGlobal("matchMedia", (query: string) => ({
    media: query,
    get matches() {
      return current;
    },
    addEventListener: (_: string, fn: () => void) => listeners.add(fn),
    removeEventListener: (_: string, fn: () => void) => listeners.delete(fn),
  }));
  return {
    set(next: boolean) {
      current = next;
      for (const fn of listeners) fn();
    },
    listenerCount: () => listeners.size,
  };
}

function Probe() {
  return <span>{useCompactViewport() ? "compact" : "wide"}</span>;
}

describe("useMediaQuery", () => {
  it("reports the match on the first render, without a correction pass", () => {
    stubMatchMedia(true);
    render(<Probe />);
    expect(screen.getByText("compact")).toBeTruthy();
  });

  it("follows the viewport when it changes", () => {
    const media = stubMatchMedia(false);
    render(<Probe />);
    expect(screen.getByText("wide")).toBeTruthy();

    act(() => media.set(true));
    expect(screen.getByText("compact")).toBeTruthy();
  });

  it("unsubscribes on unmount", () => {
    const media = stubMatchMedia(false);
    const { unmount } = render(<Probe />);
    expect(media.listenerCount()).toBe(1);
    unmount();
    expect(media.listenerCount()).toBe(0);
  });

  it("falls back to false where matchMedia does not exist", () => {
    // jsdom's default. The desktop layout is the safer wrong answer.
    function Raw() {
      return <span>{useMediaQuery("(max-width: 1px)") ? "yes" : "no"}</span>;
    }
    render(<Raw />);
    expect(screen.getByText("no")).toBeTruthy();
  });
});

/**
 * A `matchMedia` that answers `(max-width: N)` against a fixed width, so the
 * two queries `useSidebarSqueeze` asks get different answers the way a real
 * viewport gives them.
 */
function stubWidth(width: number) {
  vi.stubGlobal("matchMedia", (query: string) => {
    const max = /\(max-width:\s*(\d+)px\)/.exec(query);
    return {
      media: query,
      matches: max !== null && width <= Number(max[1]),
      addEventListener: () => {},
      removeEventListener: () => {},
    };
  });
}

function SqueezeProbe({ collapsed }: { collapsed: boolean }) {
  return <span>{useSidebarSqueeze(collapsed) ? "squeezed" : "fits"}</span>;
}

describe("useSidebarSqueeze (SUB-149)", () => {
  it.each([
    // [viewport, sidebar collapsed, expected]
    [641, false, "squeezed"],
    [SIDEBAR_VETO_MAX_WIDTH, false, "squeezed"],
    [SIDEBAR_VETO_MAX_WIDTH + 1, false, "fits"],
    // The rail leaves the column the row layout was designed into.
    [641, true, "fits"],
    // Below the breakpoint the phone veto owns the answer, not this one.
    [640, false, "fits"],
  ] as const)("at %ipx, collapsed=%s: %s", (width, collapsed, expected) => {
    stubWidth(width);
    render(<SqueezeProbe collapsed={collapsed} />);
    expect(screen.getByText(expected)).toBeTruthy();
  });
});
