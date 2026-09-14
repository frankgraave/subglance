import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  barHeight,
  latencyCeiling,
  slotCountFor,
  slotStatus,
  summarise,
  toSlots,
  TOOLTIP_MIN_WIDTH,
  tooltipLeft,
  type Beat,
  type Slot,
} from "./model";
import { Tooltip, type TooltipRow } from "../components/Tooltip";
import { Chart } from "../components/Chart";
import type { ChipStatus } from "../components/Chip";
import type { ReactNode } from "react";

export type HeartbeatBarProps = {
  /** Checks oldest first, newest last. */
  beats: Beat[];
  /** Names the graphic for assistive technology, e.g. the monitor's name. */
  label: string;
  /** Track height in pixels. */
  height?: number;
  barWidth?: number;
  gap?: number;
  /**
   * Width to use when the container cannot be measured — jsdom and SSR report
   * every element as 0 wide. Never an override: wherever there is real layout
   * the bar sizes itself to its container, because a fixed pixel width is a
   * guess about the viewport and the guess is wrong on a phone.
   */
  width?: number;
  className?: string;
  /**
   * Whether this bar is the accessible source of the check history.
   *
   * Default `true`, which is the detail view: the track takes focus, arrow
   * keys walk the columns, and a `<table>` in the `figcaption` carries every
   * slot as text.
   *
   * `false` is for list layouts, where the same bar is repeated once per
   * monitor and all of it turns into cost. Measured on a 200-monitor Rows
   * render: 402 tab stops, so reaching the last row's link took ~400 Tab
   * presses, and 5,600 screen-reader-only `<tr>` elements — a DOM three times
   * the ~11k budget the decision not to virtualise rests on (MonitorRow,
   * DESIGN.md §10). None of it adds a fact: the row already states status,
   * latency, uptime and the failure reason in text. So a non-interactive bar
   * draws the pixels and nothing else, hidden from assistive technology and
   * out of the tab order, and the detail view keeps the full instrument.
   */
  interactive?: boolean;
  /**
   * True when the live stream is down. The bar itself drains its colour from a
   * `data-conn` ancestor, but the tooltip states a status in words and in the
   * present tense, so it is told directly and stops making that claim.
   */
  stale?: boolean;
  /**
   * Wraps the track in the chart chrome (§13): uptime above it, the check
   * breakdown right-aligned beside that, and the window's start and end in the
   * two bottom corners.
   *
   * Off by default, and that is the point. The bar is repeated once per
   * monitor in list layouts, where the row already states uptime in text and a
   * second copy of it above every track would be the same fact forty times.
   * The detail view, where the bar is the subject rather than a column, is
   * where the chrome pays for itself: without it a reader sees that something
   * failed and has no way to tell when.
   */
  framed?: boolean;
  /**
   * The legend below the plot, when framed. A slot rather than content: what
   * needs naming depends on the series, and the legend component itself is not
   * this component's to build.
   */
  legend?: ReactNode;
};

const formatTime = (ts: number) =>
  new Date(ts).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });

/**
 * The corner times under a framed track.
 *
 * Shorter than the tooltip's format on purpose: a corner label states where
 * the window begins and ends, and a reader who wants the second a check landed
 * hovers the column that holds it. The date is kept because a heartbeat window
 * routinely spans midnight, and "23:58 → 00:04" with no date is a range that
 * could be six minutes or thirty hours.
 */
const formatCorner = (ts: number) =>
  new Date(ts).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });

const formatLatency = (ms: number | null) =>
  ms === null
    ? "no timing"
    : ms >= 1000
      ? `${(ms / 1000).toFixed(2)} s`
      : `${ms} ms`;

/**
 * The unit a latency is best read in, or undefined when there is none to name.
 *
 * Paired with `latencyValue`: together they split "142 ms" into a header unit
 * and a bare number, so a tooltip of three latencies does not spend part of
 * its width repeating the same two letters on every row.
 */
const latencyUnit = (ms: number | null): string | undefined =>
  ms === null ? undefined : ms >= 1000 ? "s" : "ms";

/** The number alone, in the unit `latencyUnit` names for it. */
const latencyValue = (ms: number | null): string =>
  ms === null ? "no timing" : ms >= 1000 ? (ms / 1000).toFixed(2) : `${ms}`;

