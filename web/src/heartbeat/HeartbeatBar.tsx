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
   * Overrides the measured width. Only for environments without layout
   * (tests, SSR); in the browser the component sizes itself to its container.
   */
  width?: number;
  className?: string;
};

const formatTime = (ts: number) =>
  new Date(ts).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });

const formatLatency = (ms: number | null) =>
  ms === null
    ? "no timing"
    : ms >= 1000
      ? `${(ms / 1000).toFixed(2)} s`
      : `${ms} ms`;

/**
 * Width of the element, tracked live.
 *
 * Falls back to `fallback` when there is no layout to measure — jsdom reports
 * every element as 0 wide and would otherwise render an empty component in
 * every test.
 */
function useMeasuredWidth(
  ref: React.RefObject<HTMLElement | null>,
  fallback?: number,
): number {
  const [measured, setMeasured] = useState(0);

  useLayoutEffect(() => {
    if (fallback !== undefined) return;
    const node = ref.current;
    if (!node) return;
    const measure = () => setMeasured(node.getBoundingClientRect().width);
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(node);
    return () => observer.disconnect();
  }, [ref, fallback]);

  // Derived during render rather than mirrored into state: an explicit width
  // is an input, not something to synchronise.
  return fallback ?? measured;
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

  return (
    <figure className={className} style={{ margin: 0 }}>
      <div
        ref={trackRef}
        className="hb-track"
        style={{ height }}
        tabIndex={0}
        role="group"
        aria-label={description}
        onKeyDown={onKeyDown}
        onFocus={() => setFocused(true)}
        onBlur={() => {
          setFocused(false);
          setActive(null);
        }}
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
            <div className="text-helper text-ink-2">
              {activeSlot.count > 1
                ? `${formatTime(activeSlot.from)} – ${formatTime(activeSlot.to)}`
                : formatTime(activeSlot.to)}
            </div>
            <div className="text-body font-strong text-ink">
              {activeSlot.ok ? formatLatency(activeSlot.latencyMs) : "Failed"}
              {activeSlot.count > 1 && (
                <span className="text-ink-3">
                  {" "}
                  · {activeSlot.count} checks
                  {activeSlot.downCount > 0
                    ? `, ${activeSlot.downCount} failed`
                    : ""}
                </span>
              )}
            </div>
            {!activeSlot.ok && (activeSlot.error || activeSlot.statusCode) && (
              <div className="text-helper text-down">
                {activeSlot.statusCode ? `${activeSlot.statusCode} ` : ""}
                {activeSlot.error}
              </div>
            )}
          </div>
        )}
      </div>

      {/* The load-bearing text alternative. Bounded by the column count, so a
          500-beat series is still a readable table. */}
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

      <div aria-live="polite" className="hb-sr-only">
        {focused && activeSlot && activeSlot.kind === "beat"
          ? `${formatTime(activeSlot.to)}, ${activeSlot.ok ? "passed" : "failed"}, ${formatLatency(activeSlot.latencyMs)}`
          : ""}
      </div>
    </figure>
  );
}
