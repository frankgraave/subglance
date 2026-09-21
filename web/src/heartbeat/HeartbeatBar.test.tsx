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

  /*
   * The regression this pair exists for (SUB-29).
   *
   * `width` used to switch measurement off, so every caller's jsdom fallback
   * became the width in the browser as well: the detail view drew a 720px bar
   * inside a 317px panel and pushed a 375px page out to 746px. jsdom has no
   * layout, so a real container width has to be faked — which is exactly the
   * condition the fallback must yield to.
   */
  const withLayout = (width: number, run: () => void) => {
    const realRect = Element.prototype.getBoundingClientRect;
    const realRO = globalThis.ResizeObserver;
    Element.prototype.getBoundingClientRect = function (this: Element) {
      return this.classList.contains("hb-track")
        ? ({ left: 0, top: 0, width, height: 34 } as DOMRect)
        : realRect.call(this);
    };
    // Observing is what the component does after measuring; a no-op class is
    // enough, because the fake width never changes.
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver;
    try {
      run();
    } finally {
      Element.prototype.getBoundingClientRect = realRect;
      globalThis.ResizeObserver = realRO;
    }
  };

  it("sizes itself to its container, not to the width it was handed", () => {
    // A phone-sized panel with a desktop-sized fallback: the container wins.
    withLayout(317, () => {
      render(<HeartbeatBar beats={beats(140)} label="API" width={720} />);
      const svg = document.querySelector("svg")!;
      expect(Number(svg.getAttribute("width"))).toBeLessThanOrEqual(317);
      // 317px at 6 + 3 per column is 35 columns, not the 80 a 720px bar draws.
      expect(document.querySelectorAll(".hb-bar")).toHaveLength(35);
    });
  });

  it("falls back to the given width only where there is no layout to measure", () => {
    render(<HeartbeatBar beats={beats(5)} label="API" width={WIDTH} />);
    expect(document.querySelectorAll(".hb-bar")).toHaveLength(41);
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

  /*
   * The problem the tooltip's partial marker exists for: a bucket holding a
   * handful of the checks its window should contain used to be drawn exactly
   * like a full one, so a gap in the history read as a healthy stretch.
   */
  it("says a bucket is incomplete, and stays quiet when it is not", () => {
    // 41 columns over 820 minute-checks: ~20 per bucket. The oldest stretch
    // loses nine of every ten checks, the newest keeps all of them.
    const all = beats(820);
    const gappy = [
      ...all.slice(0, 400).filter((_, i) => i % 10 === 0),
      ...all.slice(400),
    ];
    render(<HeartbeatBar beats={gappy} label="API" width={WIDTH} />);
    const track = document.querySelector(".hb-track") as HTMLElement;
    track.getBoundingClientRect = () => ({ left: 0, width: WIDTH }) as DOMRect;

    fireEvent.pointerMove(track, { clientX: 2 });
    expect(screen.getByTestId("hb-tooltip").textContent).toContain(
      "Partial data",
    );

    fireEvent.pointerMove(track, { clientX: 40 * 9 + 2 });
    expect(screen.getByTestId("hb-tooltip").textContent).not.toContain(
      "Partial data",
    );
  });

  it("names the unit once and closes a bucketed readout with a total", () => {
    render(<HeartbeatBar beats={beats(200)} label="API" width={WIDTH} />);
    const track = document.querySelector(".hb-track") as HTMLElement;
    track.getBoundingClientRect = () => ({ left: 0, width: WIDTH }) as DOMRect;
    fireEvent.pointerMove(track, { clientX: 40 * 9 + 2 });
    const tip = screen.getByTestId("hb-tooltip");
    // A unit, not the metric name: "latency" is what the row is called, "ms"
    // is what its number is in.
    expect(tip.querySelector(".tooltip-unit")!.textContent).toBe("ms");
    // Named once in the header, so the value must not repeat it.
    expect(tip.querySelector(".tooltip-value")!.textContent).toBe("120");
    const total = tip.querySelector(".tooltip-row--total")!;
    expect(total.textContent).toContain("checks");
    // A healthy bucket does not spend a row saying nothing failed.
    expect(tip.textContent).not.toContain("Failed");
  });

  it("states the column's status in words, not only in the marker colour", () => {
    render(<HeartbeatBar beats={beats(5)} label="API" width={WIDTH} />);
    const track = document.querySelector(".hb-track") as HTMLElement;
    track.getBoundingClientRect = () => ({ left: 0, width: WIDTH }) as DOMRect;
    fireEvent.pointerMove(track, { clientX: 40 * 9 + 2 });
    const tip = screen.getByTestId("hb-tooltip");
    expect(tip.querySelector(".tooltip-status")!.textContent).toBe("Up");
  });

  /*
   * While the stream is down the bar drains its colour, but the tooltip is
   * positioned outside that rule's reach. A column hovered ten minutes after
   * the connection dropped used to report the last known status in the present
   * tense, in a colour that still said "up right now".
   */
  it("stops the tooltip claiming a current status while the stream is stale", () => {
    render(<HeartbeatBar beats={beats(5)} label="API" width={WIDTH} stale />);
    const track = document.querySelector(".hb-track") as HTMLElement;
    track.getBoundingClientRect = () => ({ left: 0, width: WIDTH }) as DOMRect;
    fireEvent.pointerMove(track, { clientX: 40 * 9 + 2 });
    const tip = screen.getByTestId("hb-tooltip");
    expect(tip.querySelector(".tooltip-status")!.textContent).toBe(
      "Not updating",
    );
    expect(tip.querySelector(".tooltip-marker")).toBeNull();
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

/*
 * The chrome (§13). The defect it closes: a reader could see that something
 * failed and had no way at all to tell when — the track carried no time
 * context whatsoever.
 */
describe("HeartbeatBar, framed", () => {
  it("states the window's start and end in the two bottom corners", () => {
    const series = beats(5);
    render(<HeartbeatBar beats={series} label="API" width={WIDTH} framed />);
    const corner = (ts: number) =>
      new Date(ts).toLocaleString(undefined, {
        month: "short",
        day: "numeric",
        hour: "2-digit",
        minute: "2-digit",
      });
    expect(screen.getByTestId("chart-start").textContent).toBe(
      corner(series[0].ts),
    );
    expect(screen.getByTestId("chart-end").textContent).toBe(
      corner(series[4].ts),
    );
  });

  it("labels the corners with the data's window, never with the clock", () => {
    // The series is historic. A chrome that reached for Date.now() would put
    // today's date under a bar drawn from last year's checks.
    render(<HeartbeatBar beats={beats(5)} label="API" width={WIDTH} framed />);
    const now = new Date().toLocaleString(undefined, {
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    });
    expect(screen.getByTestId("chart-end").textContent).not.toBe(now);
    expect(screen.getByTestId("chart-start").textContent).not.toBe(now);
  });

  it("leads with uptime and right-aligns the check breakdown beside it", () => {
    render(
      <HeartbeatBar
        beats={beats(10, (i) => ({ ok: i !== 3, assessment: i !== 3 ? "up" : "down" }))}
        label="API"
        width={WIDTH}
        framed
      />,
    );
    expect(screen.getByTestId("chart-headline").textContent).toBe("90.00%");
    expect(screen.getByTestId("chart-breakdown").textContent).toBe(
      "10 eligible checks · 1 confirmed down",
    );
  });

  it("says so in words when there is nothing to divide", () => {
    // 0/0 is NaN%, and "NaN%" above an empty track reads as a crash rather
    // than as a monitor that has not run yet.
    render(<HeartbeatBar beats={[]} label="API" width={WIDTH} framed />);
    expect(screen.getByTestId("chart-headline").textContent).toBe("—");
    expect(screen.getByTestId("chart-breakdown").textContent).toBe(
      "no checks yet",
    );
    expect(screen.queryByTestId("chart-start")).toBeNull();
  });

  it("fills the legend from the caller rather than inventing one", () => {
    render(
      <HeartbeatBar
        beats={beats(5)}
        label="API"
        width={WIDTH}
        framed
        legend={<span>Bar height is latency</span>}
      />,
    );
    expect(screen.getByText("Bar height is latency")).toBeTruthy();
  });

  it("draws no chrome at all unframed, which is what a list row needs", () => {
    // The chrome is opt-in for a reason: one uptime headline per row, forty
    // rows deep, is the same fact forty times and the row already states it.
    render(<HeartbeatBar beats={beats(5)} label="API" width={WIDTH} />);
    expect(screen.queryByTestId("chart-headline")).toBeNull();
    expect(screen.queryByTestId("chart-grid")).toBeNull();
    expect(document.querySelectorAll(".hb-bar")).toHaveLength(41);
  });
});


describe("maintenance chart denominator", () => {
  it.each(["up", "down"] as const)("renders unknown for maintenance-only %s", (assessment) => {
    render(<HeartbeatBar beats={beats(2, () => ({assessment, maintenance: true, ok: assessment === "up"}))} label="API" width={WIDTH} framed />);
    expect(screen.getByText("No eligible checks")).toBeTruthy();
    expect(document.querySelector(".chart-headline")?.textContent).toBe("—");
  });
  it("excludes maintenance from both percentage and eligible count", () => {
    render(<HeartbeatBar beats={beats(2, (i) => ({assessment: i ? "down" : "up", maintenance: !!i, ok: !i}))} label="API" width={WIDTH} framed />);
    expect(screen.getByText("100.00%")).toBeTruthy();
    expect(screen.getByText("1 eligible check · 0 confirmed down")).toBeTruthy();
  });
});

it("does not count a warning twice in the accessible summary", () => {
  render(<HeartbeatBar label="Mixed" width={WIDTH} beats={beats(3, i => ({
    ok: i === 0, assessment: i === 0 ? "up" : i === 1 ? "warning" : "down",
  }))} />);
  expect(screen.getByRole("group").getAttribute("aria-label")).toContain("1 failed, 1 warning");
});

it.each([false, true])("announces warning assessments truthfully with stale=%s", (stale) => {
  render(<HeartbeatBar beats={beats(1, () => ({ok:false, assessment:"warning"}))} label="API" width={WIDTH} stale={stale}/>);
  fireEvent.focus(screen.getByRole("group"));
  fireEvent.keyDown(screen.getByRole("group"), {key:"End"});
  const live=document.querySelector('[aria-live="polite"]')!;
  expect(live.textContent).toContain("Warning — unconfirmed, no alert");
  expect(live.textContent).not.toContain("failed");
  expect(live.textContent?.includes("Last known")).toBe(stale);
  expect(live.textContent?.includes("Not updating")).toBe(stale);
});

it.each([true, false])("marks a stale %s check announcement as last-known", (ok) => {
  render(<HeartbeatBar beats={beats(1, () => ({ok}))} label="API" width={WIDTH} stale/>);
  fireEvent.focus(screen.getByRole("group"));
  fireEvent.keyDown(screen.getByRole("group"), {key:"End"});
  const live=document.querySelector('[aria-live="polite"]')!;
  expect(live.textContent).toContain(`Last known: ${ok ? "passed" : "failed"}`);
  expect(live.textContent).toContain("Not updating");
});

it("dismisses the selected check before Escape reaches page navigation", () => {
  let pageEscapes=0;
  const onKeyDown=(event:KeyboardEvent)=>{if(event.key==="Escape") pageEscapes++;};
  window.addEventListener("keydown",onKeyDown);
  try {
    render(<HeartbeatBar beats={beats(1)} label="API" width={WIDTH}/>);
    const track=screen.getByRole("group");
    fireEvent.focus(track);
    fireEvent.keyDown(track,{key:"End"});
    expect(screen.getByTestId("hb-tooltip")).toBeTruthy();
    fireEvent.keyDown(track,{key:"Escape"});
    expect(screen.queryByTestId("hb-tooltip")).toBeNull();
    expect(pageEscapes).toBe(0);
    fireEvent.keyDown(track,{key:"Escape"});
    expect(pageEscapes).toBe(1);
  } finally {window.removeEventListener("keydown",onKeyDown);}
});
