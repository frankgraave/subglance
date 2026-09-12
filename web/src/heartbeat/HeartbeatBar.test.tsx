// @vitest-environment jsdom
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { HeartbeatBar } from "./HeartbeatBar";
import { TOOLTIP_MIN_WIDTH, type Beat } from "./model";

afterEach(cleanup);

const beats = (
  n: number,
  over: (i: number) => Partial<Beat> = () => ({}),
): Beat[] =>
  Array.from({ length: n }, (_, i) => ({
    ts: 1_700_000_000_000 + i * 60_000,
    ok: true,
    latencyMs: 120,
    ...over(i),
  }));

/** jsdom has no layout, so the width is passed in explicitly. */
const WIDTH = 366; // 41 columns at 6 + 3

describe("HeartbeatBar", () => {
  it("draws one column per slot and puts the newest on the right", () => {
    render(<HeartbeatBar beats={beats(5)} label="API" width={WIDTH} />);
    const bars = document.querySelectorAll(".hb-bar");
    expect(bars).toHaveLength(41);
    // 36 empty slots pad the left, the 5 real beats sit at the end.
    expect(bars[0].getAttribute("data-status")).toBe("empty");
    expect(bars[40].getAttribute("data-status")).toBe("up");
  });

  it("stays readable at 500 checks: still one column per slot, none dropped", () => {
    render(<HeartbeatBar beats={beats(500)} label="API" width={WIDTH} />);
    expect(document.querySelectorAll(".hb-bar")).toHaveLength(41);
    const rows = within(screen.getByRole("table")).getAllByRole("row");
    expect(rows.length).toBe(42); // header + 41 buckets
  });

  it("draws a failure at full height and taller than any healthy bar", () => {
    render(
      <HeartbeatBar
        beats={beats(4, (i) =>
          i === 1 ? { ok: false, error: "connection refused" } : {},
        )}
        label="API"
        width={WIDTH}
      />,
    );
    const heightOf = (status: string) =>
      Number(
        document
          .querySelector(`.hb-bar[data-status="${status}"]`)!
          .getAttribute("height"),
      );
    expect(heightOf("down")).toBeGreaterThan(heightOf("up"));
  });

  it("shows exact time, latency and error on hover", () => {
    render(
      <HeartbeatBar
        beats={beats(2, (i) =>
          i === 1
            ? { ok: false, error: "connection refused", statusCode: 502 }
            : {},
        )}
        label="API"
        width={WIDTH}
      />,
    );
    const track = document.querySelector(".hb-track") as HTMLElement;
    track.getBoundingClientRect = () => ({ left: 0, width: WIDTH }) as DOMRect;
    // Column 40 is the newest beat, which failed.
    fireEvent.pointerMove(track, { clientX: 40 * 9 + 2 });
    const tip = screen.getByTestId("hb-tooltip");
    expect(tip.textContent).toContain("Failed");
    expect(tip.textContent).toContain("connection refused");
    expect(tip.textContent).toContain("502");
  });

  it("lets the tooltip overhang a narrow track rather than clipping it", () => {
    // A 60px track hard against the right edge of a 1024px jsdom window: the
    // tooltip is wider than the track, so it has to escape it to stay whole.
    render(<HeartbeatBar beats={beats(7)} label="API" width={60} />);
    const track = document.querySelector(".hb-track") as HTMLElement;
    track.getBoundingClientRect = () => ({ left: 900, width: 60 }) as DOMRect;
    fireEvent.pointerMove(track, { clientX: 902 });
    const tip = screen.getByTestId("hb-tooltip") as HTMLElement;
    // Right edge of the tooltip, in viewport coordinates, stays inside the window.
    const left = Number.parseFloat(tip.style.left);
    expect(900 + left + 148).toBeLessThanOrEqual(window.innerWidth);
    // ...and it is allowed to start left of the track, which the old
    // track-relative clamp forbade.
    expect(left).toBeLessThan(0);
  });

  it("re-clamps an open tooltip when the viewport shrinks under it", () => {
    render(<HeartbeatBar beats={beats(7)} label="API" width={60} />);
    const track = document.querySelector(".hb-track") as HTMLElement;
    track.getBoundingClientRect = () => ({ left: 900, width: 60 }) as DOMRect;
    fireEvent.pointerMove(track, { clientX: 902 });
    const tip = screen.getByTestId("hb-tooltip") as HTMLElement;
    const before = Number.parseFloat(tip.style.left);

    // The window narrows while the tooltip stays open: nothing about the
    // active column changes, only the edge it has to stay inside of.
    const width = window.innerWidth;
    try {
      window.innerWidth = 960;
      fireEvent(window, new Event("resize"));

      const after = Number.parseFloat(tip.style.left);
      expect(after).toBeLessThan(before);
      expect(900 + after + TOOLTIP_MIN_WIDTH).toBeLessThanOrEqual(960);
    } finally {
      window.innerWidth = width;
    }
  });

  it("re-clamps an open tooltip when the track scrolls sideways", () => {
    render(<HeartbeatBar beats={beats(7)} label="API" width={60} />);
    const track = document.querySelector(".hb-track") as HTMLElement;
    track.getBoundingClientRect = () => ({ left: 100, width: 60 }) as DOMRect;
    fireEvent.pointerMove(track, { clientX: 102 });
    const tip = screen.getByTestId("hb-tooltip") as HTMLElement;
    const before = Number.parseFloat(tip.style.left);

    // A scrolling ancestor drags the track towards the right edge. The offset
    // is track-relative, so it has to shrink to keep the same viewport edge.
    track.getBoundingClientRect = () => ({ left: 950, width: 60 }) as DOMRect;
    fireEvent.scroll(document, {});

    const after = Number.parseFloat(tip.style.left);
    expect(after).toBeLessThan(before);
    expect(950 + after + TOOLTIP_MIN_WIDTH).toBeLessThanOrEqual(
      window.innerWidth,
    );
  });

  it("is keyboard navigable and announces the focused check", () => {
    render(<HeartbeatBar beats={beats(3)} label="API" width={WIDTH} />);
    const track = document.querySelector(".hb-track") as HTMLElement;
    expect(track.getAttribute("tabindex")).toBe("0");
    fireEvent.focus(track);
    fireEvent.keyDown(track, { key: "ArrowLeft" });
    const live = document.querySelector('[aria-live="polite"]')!;
    expect(live.textContent).toContain("120 ms");
    fireEvent.keyDown(track, { key: "Escape" });
    expect(live.textContent).toBe("");
  });

  it("hides the pixels from assistive tech and offers the table instead", () => {
    render(<HeartbeatBar beats={beats(3)} label="API" width={WIDTH} />);
    expect(document.querySelector("svg")!.getAttribute("aria-hidden")).toBe(
      "true",
    );
    expect(screen.getByRole("table")).toBeDefined();
    expect(
      document.querySelector(".hb-track")!.getAttribute("aria-label"),
    ).toContain("3 checks");
  });

  it("degrades to an empty track instead of blowing up on no data", () => {
    render(<HeartbeatBar beats={[]} label="API" width={WIDTH} />);
    const bars = document.querySelectorAll(".hb-bar");
    expect(bars).toHaveLength(41);
    expect(
      [...bars].every((b) => b.getAttribute("data-status") === "empty"),
    ).toBe(true);
    expect(
      within(screen.getByRole("table")).queryAllByRole("row"),
    ).toHaveLength(1);
  });

  it("marks checks without a latency as unknown rather than instant", () => {
    render(
      <HeartbeatBar
        beats={beats(1, () => ({ latencyMs: null }))}
        label="API"
        width={WIDTH}
      />,
    );
    const bar = document.querySelectorAll(".hb-bar")[40];
    expect(bar.getAttribute("data-status")).toBe("unknown");
    expect(Number(bar.getAttribute("height"))).toBeGreaterThan(2);
  });

  it("animates only a genuinely new beat, not a re-render of the same series", () => {
    const series = beats(3);
    const { rerender } = render(
      <HeartbeatBar beats={series} label="API" width={WIDTH} />,
    );
    rerender(<HeartbeatBar beats={[...series]} label="API" width={WIDTH} />);
    expect(document.querySelector(".hb-bar--new")).toBeNull();

    rerender(
      <HeartbeatBar
        beats={[...series, { ts: 1_700_000_999_000, ok: true, latencyMs: 90 }]}
        label="API"
        width={WIDTH}
      />,
    );
    expect(document.querySelector(".hb-bar--new")).not.toBeNull();
  });
});