/**
 * The readout for one column: its rows, and the unit its numbers are in.
 *
 * A bucket is not one series but a fold of several checks, so the rows state
 * what the fold hid: the slowest latency it contains, and how many of its
 * checks failed. A bucket with no failures says so by omission — a "0 failed"
 * row in a healthy tooltip is a number to read and dismiss on every hover.
 *
 * The unit travels with the rows because the Tooltip names it once in its
 * header, and that header is a claim about *every* value below it. So it is
 * only set when the rows agree on one: a bucket that also reports a count of
 * failed checks has no single unit, and its latency then carries its own.
 *
 * `stale` is the live stream being down. The marker is decorative and the
 * status word beside it is written in the present tense, so both have to stop
 * claiming anything while the data is frozen: a column hovered ten minutes
 * after the connection dropped would otherwise report the last known status as
 * the current one.
 */
function tooltipReadout(
  slot: Extract<Slot, { kind: "beat" }>,
  stale: boolean,
): { rows: TooltipRow[]; unit?: string } {
  const marker: ChipStatus = slot.ok
    ? slot.latencyMs === null
      ? "warn"
      : "up"
    : "down";
  const status = slot.ok
    ? slot.latencyMs === null
      ? "No timing"
      : "Up"
    : "Down";
  const mixed = slot.downCount > 0;
  const rows: TooltipRow[] = [
    {
      key: "latency",
      label: slot.count > 1 ? "Slowest" : "Latency",
      value: mixed
        ? formatLatency(slot.latencyMs)
        : latencyValue(slot.latencyMs),
      ...(stale ? { status: "Not updating" } : { marker, status }),
    },
  ];
  if (mixed) {
    rows.push({
      key: "failed",
      label: "Failed",
      value: `${slot.downCount}`,
      ...(stale
        ? { status: "Not updating" }
        : { marker: "down" as ChipStatus, status: "Down" }),
    });
  }
  return { rows, unit: mixed ? undefined : latencyUnit(slot.latencyMs) };
}

/**
 * Width of the element, tracked live.
 *
 * Measurement always runs, and `fallback` only stands in when it yields
 * nothing. The earlier version skipped measuring entirely whenever a
 * `fallback` was passed, which made every caller's "jsdom fallback" the real
 * width in the browser too: the detail view drew a 720px bar inside a 317px
 * panel on a 375px phone and pushed the whole page 371px wide (SUB-29). A
 * width the caller cannot know — it depends on the viewport — must never win
 * over one the browser can measure.
 */
function useMeasuredWidth(
  ref: React.RefObject<HTMLElement | null>,
  fallback?: number,
): number {
  const [measured, setMeasured] = useState(0);

  useLayoutEffect(() => {
    const node = ref.current;
    if (!node) return;
    const measure = () => setMeasured(node.getBoundingClientRect().width);
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(node);
    return () => observer.disconnect();
  }, [ref]);

  // A measured 0 means "no layout here" (jsdom, SSR, a display:none ancestor),
  // never "zero pixels wide", so it is the one case the fallback covers.
  return measured > 0 ? measured : (fallback ?? 0);
}

/** Sentence read out when the graphic receives focus, and shown to nobody else. */
function describe(label: string, slots: Slot[]): string {
  const { checks, failed, span } = summarise(slots);
  if (checks === 0) return `${label}: no checks yet.`;
  const window = span
    ? ` between ${formatTime(span[0])} and ${formatTime(span[1])}`
    : "";
  const health = failed === 0 ? "all passed" : `${failed} failed`;
  return `${label}: ${checks} checks${window}, ${health}. Bar height is latency; a failed check is drawn full height.`;
}

/**
 * The heartbeat bar: one column per check, colour for status and height for
 * latency (DESIGN.md §4).
 *
 * Two decisions drive the implementation:
 *
 * 1. **The column count follows the available width, not the data.** More
 *    checks than columns are bucketed (worst check wins), fewer are padded
 *    with empty slots. That is what keeps 20 and 500 beats equally readable
 *    without ever hiding a failure.
 * 2. **The pixels are `aria-hidden` and a table carries the content.** A
 *    hundred `aria-label`ed rects linearise into a hundred unlabelled stops in
 *    a screen reader; a table is navigable structure. Sighted keyboard users
 *    get arrow-key traversal over the same slots with a live announcement.
 */
