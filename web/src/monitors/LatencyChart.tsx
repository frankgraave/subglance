import { useCallback, useLayoutEffect, useRef, useState } from "react";
import { Card, Panel } from "../components/Card";
import { Chart } from "../components/Chart";
import { IconClock } from "../components/icons";
import { SegmentedControl } from "../components/SegmentedControl";
import { Tooltip } from "../components/Tooltip";
import { tooltipLeft } from "../heartbeat/model";
import { formatLatency } from "./format";
import {
  LATENCY_WINDOWS,
  measuredRuns,
  nearestPoint,
  summariseLatency,
  xOf,
  yOf,
  type LatencyPoint,
  type LatencySeries,
  type LatencyWindow,
} from "./latency";

/**
 * Latency over a real window, on the monitor's detail page (SUB-23).
 *
 * The heartbeat bar above it already encodes latency as bar height, but only
 * over the last hundred checks the stream carries — about an hour and a half
 * at the default interval. This answers the question that bar cannot: "has it
 * been getting slower this week?".
 *
 * Drawn by hand inside the shared chart chrome rather than with a chart
 * library (DESIGN.md §10), and in the ink scale rather than in a status
 * colour: latency is a measurement, not a state. The only status colour on
 * the plot is the down tick along the baseline, because a step with a
 * confirmed outage *is* a state, and it is the one thing on this chart that
 * should be findable at a glance.
 *
 * Three decisions that are easy to undo by accident:
 *
 * - **The line breaks at a gap.** A step with no checks, or whose checks all
 *   failed, ends the line; the next measured step starts a new one. Bridging
 *   the gap would draw a latency nobody measured — across an outage, it would
 *   draw the service as answering when it was not.
 * - **The scale is the peak.** The chrome draws no axis, so the top of the
 *   plot is the highest step average, and that number is on screen in the
 *   breakdown. A fixed scale would flatten a 40ms service into the floor.
 * - **The previous window stays up while the next one loads.** Switching
 *   from 24h to 7d swaps the line in place instead of blanking the card to a
 *   loading line and back, which is what makes the switch read as the same
 *   chart re-measured rather than as a new page.
 */

export type LatencyChartProps = {
  window: LatencyWindow;
  onWindowChange: (next: LatencyWindow) => void;
  series?: LatencySeries;
  loading?: boolean;
  error?: Error | null;
  /** True while a different window is being fetched over the one on screen. */
  refreshing?: boolean;
  /** Plot width for environments without layout, such as jsdom. */
  width?: number;
};

const WINDOW_LABELS: Record<LatencyWindow, string> = {
  "24h": "24h",
  "7d": "7d",
  "30d": "30d",
};

/** The viewBox is unitless; the SVG stretches it over the plot. */
const VIEW_W = 1000;
const VIEW_H = 100;

const formatCorner = (ms: number, stepMs: number) =>
  new Date(ms).toLocaleString(undefined, stepMs >= 86_400_000
    ? { month: "short", day: "numeric" }
    : { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });

function describeStep(p: LatencyPoint, stepMs: number): string {
  return `${formatCorner(p.t, stepMs)} – ${formatCorner(p.t + stepMs, stepMs)}`;
}

