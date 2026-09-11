import { memo } from "react";
import { HeartbeatBar } from "../heartbeat/HeartbeatBar";
import { Led } from "./Led";
import type { Monitor } from "./types";

/**
 * One monitor as one table row.
 *
 * Memoised because the dashboard renders up to 200 of these and each one
 * carries a ~40-rect heartbeat bar. There is no virtualisation here by
 * decision (DESIGN.md §10, research note 1): 200 rows is roughly 11k DOM
 * nodes, which the browser handles fine, and a virtualised list breaks
 * Ctrl-F, breaks screen-reader row counts and breaks table semantics. What it
 * does need is that a tick on one monitor does not re-render the other 199 —
 * that is this `memo` plus a stable `key={monitor.id}`.
 */

/** How many heartbeat columns a row shows. DESIGN.md §4: the last 28 checks. */
export const ROW_BEAT_WIDTH = 168;

/**
 * A value we do not have, drawn as an em dash.
 *
 * Never `0`. A monitor that has never reported a latency and one that
 * answered instantly are different facts, and rendering both as `0 ms` makes
 * the dashboard confidently wrong. The dash is decorative; the real reason is
 * in the accessible text beside it.
 */
function Unknown({ what }: { what: string }) {
  return (
    <>
      <span aria-hidden="true">—</span>
      <span className="sr-only">No {what} data</span>
    </>
  );
}

const formatLatency = (ms: number) =>
  ms >= 1000 ? `${(ms / 1000).toFixed(2)} s` : `${Math.round(ms)} ms`;

/** Uptime to one decimal, so 99.95 does not round up to a perfect 100%. */
const formatUptime = (pct: number) => `${pct.toFixed(pct >= 99.95 || pct === 0 ? 0 : 1)}%`;

export type MonitorRowProps = {
  monitor: Monitor;
  /**
   * Explicit heartbeat width. jsdom reports every element as 0 wide, so
   * without this the bar renders empty in tests; in the browser the default
   * lets the bar measure its own column.
   */
  beatWidth?: number;
};

function MonitorRowImpl({ monitor, beatWidth = ROW_BEAT_WIDTH }: MonitorRowProps) {
  const { name, status, target, latencyMs, uptime24h, beats, error } = monitor;

  return (
    <tr className="mon-row" data-status={status} data-testid={`monitor-row-${monitor.id}`}>
      {/* The lamp's label is hidden here: the row's accessible name already
          carries the status, so a visible repeat would be noise. */}
      <td className="mon-cell mon-cell--led">
        <Led status={status} />
      </td>

      {/* scope="row" makes the name the row's header, so a screen reader
          announces "api.example.com, Latency, 120 ms" when you move across
          the row instead of reading a bare number. */}
      <th scope="row" className="mon-cell mon-cell--name">
        <span className="mon-name">{name}</span>
        <span className="mon-target">{target}</span>
      </th>

      <td className="mon-cell mon-cell--beats">
        <HeartbeatBar beats={beats} label={name} width={beatWidth} height={26} barWidth={4} gap={2} />
      </td>

      <td className="mon-cell mon-cell--num">
        {status === "down" && error ? (
          // A failed check has no latency to report, so the column carries the
          // reason instead — colour plus text, never colour alone (§2.3).
          <span className="mon-error" title={error}>
            {error}
          </span>
        ) : latencyMs === null ? (
          <Unknown what="latency" />
        ) : (
          formatLatency(latencyMs)
        )}
      </td>

      <td className="mon-cell mon-cell--num">
        {uptime24h === null ? <Unknown what="uptime" /> : formatUptime(uptime24h)}
      </td>
    </tr>
  );
}

/**
 * Compares only the fields the row draws. The default shallow compare would
 * do, but `beats` is a fresh array on every poll even when the checks are
 * identical, which would defeat the memo for exactly the update it exists to
 * absorb.
 */
export const MonitorRow = memo(MonitorRowImpl, (prev, next) => {
  const a = prev.monitor;
  const b = next.monitor;
  return (
    prev.beatWidth === next.beatWidth &&
    a.id === b.id &&
    a.name === b.name &&
    a.status === b.status &&
    a.target === b.target &&
    a.latencyMs === b.latencyMs &&
    a.uptime24h === b.uptime24h &&
    a.error === b.error &&
    a.lastCheck === b.lastCheck &&
    a.beats.length === b.beats.length &&
    // Beats are append-only and oldest-first, so the newest timestamp is a
    // sufficient fingerprint for "the series changed".
    a.beats[a.beats.length - 1]?.ts === b.beats[b.beats.length - 1]?.ts
  );
});
