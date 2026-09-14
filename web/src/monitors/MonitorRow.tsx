import { memo } from "react";
import { HeartbeatBar } from "../heartbeat/HeartbeatBar";
import { describeTarget, formatLatency, formatUptime } from "./format";
import { Led } from "./Led";
import { MonitorLink } from "./MonitorLink";
import { Unknown } from "./Unknown";
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

export type MonitorRowProps = {
  monitor: Monitor;
  /**
   * Heartbeat width for environments without layout. jsdom reports every
   * element as 0 wide, so without this the bar renders empty in tests; in the
   * browser the bar measures its own column and this is not used.
   */
  beatWidth?: number;
  /** Opens this monitor's detail view client-side. See MonitorLink. */
  onOpen?: (id: string) => void;
};

function MonitorRowImpl({
  monitor,
  beatWidth = ROW_BEAT_WIDTH,
  onOpen,
}: MonitorRowProps) {
  const { name, status, latencyMs, uptime24h, beats, error } = monitor;

  return (
    <tr
      className="mon-row"
      data-status={status}
      data-testid={`monitor-row-${monitor.id}`}
    >
      {/* The word is shown for everything except `up` (SUB-100).
          Hidden for all four statuses, the only difference between down,
          pending and paused in this layout was hue — red, amber and grey at
          the same 2px edge and the same filled 20x7 pill — which is rule
          "never colour alone" broken for exactly the readers it exists for.
          Showing it for `up` as well would print the same word down 190 rows
          and drown the three that matter, so the quiet default stays quiet:
          no word *is* the up signal, and it is not a colour. */}
      <td className="mon-cell mon-cell--led">
        <Led status={status} hideLabel={status === "up"} />
      </td>

      {/* scope="row" makes the name the row's header, so a screen reader
          announces "api.example.com, Latency, 120 ms" when you move across
          the row instead of reading a bare number. */}
      <th scope="row" className="mon-cell mon-cell--name">
        <MonitorLink
          id={monitor.id}
          name={name}
          onOpen={onOpen}
          className="mon-name"
        />
        <span className="mon-target">{describeTarget(monitor)}</span>
      </th>

      <td className="mon-cell mon-cell--beats">
        {/* Not interactive here (SUB-100). One focusable bar per row put 402
            tab stops in front of the last row's link at 200 monitors, and one
            sr-only table per row put the DOM at ~33k nodes against the ~11k
            budget above. The row already says status, latency, uptime and the
            failure reason in text; the bar is the trend, and the detail view
            keeps the readable version. */}
        <HeartbeatBar
          beats={beats}
          label={name}
          width={beatWidth}
          height={26}
          barWidth={4}
          gap={2}
          interactive={false}
        />
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
        {uptime24h === null ? (
          <Unknown what="uptime" />
        ) : (
          formatUptime(uptime24h)
        )}
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
    // Compared, not ignored: the row renders it into a link's handler, so a
    // changed callback must reach the DOM. It is a stable useCallback in
    // practice, so this costs nothing.
    prev.onOpen === next.onOpen &&
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