function runPath(run: LatencyPoint[], series: LatencySeries, ceiling: number): string {
  const step = xOf(series, series.from + series.stepMs) - xOf(series, series.from);
  if (run.length === 1) {
    // One measured step between two gaps: a path through a single point
    // draws nothing, so the step gets a flat segment across its own width.
    const p = run[0];
    const x0 = xOf(series, p.t) * VIEW_W;
    const y = yOf(p.avgMs ?? 0, ceiling) * VIEW_H;
    return `M${x0.toFixed(1)},${y.toFixed(1)}H${(x0 + step * VIEW_W).toFixed(1)}`;
  }
  return run
    .map((p, i) => {
      const x = (xOf(series, p.t) + step / 2) * VIEW_W;
      const y = yOf(p.avgMs ?? 0, ceiling) * VIEW_H;
      return `${i === 0 ? "M" : "L"}${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join("");
}

export function LatencyChart({
  window: selected,
  onWindowChange,
  series,
  loading = false,
  error = null,
  refreshing = false,
  width,
}: LatencyChartProps) {
  const plotRef = useRef<HTMLDivElement>(null);
  const tooltipRef = useRef<HTMLDivElement>(null);
  const [active, setActive] = useState<number | null>(null);
  const [focused, setFocused] = useState(false);
  const [tooltipX, setTooltipX] = useState(0);
  // The window the active index belongs to: a switch drops the readout
  // instead of pointing at whatever step now has the same index.
  const [activeSeries, setActiveSeries] = useState<LatencySeries | undefined>(series);
  if (activeSeries !== series) {
    setActiveSeries(series);
    if (active !== null) setActive(null);
  }

  const points = series?.points ?? [];
  const summary = series ? summariseLatency(series) : null;
  const ceiling = summary?.peakMs ?? 0;
  const activePoint = active !== null ? points[active] : undefined;

  const plotWidth = () => plotRef.current?.getBoundingClientRect().width || width || 0;

  const place = useCallback(() => {
    const plot = plotRef.current;
    const tooltip = tooltipRef.current;
    if (!series || !activePoint || !plot || !tooltip) return;
    const w = plot.getBoundingClientRect().width || width || 0;
    const centre = (xOf(series, activePoint.t) + (series.stepMs / (series.to - series.from)) / 2) * w;
    setTooltipX(tooltipLeft({
      columnCentre: centre,
      tooltipWidth: tooltip.getBoundingClientRect().width,
      trackLeft: plot.getBoundingClientRect().left,
      viewportWidth: globalThis.innerWidth,
    }));
  }, [series, activePoint, width]);
  useLayoutEffect(() => {
    if (!activePoint) return;
    place();
    globalThis.addEventListener("resize", place);
    globalThis.addEventListener("scroll", place, true);
    return () => {
      globalThis.removeEventListener("resize", place);
      globalThis.removeEventListener("scroll", place, true);
    };
  }, [activePoint, place]);

  const move = (delta: number) => {
    if (points.length === 0) return;
    setActive((current) =>
      current === null
        ? delta > 0 ? 0 : points.length - 1
        : Math.min(Math.max(current + delta, 0), points.length - 1));
  };
  const onKeyDown = (event: React.KeyboardEvent) => {
    const keys: Record<string, () => void> = {
      ArrowRight: () => move(1),
      ArrowLeft: () => move(-1),
      Home: () => setActive(points.length > 0 ? 0 : null),
      End: () => setActive(points.length > 0 ? points.length - 1 : null),
      Escape: () => setActive(null),
    };
    const handler = keys[event.key];
    if (!handler) return;
    event.preventDefault();
    if (event.key === "Escape" && active !== null) event.stopPropagation();
    handler();
  };

  const control = (
    <SegmentedControl
      label="Latency window"
      options={LATENCY_WINDOWS.map((id) => ({ id, label: WINDOW_LABELS[id] }))}
      value={selected}
      onChange={onWindowChange}
    />
  );

  let body;
  if (error && !series) {
    body = <p role="alert" className="mon-detail-note">Could not load latency: {error.message}</p>;
  } else if (loading && !series) {
    body = <p className="mon-detail-note">Loading latency…</p>;
  } else if (!series || points.length === 0) {
    body = <p className="mon-detail-note">No checks in the last {WINDOW_LABELS[selected]}.</p>;
  } else {
    const runs = measuredRuns(series);
    const stepFraction = series.stepMs / (series.to - series.from);
    const breakdown = summary!.peakMs === null
      ? `${summary!.checks} checks · no latency measured`
      : `peak ${formatLatency(summary!.peakMs)} · ${summary!.checks} checks${summary!.downSteps > 0 ? ` · down in ${summary!.downSteps} step${summary!.downSteps === 1 ? "" : "s"}` : ""}`;
    const description = `Average latency over the last ${WINDOW_LABELS[selected]}, one value per ${formatLatencyStep(series.stepMs)}. The line breaks where nothing was measured.`;
    body = (
      <figure className="lat-figure" data-refreshing={refreshing || undefined} aria-busy={refreshing || undefined}>
        <Chart
          headline={summary!.averageMs === null ? "—" : formatLatency(summary!.averageMs)}
          breakdown={breakdown}
          start={formatCorner(series.from, series.stepMs)}
          end={formatCorner(series.to, series.stepMs)}
        >
          {/* A focusable group, the same contract as the heartbeat track: arrow
              keys step through the readout, and the figcaption table carries
              the same numbers for a screen reader that never focuses it. */}
          {/* eslint-disable-next-line jsx-a11y/no-noninteractive-element-interactions -- keyboard stepping through the readout needs the handler on the focusable plot */}
          <div
            ref={plotRef}
            className="lat-plot"
            data-testid="lat-plot"
            // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- the readout is keyboard-driven, so the plot must be reachable by Tab
            tabIndex={0}
            role="group"
            aria-label={description}
            onKeyDown={onKeyDown}
            onFocus={() => setFocused(true)}
            onBlur={() => { setFocused(false); setActive(null); }}
            onPointerMove={(event) => {
              const rect = event.currentTarget.getBoundingClientRect();
              const w = rect.width || plotWidth();
              if (w <= 0) return;
              setActive(nearestPoint(series, (event.clientX - rect.left) / w));
            }}
            onPointerLeave={() => { if (!focused) setActive(null); }}
          >
            <svg
              aria-hidden="true"
              className="lat-svg"
              viewBox={`0 0 ${VIEW_W} ${VIEW_H}`}
              preserveAspectRatio="none"
            >
              {runs.map((run) => (
                <path
                  key={run[0].t}
                  className="lat-line"
                  data-testid="lat-run"
                  d={runPath(run, series, ceiling)}
                  vectorEffect="non-scaling-stroke"
                />
              ))}
              {points.filter((p) => p.down > 0).map((p) => (
                <rect
                  key={p.t}
                  className="lat-down"
                  data-testid="lat-down"
                  x={xOf(series, p.t) * VIEW_W}
                  y={VIEW_H - 4}
                  width={Math.max(stepFraction * VIEW_W, 2)}
                  height={4}
                />
              ))}
              {activePoint && (
                <line
                  className="lat-cursor"
                  x1={(xOf(series, activePoint.t) + stepFraction / 2) * VIEW_W}
                  x2={(xOf(series, activePoint.t) + stepFraction / 2) * VIEW_W}
                  y1={0}
                  y2={VIEW_H}
                  vectorEffect="non-scaling-stroke"
                />
              )}
            </svg>
            {activePoint && (
              <div ref={tooltipRef} className="hb-tooltip" data-testid="lat-tooltip" style={{ left: tooltipX }}>
                <Tooltip
                  timestamp={describeStep(activePoint, series.stepMs)}
                  rows={activePoint.avgMs === null
                    ? [{ key: "none", label: "Latency", value: "not measured" }]
                    : [
                        { key: "avg", label: "Average", value: formatLatency(activePoint.avgMs) },
                        { key: "min", label: "Fastest", value: activePoint.minMs === null ? "—" : formatLatency(activePoint.minMs) },
                        { key: "max", label: "Slowest", value: activePoint.maxMs === null ? "—" : formatLatency(activePoint.maxMs) },
                      ]}
                  total={{ label: "Checks", value: `${activePoint.checks}` }}
                  footer={activePoint.down > 0 ? `${activePoint.down} confirmed down` : undefined}
                />
              </div>
            )}
          </div>
        </Chart>
        <figcaption className="sr-only">
          <table>
            <caption>{description}</caption>
            <thead>
              <tr>
                <th scope="col">Period</th>
                <th scope="col">Average</th>
                <th scope="col">Checks</th>
              </tr>
            </thead>
            <tbody>
              {points.map((p) => (
                <tr key={p.t}>
                  <td>{describeStep(p, series.stepMs)}</td>
                  <td>{p.avgMs === null ? "not measured" : formatLatency(p.avgMs)}</td>
                  <td>{p.checks}{p.down > 0 ? `, ${p.down} confirmed down` : ""}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </figcaption>
        {error ? <p role="alert" className="mon-detail-note">Could not refresh latency: {error.message}. Showing the last loaded window.</p> : null}
      </figure>
    );
  }

  return (
    <Card title="Latency" icon={<IconClock />} headingLevel={2} action={control}>
      <Panel>{body}</Panel>
    </Card>
  );
}

function formatLatencyStep(stepMs: number): string {
  const minutes = Math.round(stepMs / 60_000);
  if (minutes < 60) return `${minutes} minutes`;
  const hours = minutes / 60;
  if (hours < 24) return hours === 1 ? "hour" : `${hours} hours`;
  const days = hours / 24;
  return days === 1 ? "day" : `${days} days`;
}
