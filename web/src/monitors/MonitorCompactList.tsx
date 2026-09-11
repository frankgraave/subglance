import { memo } from "react";
import { EmptyState } from "./EmptyState";
import { formatLatency, formatUptime } from "./format";
import { Led } from "./Led";
import { partition } from "./model";
import type { Monitor } from "./types";

/**
 * The dense layout: one line per monitor, for screens holding 100+.
 *
 * **Why a list and not a shorter table.** The row layout earns its `<table>`
 * by inviting comparison down a column — latency against latency, uptime
 * against uptime. This layout is for the opposite question: "is anything red?"
 * across a hundred lines you are not reading individually. Dropping to a list
 * halves the DOM per monitor, keeps find-in-page working, and stops promising
 * a grid whose columns nobody scans.
 *
 * **What it keeps.** Lamp, name, target, latency, uptime — the same facts,
 * just on one line with the heartbeat bar dropped. The bar is the one thing
 * that cannot survive this density: 40 rects x 200 monitors is the cost the
 * layout exists to avoid, and at this line height it would be 8px of noise.
 * Anyone who wants the trend switches to Rows, which is one click away.
 *
 * **No grouping yet, deliberately.** DESIGN.md §7 groups this layout by
 * customer or environment; §12 records that tags have no screen to create or
 * assign them, so grouping today would mean inventing a taxonomy in the
 * frontend. So this renders one flat `<ul>`: `partition` (down first, then
 * alphabetical) only orders it, which is the ordering every other layout
 * shares. Headed "Needs attention" / "All monitors" sections would be grouping
 * by a different name, and half a taxonomy reads worse than none — at this
 * density the broken monitors are already the first lines on the screen.
 */

export type MonitorCompactListProps = {
  monitors: readonly Monitor[];
  /** Non-empty when the list has been filtered, used only for empty-state copy. */
  query?: string;
  /** Total before filtering, so "no results" can be told from "no monitors". */
  totalCount?: number;
};

function CompactLineImpl({ monitor }: { monitor: Monitor }) {
  const { name, status, target, latencyMs, uptime24h } = monitor;
  return (
    <li className="mon-line" data-status={status} data-testid={`monitor-line-${monitor.id}`}>
      <Led status={status} className="mon-line-led" />
      <span className="mon-line-name">{name}</span>
      <span className="mon-line-target">{target}</span>
      <span className="mon-line-num">{latencyMs === null ? "—" : formatLatency(latencyMs)}</span>
      <span className="mon-line-num">{uptime24h === null ? "—" : formatUptime(uptime24h)}</span>
    </li>
  );
}

/** Memoised for the same reason MonitorRow is: one tick must not redraw 199 lines. */
const CompactLine = memo(CompactLineImpl, (prev, next) => {
  const a = prev.monitor;
  const b = next.monitor;
  return (
    a.id === b.id &&
    a.name === b.name &&
    a.status === b.status &&
    a.target === b.target &&
    a.latencyMs === b.latencyMs &&
    a.uptime24h === b.uptime24h
  );
});

export function MonitorCompactList({
  monitors,
  query = "",
  totalCount,
}: MonitorCompactListProps) {
  const total = totalCount ?? monitors.length;
  if (monitors.length === 0) {
    return <EmptyState query={query} totalCount={total} />;
  }

  // `partition` orders the list — down first, then alphabetical — and that is
  // all it does here. Splitting the result into headed sections would be the
  // grouping this layout deliberately ships without.
  const { attention, rest } = partition(monitors);
  const ordered = [...attention, ...rest];

  return (
    <div className="mon-lines">
      <ul className="mon-line-stack" aria-label={`Monitors (${ordered.length})`}>
        {ordered.map((monitor) => (
          <CompactLine key={monitor.id} monitor={monitor} />
        ))}
      </ul>
    </div>
  );
}