export function HeartbeatBar({
  beats,
  label,
  height = 34,
  barWidth = 6,
  gap = 3,
  width,
  className,
  interactive = true,
  stale = false,
  framed = false,
  legend,
}: HeartbeatBarProps) {
  const trackRef = useRef<HTMLDivElement>(null);
  const measured = useMeasuredWidth(trackRef, width);
  const [active, setActive] = useState<number | null>(null);
  const [focused, setFocused] = useState(false);

  const slotCount = slotCountFor(measured, barWidth, gap);
  const slots = useMemo(() => toSlots(beats, slotCount), [beats, slotCount]);
  const ceiling = useMemo(() => latencyCeiling(slots), [slots]);

  const newest = slots.length > 0 ? slots[slots.length - 1] : undefined;
  const newestTs = newest && newest.kind === "beat" ? newest.to : null;

  // Animate the newest column only when it is genuinely new. Remounting the
  // whole row (a resize, a monitor switch) must not replay the arrival of a
  // check that landed minutes ago.
  const seenTs = useRef<number | null>(newestTs);
  const [arriving, setArriving] = useState<number | null>(null);
  useEffect(() => {
    if (
      newestTs !== null &&
      seenTs.current !== null &&
      newestTs !== seenTs.current
    ) {
      setArriving(newestTs);
      const timer = setTimeout(() => setArriving(null), 560);
      seenTs.current = newestTs;
      return () => clearTimeout(timer);
    }
    seenTs.current = newestTs;
  }, [newestTs]);

  const step = barWidth + gap;
  const trackWidth = slotCount > 0 ? slotCount * step - gap : 0;

  const indexAt = (clientX: number): number | null => {
    const node = trackRef.current;
    if (!node || slotCount === 0) return null;
    const rect = node.getBoundingClientRect();
    const index = Math.floor((clientX - rect.left) / step);
    return index >= 0 && index < slotCount ? index : null;
  };

  const move = (delta: number) => {
    setActive((current) => {
      const from = current ?? slotCount - 1;
      return Math.min(Math.max(from + delta, 0), Math.max(slotCount - 1, 0));
    });
  };

  const onKeyDown = (event: React.KeyboardEvent) => {
    const keys: Record<string, () => void> = {
      ArrowRight: () => move(1),
      ArrowLeft: () => move(-1),
      Home: () => setActive(0),
      End: () => setActive(slotCount - 1),
      Escape: () => setActive(null),
    };
    const handler = keys[event.key];
    if (!handler) return;
    event.preventDefault();
    handler();
  };

  const activeSlot = active !== null ? slots[active] : undefined;

  // The tooltip is positioned after it exists, because its width depends on
  // its text. Measuring in a layout effect keeps that off-screen: the browser
  // paints once, already in the right place.
  //
  // The position is a function of four things that can all change while the
  // tooltip stays open: the active column, the track's position in the
  // viewport, the viewport width, and the tooltip's own width (its text
  // changes when a bucket gains a check). Re-running only on
  // `active`/`step`/`barWidth` leaves a stale offset behind after a window
  // resize or a scroll, which is exactly how the tooltip ends up half off
  // screen again. So the measurement is repeated on every one of those
  // signals.
  const tooltipRef = useRef<HTMLDivElement>(null);
  const [tooltipX, setTooltipX] = useState(0);
  const place = useCallback(() => {
    const track = trackRef.current;
    const tooltip = tooltipRef.current;
    if (active === null || !track || !tooltip) return;
    setTooltipX(
      tooltipLeft({
        columnCentre: active * step + barWidth / 2,
        tooltipWidth:
          tooltip.getBoundingClientRect().width || TOOLTIP_MIN_WIDTH,
        trackLeft: track.getBoundingClientRect().left,
        viewportWidth: window.innerWidth,
      }),
    );
  }, [active, step, barWidth]);
  useLayoutEffect(() => {
    if (active === null) return;
    // `activeSlot` is read so a change of tooltip text re-measures even where
    // ResizeObserver is unavailable.
    void activeSlot;
    place();
    window.addEventListener("resize", place);
    // Capturing: the track can be moved by any scrolling ancestor, not only
    // the window.
    window.addEventListener("scroll", place, true);
    const tooltip = tooltipRef.current;
    const observer =
      tooltip && typeof ResizeObserver !== "undefined"
        ? new ResizeObserver(place)
        : undefined;
    observer?.observe(tooltip!);
    return () => {
      window.removeEventListener("resize", place);
      window.removeEventListener("scroll", place, true);
      observer?.disconnect();
    };
  }, [active, activeSlot, place]);
  const description = describe(label, slots);

  // Hover still works without interactivity: pointing at a column is not a
  // promise to assistive technology, and it is the one affordance a list bar
  // can keep for free.
  const trackProps = interactive
    ? {
        tabIndex: 0,
        role: "group",
        "aria-label": description,
        onKeyDown,
        onFocus: () => setFocused(true),
        onBlur: () => {
          setFocused(false);
          setActive(null);
        },
      }
    : {};

  const track = (
    <div
      ref={trackRef}
      className="hb-track"
      style={{ height }}
      {...trackProps}
      onPointerMove={(event) => setActive(indexAt(event.clientX))}
      onPointerLeave={() => {
        if (!focused) setActive(null);
      }}
    >
      <svg
        aria-hidden="true"
        width={trackWidth}
        height={height}
        viewBox={`0 0 ${Math.max(trackWidth, 1)} ${height}`}
        style={{ display: "block", overflow: "visible" }}
      >
        {slots.map((slot, index) => {
          const status = slotStatus(slot);
          // An empty slot still draws: a 2px stub reads as "no data here",
          // whereas a gap reads as a rendering bug.
          const h =
            slot.kind === "empty"
              ? 2
              : Math.max(barHeight(slot, ceiling) * height, 2);
          const isNew = arriving !== null && index === slots.length - 1;
          const classes = [
            "hb-bar",
            `hb-bar--${status}`,
            index === active ? "hb-bar--active" : "",
            isNew ? "hb-bar--new" : "",
          ]
            .filter(Boolean)
            .join(" ");
          return (
            <rect
              key={index}
              data-testid={`hb-slot-${index}`}
              data-status={status}
              className={classes}
              x={index * step}
              y={height - h}
              width={barWidth}
              height={h}
              rx={Math.min(2, barWidth / 2)}
            />
          );
        })}
      </svg>

      {activeSlot && activeSlot.kind === "beat" && (
        <div
          ref={tooltipRef}
          className="hb-tooltip"
          data-testid="hb-tooltip"
          style={{ left: tooltipX }}
        >
          <Tooltip
            timestamp={
              activeSlot.count > 1
                ? `${formatTime(activeSlot.from)} – ${formatTime(activeSlot.to)}`
                : formatTime(activeSlot.to)
            }
            unit={tooltipReadout(activeSlot, stale).unit}
            rows={tooltipReadout(activeSlot, stale).rows}
            total={
              activeSlot.count > 1
                ? { label: "Total", value: `${activeSlot.count} checks` }
                : undefined
            }
            partial={activeSlot.partial}
            footer={
              !activeSlot.ok && (activeSlot.error || activeSlot.statusCode)
                ? `${activeSlot.statusCode ? `${activeSlot.statusCode} ` : ""}${activeSlot.error ?? ""}`
                : undefined
            }
          />
        </div>
      )}
    </div>
  );

  const uptime = summarise(slots);
  const pct =
    uptime.checks === 0
      ? "—"
      : `${(((uptime.checks - uptime.failed) / uptime.checks) * 100).toFixed(2)}%`;

  // The chrome states the window it is drawing, not the clock: a bar showing
  // yesterday's history must not label itself with now.
  const plot = framed ? (
    <Chart
      headline={pct}
      breakdown={
        uptime.checks === 0
          ? "no checks yet"
          : `${uptime.checks} checks · ${uptime.failed} failed`
      }
      start={uptime.span ? formatCorner(uptime.span[0]) : undefined}
      end={uptime.span ? formatCorner(uptime.span[1]) : undefined}
      legend={legend}
    >
      {track}
    </Chart>
  ) : (
    track
  );

  return (
    <figure
      className={className}
      style={{ margin: 0 }}
      aria-hidden={interactive ? undefined : true}
    >
      {plot}


      {/* The load-bearing text alternative, and the reason the pixels may be
          `aria-hidden`. Bounded by the column count, so a 500-beat series is
          still a readable table. Dropped entirely in list layouts: one table
          per monitor is 5,600 rows the row's own text already covers. */}
      {interactive && (
      <figcaption className="hb-sr-only">
        <table>
          <caption>{description}</caption>
          <thead>
            <tr>
              <th scope="col">Time</th>
              <th scope="col">Result</th>
              <th scope="col">Latency</th>
            </tr>
          </thead>
          <tbody>
            {slots
              .filter(
                (slot): slot is Extract<Slot, { kind: "beat" }> =>
                  slot.kind === "beat",
              )
              .map((slot) => (
                <tr key={slot.index}>
                  <td>
                    {slot.count > 1
                      ? `${formatTime(slot.from)} – ${formatTime(slot.to)}`
                      : formatTime(slot.to)}
                  </td>
                  <td>
                    {slot.ok
                      ? "passed"
                      : `failed${slot.error ? `: ${slot.error}` : ""}`}
                    {slot.count > 1 ? ` (${slot.count} checks)` : ""}
                  </td>
                  <td>{formatLatency(slot.latencyMs)}</td>
                </tr>
              ))}
          </tbody>
        </table>
      </figcaption>
      )}

      {interactive && (
        <div aria-live="polite" className="hb-sr-only">
          {focused && activeSlot && activeSlot.kind === "beat"
            ? `${formatTime(activeSlot.to)}, ${activeSlot.ok ? "passed" : "failed"}, ${formatLatency(activeSlot.latencyMs)}`
            : ""}
        </div>
      )}
    </figure>
  );
}
